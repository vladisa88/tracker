package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"tracking/internal/app"
)

// Each digest scenario has its own persisted subscriptions, recipient and journal.
func digestScenario(t *testing.T) (*subscriptionAPI, *app.Bot, *digestTransport) {
	t.Helper()
	db := testDB(t)
	schema := fmt.Sprintf("digest_test_%d", time.Now().UnixNano())
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Error(err)
		}
	})
	dsn, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := dsn.Query()
	q.Set("search_path", schema)
	dsn.RawQuery = q.Encode()
	t.Setenv("TEST_DATABASE_URL", dsn.String())
	a := newSubscriptionAPI(t)
	a.config.BotUsername = "tracking_test_bot"
	a.config.BotToken = "123:test-secret"
	f := &digestTransport{t: t}
	bot := app.NewBot(a.config)
	bot.Client = &http.Client{Transport: roundTrip(f.transport)}
	return a, bot, f
}

type digestTransport struct {
	t          *testing.T
	connect    bool
	updateID   int64
	messages   []string
	recipients []int64
	failure    bool
	response   string
	onSend     func(*http.Request) error
}

func (f *digestTransport) transport(r *http.Request) (*http.Response, error) {
	f.t.Helper()
	result := `{"ok":true,"result":[]}`
	if strings.HasSuffix(r.URL.Path, "/getUpdates") && f.connect {
		f.updateID++
		result = fmt.Sprintf(`{"ok":true,"result":[{"update_id":%d,"message":{"from":{"id":42},"chat":{"id":777006,"type":"private"},"text":"/start"}}]}`, f.updateID)
	} else if strings.HasSuffix(r.URL.Path, "/sendMessage") {
		var body struct {
			ChatID    int64 `json:"chat_id"`
			Text      string
			ParseMode string `json:"parse_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Fatal(err)
		}
		if body.ParseMode != "" {
			f.t.Fatal("digest must use literal plain text")
		}
		f.messages = append(f.messages, body.Text)
		f.recipients = append(f.recipients, body.ChatID)
		if f.failure {
			return nil, fmt.Errorf("simulated transport failure")
		}
		if f.onSend != nil {
			if err := f.onSend(r); err != nil {
				return nil, err
			}
		}
		result = `{"ok":true,"result":{"message_id":10}}`
		if f.response != "" {
			result = f.response
		}
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(result))}, nil
}
func connectDigest(t *testing.T, a *subscriptionAPI, bot *app.Bot, f *digestTransport) {
	t.Helper()
	f.connect = true
	if err := app.NewBotWorker(testDB(t), a.config, bot).Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.connect = false
	f.messages = nil
	f.recipients = nil
}
func createDigestSubscription(a *subscriptionAPI, name, date string) app.Subscription {
	a.t.Helper()
	b, err := json.Marshal(map[string]any{"name": name, "amount": "12345678901234567890.01", "currency": "RUB", "periodDays": 30, "nextPaymentDate": date})
	if err != nil {
		a.t.Fatal(err)
	}
	return a.decode(a.request("POST", "/api/subscriptions", string(b), 201))
}
func runDigest(t *testing.T, a *subscriptionAPI, bot *app.Bot, now string) {
	t.Helper()
	at, err := time.Parse(time.RFC3339, now)
	if err != nil {
		t.Fatal(err)
	}
	// Reopen both the database and worker on every cycle, as after a restart.
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at); err != nil {
		t.Fatal(err)
	}
}
func TestDigestDuePaymentsAndRestart(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	today := createDigestSubscription(a, "<b>Сегодня & [сервис](url) 😀</b>", "2028-02-29")
	createDigestSubscription(a, "Будущий платёж", "2028-03-03")
	createDigestSubscription(a, "Завтра", "2028-03-01")
	createDigestSubscription(a, "Дальше", "2028-03-04")
	archived := createDigestSubscription(a, "Архив", "2028-02-29")
	a.request("POST", "/api/subscriptions/"+archived.ID+"/archive", "", 200)
	// No UI reads after advancing a month: the worker must project schedules itself.
	t.Setenv("TZ", "Pacific/Honolulu")
	runDigest(t, a, bot, "2028-03-30T06:59:59Z")
	if len(f.messages) != 0 {
		t.Fatal("daily message before 10 Moscow")
	}
	runDigest(t, a, bot, "2028-03-29T21:00:00-10:00")
	want := "Плановые платежи\n\nСегодня\n<b>Сегодня & [сервис](url) 😀</b> — 12345678901234567890.01 RUB · 2028-03-30\n\nЧерез 3 дня\nБудущий платёж — 12345678901234567890.01 RUB · 2028-04-02\n\nПолный список: " + a.config.Origin + "/"
	if len(f.messages) != 1 || f.messages[0] != want || f.recipients[0] != 777006 {
		t.Fatalf("digest: %v recipients=%v", f.messages, f.recipients)
	}
	a.now = time.Date(2028, 3, 30, 11, 0, 0, 0, time.UTC)
	a.login()
	a.request("PATCH", "/api/subscriptions/"+today.ID, `{"name":"Изменено","amount":"42.99"}`, 200)
	created := createDigestSubscription(a, "Новый расход", "2028-03-30")
	if got := a.find(created.ID, "active"); got == nil || got.Name != "Новый расход" {
		t.Fatal("new expense not immediately visible")
	}
	if got := a.find(today.ID, "active"); got == nil || got.Amount != "42.99" {
		t.Fatal("edit not immediately visible")
	}
	runDigest(t, a, bot, "2028-03-30T11:00:00Z")
	runDigest(t, a, bot, "2028-03-30T20:59:59Z")
	if len(f.messages) != 1 {
		t.Fatal("processed day was resent")
	}
	runDigest(t, a, bot, "2028-03-30T21:00:00Z") // Already the next Moscow date, before 10.
	if len(f.messages) != 1 {
		t.Fatal("new Moscow day sent before 10")
	}
	runDigest(t, a, bot, "2028-03-31T07:00:00Z")
	if len(f.messages) != 2 || !strings.Contains(f.messages[1], "Завтра") || !strings.Contains(f.messages[1], "Дальше") || strings.Contains(f.messages[1], "Новый расход") {
		t.Fatalf("next day is not a fresh forecast: %v", f.messages)
	}
}

func TestDigestEmptyDayStaysProcessed(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	runDigest(t, a, bot, "2028-02-29T07:00:00Z")
	createDigestSubscription(a, "После пустой подборки", "2028-02-29")
	runDigest(t, a, bot, "2028-02-29T11:00:00Z")
	if len(f.messages) != 0 {
		t.Fatalf("empty day recalculated: %v", f.messages)
	}
}

func TestDigestWaitsForConnectionAndOmitsEmptySection(t *testing.T) {
	a, bot, f := digestScenario(t)
	createDigestSubscription(a, "Сегодня без подключения", "2028-02-29")
	runDigest(t, a, bot, "2028-02-29T07:00:00Z")
	if len(f.messages) != 0 {
		t.Fatal("sent to unknown recipient")
	}
	// Login and subscription accounting work without a connected bot.
	if len(a.request("GET", "/api/subscriptions?state=active", "", 200)) == 0 {
		t.Fatal("accounting unavailable")
	}
	connectDigest(t, a, bot, f)
	runDigest(t, a, bot, "2028-02-29T11:00:00Z")
	if len(f.messages) != 1 || !strings.Contains(f.messages[0], "Сегодня без подключения") || strings.Contains(f.messages[0], "Через 3 дня") {
		t.Fatalf("late connection: %v", f.messages)
	}
}

func TestDigestLongMessageIsOneCompactDigest(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	// Each row fits, but 22 rows exceed Telegram's bound, especially in UTF-16.
	for i := 0; i < 22; i++ {
		createDigestSubscription(a, fmt.Sprintf("%02d %s", i, strings.Repeat("😀", 100)), "2028-03-03")
	}
	runDigest(t, a, bot, "2028-02-29T07:00:00Z")
	want := "Плановые платежи: 22\n\nЧерез 3 дня: 22\n\nПолный список: " + a.config.Origin + "/"
	if len(f.messages) != 1 || f.messages[0] != want {
		t.Fatalf("compact digest: %v", f.messages)
	}
	// The link retains normal authorization and carries no subscription or secret.
	anonymous := &http.Client{}
	res, err := anonymous.Get(a.config.Origin + "/api/subscriptions?state=active")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("digest link bypassed authentication")
	}
}

func TestDigestSnapshotSurvivesUnconfirmedAttempt(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	sub := createDigestSubscription(a, "Исходное название", "2028-02-29")
	f.failure = true
	at := time.Date(2028, 2, 29, 7, 0, 0, 0, time.UTC)
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at); err == nil {
		t.Fatal("expected transport failure")
	}
	a.request("PATCH", "/api/subscriptions/"+sub.ID, `{"name":"Новое название","amount":"2.00"}`, 200)
	f.failure = false
	runDigest(t, a, bot, "2028-02-29T11:00:00Z")
	if len(f.messages) != 2 || f.messages[0] != f.messages[1] || !strings.Contains(f.messages[1], "Исходное название") {
		t.Fatalf("snapshot was changed: %v", f.messages)
	}
	runDigest(t, a, bot, "2028-02-29T12:00:00Z")
	if len(f.messages) != 2 {
		t.Fatal("confirmed send repeated")
	}
}

func TestDigestRetryDelaySurvivesRestart(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	createDigestSubscription(a, "Повтор", "2028-02-29")
	f.failure = true
	at := time.Date(2028, 2, 29, 7, 0, 0, 0, time.UTC)
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at); err == nil {
		t.Fatal("expected uncertain failure")
	}
	f.failure = false
	runDigest(t, a, bot, "2028-02-29T07:00:59Z")
	if len(f.messages) != 1 {
		t.Fatal("retry ignored durable delay")
	}
	runDigest(t, a, bot, "2028-02-29T07:01:01Z")
	if len(f.messages) != 2 || f.messages[0] != f.messages[1] {
		t.Fatal("retry lost frozen content")
	}
}

func TestDigestPermanentFailureAndStartRecovery(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	createDigestSubscription(a, "Доступный учёт", "2028-02-29")
	f.response = `{"ok":false,"error_code":403}`
	at := time.Date(2028, 2, 29, 7, 0, 0, 0, time.UTC)
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at); err == nil {
		t.Fatal("expected rejection")
	}
	if body := string(a.request("GET", "/api/telegram/connection", "", 200)); !strings.Contains(body, `"status":"error"`) {
		t.Fatalf("delivery failure invisible: %s", body)
	}
	createDigestSubscription(a, "Можно добавлять", "2028-02-29")
	runDigest(t, a, bot, "2028-02-29T14:00:00Z")
	if len(f.messages) != 1 {
		t.Fatal("retried permanent failure without reconnect")
	}
	f.response = ""
	connectDigest(t, a, bot, f)
	runDigest(t, a, bot, "2028-02-29T14:01:00Z")
	if len(f.messages) != 1 || !strings.Contains(f.messages[0], "Доступный учёт") || strings.Contains(f.messages[0], "Можно добавлять") {
		t.Fatal("reconnection did not resume frozen digest")
	}
	if body := string(a.request("GET", "/api/telegram/connection", "", 200)); !strings.Contains(body, `"status":"connected"`) {
		t.Fatal("start did not restore status")
	}
}

func TestDigestRateLimitAndBackoff(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	createDigestSubscription(a, "Лимит", "2028-02-29")
	f.response = `{"ok":false,"error_code":429,"parameters":{"retry_after":300}}`
	at := time.Date(2028, 2, 29, 7, 0, 0, 0, time.UTC)
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at); err == nil {
		t.Fatal("expected rate limit")
	}
	runDigest(t, a, bot, "2028-02-29T07:04:59Z")
	if len(f.messages) != 1 {
		t.Fatal("ignored Telegram retry_after")
	}
	f.response = `{"ok":false,"error_code":502}`
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at.Add(5*time.Minute+time.Second)); err == nil {
		t.Fatal("expected temporary server failure")
	}
	f.response = ""
	runDigest(t, a, bot, "2028-02-29T07:06:59Z")
	if len(f.messages) != 2 {
		t.Fatal("backoff not increased")
	}
	runDigest(t, a, bot, "2028-02-29T07:07:02Z")
	if len(f.messages) != 3 {
		t.Fatal("retry not resumed")
	}
}

func TestDigestTwoWorkersAndCrashRecovery(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprintf("crash=%v", crash), func(t *testing.T) {
			a, bot, f := digestScenario(t)
			connectDigest(t, a, bot, f)
			createDigestSubscription(a, "Один исполнитель", "2028-02-29")
			db := testDB(t)
			db.SetMaxOpenConns(1)
			var pid int
			if err := db.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			f.onSend = func(r *http.Request) error { close(entered); <-release; return nil }
			at := time.Date(2028, 2, 29, 7, 0, 0, 0, time.UTC)
			result := make(chan error, 1)
			go func() { result <- app.NewDigestWorker(db, a.config, bot).Cycle(context.Background(), at) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("send did not start")
			}
			// A distinct Telegram transport detects any ordinary simultaneous send.
			other := &digestTransport{t: t}
			otherBot := app.NewBot(a.config)
			otherBot.Client = &http.Client{Transport: roundTrip(other.transport)}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := app.NewDigestWorker(testDB(t), a.config, otherBot).Cycle(ctx, at)
			cancel()
			if err != nil || len(other.messages) != 0 {
				close(release)
				t.Fatalf("second worker did not skip claim: %v %v", err, other.messages)
			}
			if crash {
				// Terminate the owning DB session while Telegram may already have accepted.
				if _, err := testDB(t).Exec(`SELECT pg_terminate_backend($1)`, pid); err != nil {
					close(release)
					t.Fatal(err)
				}
			}
			close(release)
			err = <-result
			if crash && err == nil {
				t.Fatal("lost database connection should leave an uncertain send")
			}
			if !crash && err != nil {
				t.Fatal(err)
			}
			f.onSend = nil
			runDigest(t, a, bot, "2028-02-29T07:01:01Z")
			want := 1
			if crash {
				want = 2
			}
			if len(f.messages) != want {
				t.Fatalf("claim recovery: %v", f.messages)
			}
			runDigest(t, a, bot, "2028-02-29T07:02:00Z")
			if len(f.messages) != want {
				t.Fatal("recovered claim remained stuck or resent success")
			}
		})
	}
}

func TestDigestLateDeliveryMidnightAndLongOutage(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	createDigestSubscription(a, "Текущий платёж", "2028-02-29")
	runDigest(t, a, bot, "2028-02-29T14:00:00+03:00")
	if len(f.messages) != 1 {
		t.Fatal("missed 10:00 was not delivered at 14:00")
	}
	// Another due date leaves an uncertain attempt just before midnight.
	createDigestSubscription(a, "Завтра", "2028-03-01")
	f.failure = true
	at := time.Date(2028, 3, 1, 20, 59, 59, 0, time.UTC)
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at); err == nil {
		t.Fatal("expected failure")
	}
	f.failure = false
	runDigest(t, a, bot, "2028-03-01T21:00:00Z")
	runDigest(t, a, bot, "2028-03-02T06:59:59Z")
	if len(f.messages) != 2 {
		t.Fatal("yesterday retried after midnight")
	}
	runDigest(t, a, bot, "2028-03-02T07:00:00Z")
	if len(f.messages) != 2 {
		t.Fatal("stale payment sent on empty new day")
	}
	runDigest(t, a, bot, "2028-05-29T11:00:00Z")
	if len(f.messages) != 3 || !strings.Contains(f.messages[2], "2028-05-29") || strings.Contains(f.messages[2], "2028-03-01") {
		t.Fatalf("outage replayed history: %v", f.messages)
	}
}

func TestDigestTimeoutWithUnknownResult(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	createDigestSubscription(a, "Неизвестный результат", "2028-02-29")
	// Telegram sees the message, but its acknowledgement never reaches the client.
	f.onSend = func(r *http.Request) error { <-r.Context().Done(); return r.Context().Err() }
	bot.Client.Timeout = 20 * time.Millisecond
	at := time.Date(2028, 2, 29, 7, 0, 0, 0, time.UTC)
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at); err == nil {
		t.Fatal("expected timeout")
	}
	f.onSend = nil
	bot.Client.Timeout = 0
	runDigest(t, a, bot, "2028-02-29T07:00:59Z")
	if len(f.messages) != 1 {
		t.Fatal("timeout retry ignored delay")
	}
	runDigest(t, a, bot, "2028-02-29T07:01:01Z")
	if len(f.messages) != 2 || f.messages[0] != f.messages[1] {
		t.Fatal("uncertain result did not retry frozen message")
	}
}

func TestDigestInFlightSendStopsAtMidnight(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	createDigestSubscription(a, "До полуночи", "2028-02-29")
	f.onSend = func(r *http.Request) error {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Error("send is not bounded by Moscow midnight")
		}
		<-r.Context().Done()
		return r.Context().Err()
	}
	at := time.Date(2028, 2, 29, 20, 59, 59, 900000000, time.UTC)
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at); err == nil {
		t.Fatal("expected cutoff")
	}
	f.onSend = nil
	runDigest(t, a, bot, "2028-02-29T21:00:00Z")
	runDigest(t, a, bot, "2028-03-01T07:00:00Z")
	if len(f.messages) != 1 {
		t.Fatal("expired message was resent")
	}
}

func TestDigestOldFailureDoesNotUndoNewStart(t *testing.T) {
	a, bot, f := digestScenario(t)
	connectDigest(t, a, bot, f)
	createDigestSubscription(a, "Проверка восстановления", "2028-02-29")
	f.response = `{"ok":false,"error_code":403}`
	f.onSend = func(r *http.Request) error {
		recovered := &botFixture{t: t, update: `{"update_id":2,"message":{"from":{"id":42},"chat":{"id":777006,"type":"private"},"text":"/start"}}`}
		other := app.NewBot(a.config)
		other.Client = &http.Client{Transport: roundTrip(recovered.transport)}
		return app.NewBotWorker(testDB(t), a.config, other).Poll(context.Background())
	}
	at := time.Date(2028, 2, 29, 7, 0, 0, 0, time.UTC)
	if err := app.NewDigestWorker(testDB(t), a.config, bot).Cycle(context.Background(), at); err == nil {
		t.Fatal("expected old send rejection")
	}
	if body := string(a.request("GET", "/api/telegram/connection", "", 200)); !strings.Contains(body, `"status":"connected"`) {
		t.Fatal("old failure overwrote new start")
	}
}
