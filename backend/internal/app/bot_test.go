package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"tracking/internal/app"
)

func TestBotConnectsOwnerAndResumes(t *testing.T) {
	db := testDB(t)
	cfg := app.Config{OwnerID: time.Now().UnixNano()%1000000000000 + 1, BotUsername: "tracking_test_bot", BotToken: "123:test-secret"}
	h := app.New(db, cfg, &fixedBotLogin{id: cfg.OwnerID})
	server := httptest.NewServer(h)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, want int) string {
		t.Helper()
		res, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s: %d %s", path, res.StatusCode, b)
		}
		return string(b)
	}
	get("/api/telegram/connection", 401)
	res, err := client.Get(server.URL + "/api/auth/telegram/start")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	u, _ := url.Parse(res.Header.Get("Location"))
	get("/api/auth/telegram/callback?code=owner&state="+u.Query().Get("state"), 303)
	state := func(want string) {
		t.Helper()
		body := get("/api/telegram/connection", 200)
		var c struct{ Status, BotURL string }
		if json.Unmarshal([]byte(body), &c) != nil || c.Status != want || c.BotURL != "https://t.me/tracking_test_bot" || strings.Contains(body, "secret") {
			t.Fatalf("connection: %s", body)
		}
	}
	state("disconnected")
	fixture := &botFixture{t: t, update: fmt.Sprintf(`{"update_id":1,"message":{"from":{"id":%d},"chat":{"id":777005,"type":"private"},"text":"/start"}}`, cfg.OwnerID)}
	telegram := app.NewBot(cfg)
	telegram.Client = &http.Client{Transport: roundTrip(fixture.transport)}
	worker := app.NewBotWorker(db, cfg, telegram)
	if err := worker.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	state("connected")
	if len(fixture.recipients) != 1 || fixture.recipients[0] != 777005 {
		t.Fatalf("recipients: %v", fixture.recipients)
	}
	// Both the API and poller reopen the database; replayed updates have no effect.
	reopened := testDB(t)
	*h = *app.New(reopened, cfg, &fixedBotLogin{id: cfg.OwnerID})
	worker = app.NewBotWorker(reopened, cfg, telegram)
	if err := worker.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	state("connected")
	if fixture.offset != 2 || len(fixture.recipients) != 1 {
		t.Fatalf("restart: offset=%d recipients=%v", fixture.offset, fixture.recipients)
	}
	fixture.update = fmt.Sprintf(`{"update_id":2,"message":{"from":{"id":%d},"chat":{"id":777005,"type":"private"},"text":"/start"}}`, cfg.OwnerID)
	results := make(chan error, 2)
	go func() { results <- worker.Poll(context.Background()) }()
	go func() { results <- app.NewBotWorker(db, cfg, telegram).Poll(context.Background()) }()
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if len(fixture.recipients) != 2 {
		t.Fatalf("parallel workers duplicated verification: %v", fixture.recipients)
	}
}

type botFixture struct {
	t          *testing.T
	update     string
	offset     int64
	recipients []int64
	failure    int
}

