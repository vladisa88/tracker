package app_test

import (
	"encoding/json"
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

func TestSubscriptionsPersistAndValidate(t *testing.T) {
	db := testDB(t)
	cfg := app.Config{OwnerID: 42}
	h := app.New(db, cfg, &browserProvider{})
	now := time.Date(2028, 2, 28, 21, 30, 0, 0, time.UTC) // February 29 in Moscow
	h.Now = func() time.Time { return now }
	server := httptest.NewServer(h)
	defer server.Close()
	// Origin is configured before requests.
	cfg.Origin = server.URL
	*h = *app.New(db, cfg, &browserProvider{})
	h.Now = func() time.Time { return now }
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, body, csrf string, want int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Origin", server.URL)
		req.Header.Set("X-CSRF-Token", csrf)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: want %d got %d: %s", method, path, want, res.StatusCode, b)
		}
		return b
	}
	request("GET", "/api/meta", "", "", 401)
	request("GET", "/api/subscriptions?state=active", "", "", 401)
	request("POST", "/api/subscriptions", `{}`, "", 401)
	login := func() {
		req, _ := http.NewRequest("GET", server.URL+"/api/auth/telegram/start", nil)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		u, _ := url.Parse(res.Header.Get("Location"))
		request("GET", "/api/auth/telegram/callback?code=owner&state="+u.Query().Get("state"), "", "", 303)
	}
	login()
	var session struct {
		CSRF string `json:"csrfToken"`
	}
	json.Unmarshal(request("GET", "/api/session", "", "", 200), &session)
	meta := request("GET", "/api/meta", "", "", 200)
	if !strings.Contains(string(meta), `"Europe/Moscow"`) {
		t.Fatalf("metadata: %s", meta)
	}
	var metadata struct {
		Periods    []int `json:"periodDays"`
		Currencies []struct {
			Code      string `json:"code"`
			Precision int    `json:"fractionDigits"`
		} `json:"currencies"`
	}
	if err := json.Unmarshal(meta, &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata.Periods) != 4 || metadata.Periods[0] != 30 || metadata.Periods[1] != 90 || metadata.Periods[2] != 180 || metadata.Periods[3] != 365 {
		t.Fatalf("period metadata: %s", meta)
	}
	for _, code := range []string{"RUB", "USD", "EUR"} {
		found := false
		for _, c := range metadata.Currencies {
			if c.Code == code && c.Precision == 2 {
				found = true
			}
		}
		if !found {
			t.Fatalf("currency metadata: %s", meta)
		}
	}
	body := `{"name":"Exact expense","amount":"12345678901234567890.01","currency":"RUB","periodDays":30,"nextPaymentDate":"2028-02-29"}`
	request("POST", "/api/subscriptions", body, "", 403)
	created := request("POST", "/api/subscriptions", body, session.CSRF, 201)
	var saved app.Subscription
	if err := json.Unmarshal(created, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.Amount != "12345678901234567890.01" || saved.Archived || saved.CreatedAt.IsZero() || saved.UpdatedAt.IsZero() {
		t.Fatalf("created: %s", created)
	}
	list := func() []app.Subscription {
		t.Helper()
		var items []app.Subscription
		if err := json.Unmarshal(request("GET", "/api/subscriptions?state=active", "", "", 200), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	find := func(items []app.Subscription) app.Subscription {
		t.Helper()
		for _, s := range items {
			if s.ID == saved.ID {
				return s
			}
		}
		t.Fatal("saved subscription missing")
		return app.Subscription{}
	}
	if find(list()).NextPaymentDate != "2028-02-29" {
		t.Fatal("today moved")
	}
	// New application and connection; dates normalize even after years without reads.
	second := app.New(testDB(t), cfg, &browserProvider{})
	second.Now = h.Now
	*h = *second
	if find(list()).Amount != saved.Amount {
		t.Fatal("amount did not persist")
	}
	for _, tc := range []struct{ now, want string }{
		{"2028-02-29T20:59:59Z", "2028-02-29"},
		{"2028-02-29T21:00:00Z", "2028-03-30"},
		{"2028-12-31T21:00:00Z", "2029-01-24"},
		{"2032-02-28T21:00:00Z", "2032-03-09"},
	} {
		now, _ = time.Parse(time.RFC3339, tc.now)
		login()
		if got := find(list()).NextPaymentDate; got != tc.want {
			t.Fatalf("at %s: %s want %s", tc.now, got, tc.want)
		}
	}
	now = time.Date(2028, 2, 28, 21, 30, 0, 0, time.UTC)
	login()
	json.Unmarshal(request("GET", "/api/session", "", "", 200), &session)
	before := len(list())
	for _, tc := range []struct{ from, to string }{
		{`"Exact expense"`, `"  "`}, {`"12345678901234567890.01"`, `"0"`}, {`"12345678901234567890.01"`, `"-1"`}, {`"12345678901234567890.01"`, `"1.001"`}, {`"12345678901234567890.01"`, `"1e3"`}, {`"12345678901234567890.01"`, `1.2`}, {`"RUB"`, `"XXX"`}, {`:30`, `:31`}, {`"2028-02-29"`, `"2028-02-28"`}, {`"2028-02-29"`, `"2028-02-30"`},
	} {
		b := request("POST", "/api/subscriptions", strings.Replace(body, tc.from, tc.to, 1), session.CSRF, 422)
		if !strings.Contains(string(b), `"fields"`) {
			t.Fatalf("missing field errors: %s", b)
		}
	}
	request("POST", "/api/subscriptions", `{`, session.CSRF, 400)
	request("POST", "/api/subscriptions", `null`, session.CSRF, 422)
	request("POST", "/api/subscriptions", body+` {}`, session.CSRF, 400)
	request("GET", "/api/subscriptions?state=unknown", "", "", 422)
	if len(list()) != before {
		t.Fatal("invalid input created records")
	}
	for _, p := range []string{"90", "180", "365"} {
		request("POST", "/api/subscriptions", strings.Replace(body, `:30`, ":"+p, 1), session.CSRF, 201)
	}
	for _, code := range []string{"USD", "EUR"} {
		request("POST", "/api/subscriptions", strings.Replace(body, `"RUB"`, `"`+code+`"`, 1), session.CSRF, 201)
	}
	for _, name := range []string{"Zeta", "Alpha", "Alpha"} {
		request("POST", "/api/subscriptions", strings.Replace(body, "Exact expense", name, 1), session.CSRF, 201)
	}
	request("POST", "/api/subscriptions", strings.Replace(body, "2028-02-29", "2028-03-05", 1), session.CSRF, 201)
	items := list()
	for i := 1; i < len(items); i++ {
		a, b := items[i-1], items[i]
		if a.NextPaymentDate > b.NextPaymentDate || (a.NextPaymentDate == b.NextPaymentDate && (a.Name > b.Name || (a.Name == b.Name && a.ID > b.ID))) {
			t.Fatal("list not sorted")
		}
	}
	request("GET", "/api/subscriptions?state=archived", "", "", 200)
}