func (f *botFixture) transport(r *http.Request) (*http.Response, error) {
	f.t.Helper()
	var result string
	if r.URL.Host != "api.telegram.org" || !strings.HasPrefix(r.URL.Path, "/bot123:test-secret/") {
		f.t.Fatalf("unexpected Telegram request: %s", r.URL.Host)
	}
	if strings.HasSuffix(r.URL.Path, "/getUpdates") {
		var body struct {
			Offset         int64
			Limit, Timeout int
			Allowed        []string `json:"allowed_updates"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Limit != 1 || body.Timeout <= 0 || len(body.Allowed) != 1 || body.Allowed[0] != "message" {
			f.t.Fatalf("poll options: %+v", body)
		}
		f.offset = body.Offset
		result = `{"ok":true,"result":[` + f.update + `]}`
	} else if strings.HasSuffix(r.URL.Path, "/sendMessage") {
		var body struct {
			ChatID int64 `json:"chat_id"`
			Text   string
		}
		json.NewDecoder(r.Body).Decode(&body)
		if !strings.Contains(body.Text, "Проверка связи") {
			f.t.Fatalf("test message: %s", body.Text)
		}
		if f.failure != 0 {
			result = `{"ok":false,"error_code":` + fmt.Sprint(f.failure) + `,"description":"secret must not escape","parameters":{"retry_after":7}}`
		} else {
			f.recipients = append(f.recipients, body.ChatID)
			result = `{"ok":true,"result":{"message_id":1}}`
		}
	} else {
		f.t.Fatal("unexpected bot method")
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(result)), Header: make(http.Header)}, nil
}

func TestBotRejectsOtherSendersAndReportsDeliveryFailure(t *testing.T) {
	db := testDB(t)
	cfg := app.Config{OwnerID: time.Now().UnixNano()%1000000000000 + 1, BotUsername: "tracking_test_bot", BotToken: "123:test-secret"}
	// Test the owner-facing status through authenticated HTTP, not database tables.
	h := app.New(db, cfg, &fixedBotLogin{id: cfg.OwnerID})
	srv := httptest.NewServer(h)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(srv.URL + "/api/auth/telegram/start")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	u, _ := url.Parse(res.Header.Get("Location"))
	res, err = client.Get(srv.URL + "/api/auth/telegram/callback?code=owner&state=" + u.Query().Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	state := func(want string) {
		t.Helper()
		res, err := client.Get(srv.URL + "/api/telegram/connection")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body struct{ Status string }
		if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&body) != nil || body.Status != want {
			t.Fatalf("state: %+v (want %s)", body, want)
		}
	}
	f := &botFixture{t: t}
	telegram := app.NewBot(cfg)
	telegram.Client = &http.Client{Transport: roundTrip(f.transport)}
	worker := app.NewBotWorker(db, cfg, telegram)
	update := func(id, sender, chat int64, kind, text string) {
		f.update = fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":%d},"chat":{"id":%d,"type":%q},"text":%q}}`, id, sender, chat, kind, text)
	}
	poll := func() {
		t.Helper()
		if err := worker.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	update(1, 99, 99, "private", "/start")
	poll()
	state("disconnected")
	update(2, cfg.OwnerID, -10, "group", "/start")
	poll()
	state("disconnected")
	update(3, cfg.OwnerID, -10, "supergroup", "/start")
	poll()
	update(4, cfg.OwnerID, 777006, "private", "/starting")
	poll()
	if len(f.recipients) != 0 {
		t.Fatal("unauthorized message sent")
	}
	update(5, cfg.OwnerID, 777006, "private", "/start@tracking_test_bot connect")
	poll()
	state("connected")
	update(6, 99, 99, "private", "/start")
	poll()
	state("connected")
	update(7, cfg.OwnerID, -10, "group", "/start")
	poll()
	state("connected")
	f.failure = 403
	update(8, cfg.OwnerID, 777006, "private", "/start")
	poll()
	state("error")
	f.failure = 0
	worker = app.NewBotWorker(testDB(t), cfg, telegram)
	poll()
	state("error")
	if len(f.recipients) != 1 {
		t.Fatal("permanent error retried without explicit start")
	}
	update(9, cfg.OwnerID, 777006, "private", "/start")
	poll()
	state("connected")
	if len(f.recipients) != 2 {
		t.Fatal("explicit start did not check connection again")
	}
	f.failure = 429
	update(10, cfg.OwnerID, 777006, "private", "/start")
	err = worker.Poll(context.Background())
	var failure *app.BotError
	if !errors.As(err, &failure) || failure.RetryAfter != 7*time.Second || failure.Permanent {
		t.Fatalf("retry: %v", err)
	}
	state("checking")
	f.failure = 0
	worker = app.NewBotWorker(testDB(t), cfg, telegram)
	poll()
	state("connected")
	if len(f.recipients) != 3 || f.recipients[2] != 777006 {
		t.Fatalf("retry delivery: %v", f.recipients)
	}
}

type fixedBotLogin struct{ id int64 }

func (p *fixedBotLogin) AuthorizationURL(state, nonce, challenge string) string {
	return "/test-login?state=" + url.QueryEscape(state)
}
func (p *fixedBotLogin) Exchange(context.Context, string, string, string, time.Time) (int64, error) {
	return p.id, nil
}
