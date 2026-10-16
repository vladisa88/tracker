package app_test

import (
	"context"
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

type subscriptionAPI struct {
	t       *testing.T
	server  *httptest.Server
	client  *http.Client
	handler *app.Server
	config  app.Config
	now     time.Time
	csrf    string
}

func newSubscriptionAPI(t *testing.T) *subscriptionAPI {
	t.Helper()
	a := &subscriptionAPI{t: t, now: time.Date(2028, 2, 28, 21, 30, 0, 0, time.UTC)}
	a.config = app.Config{OwnerID: 42}
	a.handler = app.New(testDB(t), a.config, &browserProvider{})
	a.server = httptest.NewServer(a.handler)
	t.Cleanup(a.server.Close)
	a.config.Origin = a.server.URL
	*a.handler = *app.New(testDB(t), a.config, &browserProvider{})
	a.handler.Now = func() time.Time { return a.now }
	jar, _ := cookiejar.New(nil)
	a.client = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	a.login()
	return a
}
func (a *subscriptionAPI) login() {
	a.t.Helper()
	res, err := a.client.Get(a.server.URL + "/api/auth/telegram/start")
	if err != nil {
		a.t.Fatal(err)
	}
	res.Body.Close()
	u, _ := url.Parse(res.Header.Get("Location"))
	a.request("GET", "/api/auth/telegram/callback?code=owner&state="+u.Query().Get("state"), "", 303)
	var session struct {
		CSRF string `json:"csrfToken"`
	}
	json.Unmarshal(a.request("GET", "/api/session", "", 200), &session)
	a.csrf = session.CSRF
}
func (a *subscriptionAPI) request(method, path, body string, want int) []byte {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.server.URL+path, strings.NewReader(body))
	req.Header.Set("Origin", a.server.URL)
	req.Header.Set("X-CSRF-Token", a.csrf)
	res, err := a.client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		a.t.Fatalf("%s %s: want %d got %d: %s", method, path, want, res.StatusCode, b)
	}
	return b
}
func (a *subscriptionAPI) create() app.Subscription {
	a.t.Helper()
	return a.decode(a.request("POST", "/api/subscriptions", `{"name":"Internet","amount":"799.01","currency":"RUB","periodDays":30,"nextPaymentDate":"2028-02-29"}`, 201))
}
func (a *subscriptionAPI) decode(b []byte) app.Subscription {
	a.t.Helper()
	var sub app.Subscription
	if err := json.Unmarshal(b, &sub); err != nil {
		a.t.Fatal(err)
	}
	return sub
}
func (a *subscriptionAPI) find(id, state string) *app.Subscription {
	a.t.Helper()
	var items []app.Subscription
	if err := json.Unmarshal(a.request("GET", "/api/subscriptions?state="+state, "", 200), &items); err != nil {
		a.t.Fatal(err)
	}
	for _, sub := range items {
		if sub.ID == id {
			return &sub
		}
	}
	return nil
}

func TestEditPreservesCurrentDateAndStartsNewPeriodThere(t *testing.T) {
	a := newSubscriptionAPI(t)
	sub := a.create()
	a.now = time.Date(2028, 4, 1, 0, 0, 0, 0, time.UTC)
	a.login()
	updated := a.decode(a.request("PATCH", "/api/subscriptions/"+sub.ID, `{"name":"Updated Internet","amount":"12.34","currency":"EUR","periodDays":90}`, 200))
	if updated.Name != "Updated Internet" || updated.Amount != "12.34" || updated.Currency != "EUR" || updated.PeriodDays != 90 || updated.NextPaymentDate != "2028-04-29" || updated.CreatedAt != sub.CreatedAt || !updated.UpdatedAt.After(sub.UpdatedAt) {
		t.Fatalf("incorrect edit: %+v", updated)
	}
	// Reconnect to PostgreSQL and recreate the application before reading.
	*a.handler = *app.New(testDB(t), a.config, &browserProvider{})
	a.handler.Now = func() time.Time { return a.now }
	if got := a.find(sub.ID, "active"); got == nil || got.NextPaymentDate != "2028-04-29" || got.Amount != "12.34" {
		t.Fatalf("edit lost: %+v", got)
	}
	a.now = time.Date(2028, 4, 30, 0, 0, 0, 0, time.UTC)
	a.login()
	if got := a.find(sub.ID, "active"); got == nil || got.NextPaymentDate != "2028-07-28" {
		t.Fatalf("new period not anchored: %+v", got)
	}
}

func TestEditErrorsAreAtomicAndMutationsRequireOwnerAndCSRF(t *testing.T) {
	a := newSubscriptionAPI(t)
	sub := a.create()
	path := "/api/subscriptions/" + sub.ID
	before := *a.find(sub.ID, "active")
	for _, body := range []string{
		`{"name":"Changed","amount":"0"}`, `{"amount":"1.001"}`, `{"currency":"XXX"}`, `{"periodDays":31}`,
		`{"nextPaymentDate":"2028-02-28"}`, `{"nextPaymentDate":"2028-02-30"}`, `{"name":" "}`, `{"amount":12}`, `{"name":null}`,
		`{"archived":true}`, `{"id":"other"}`, `{"unknown":"value"}`, `{}`, `null`,
	} {
		a.request("PATCH", path, body, 422)
		if got := a.find(sub.ID, "active"); got == nil || *got != before {
			t.Fatalf("invalid edit changed record: %+v", got)
		}
	}
	a.request("PATCH", path, `{`, 400)
	a.request("PATCH", path, `{} {}`, 400)
	for _, operation := range []struct{ method, suffix, body string }{
		{"PATCH", "", `{"name":"Changed"}`}, {"POST", "/archive", `{}`}, {"POST", "/restore", `{"nextPaymentDate":"2028-02-29"}`},
	} {
		csrf := a.csrf
		a.csrf = ""
		a.request(operation.method, path+operation.suffix, operation.body, 403)
		a.csrf = csrf
		savedClient := a.client
		a.client = &http.Client{}
		a.request(operation.method, path+operation.suffix, operation.body, 401)
		a.client = savedClient
		a.request(operation.method, "/api/subscriptions/missing"+operation.suffix, operation.body, 404)
	}
	updated := a.decode(a.request("PATCH", path, `{"name":"New name"}`, 200))
	if updated.NextPaymentDate != "2028-02-29" {
		t.Fatal("name edit moved date")
	}
	updated = a.decode(a.request("PATCH", path, `{"nextPaymentDate":"2028-03-02"}`, 200))
	if updated.NextPaymentDate != "2028-03-02" {
		t.Fatal("explicit date not saved")
	}
}

func TestArchiveAndRestorePersistAndExcludeFromSharedActiveRead(t *testing.T) {
	a := newSubscriptionAPI(t)
	sub := a.create()
	path := "/api/subscriptions/" + sub.ID
	a.request("POST", path+"/restore", `{"nextPaymentDate":"2028-02-29"}`, 409)
	archived := a.decode(a.request("POST", path+"/archive", `{}`, 200))
	if !archived.Archived || a.find(sub.ID, "active") != nil || a.find(sub.ID, "archived") == nil {
		t.Fatal("archive did not move subscription")
	}
	a.now = time.Date(2028, 3, 5, 0, 0, 0, 0, time.UTC)
	// A repeated archive must preserve both the date and modification timestamp.
	repeated := a.decode(a.request("POST", path+"/archive", `{}`, 200))
	if archived != repeated {
		t.Fatalf("repeated archive mutated record: %+v", repeated)
	}
	for _, body := range []string{`{}`, `{"nextPaymentDate":"2028-03-04"}`, `{"nextPaymentDate":null}`, `{"nextPaymentDate":"2028-03-05","archived":false}`} {
		a.request("POST", path+"/restore", body, 422)
		if got := a.find(sub.ID, "archived"); got == nil || *got != archived {
			t.Fatalf("invalid restore changed archive: %+v", got)
		}
	}
	// Editing the archive does not advance its stored date or reactivate it.
	edited := a.decode(a.request("PATCH", path, `{"name":"Archived Internet"}`, 200))
	if !edited.Archived || edited.NextPaymentDate != "2028-02-29" {
		t.Fatalf("archived edit changed schedule: %+v", edited)
	}
	active, err := a.handler.ListSubscriptions(context.Background(), false, a.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range active {
		if item.ID == sub.ID {
			t.Fatal("archive leaked into shared forecast/reminder input")
		}
	}
	restored := a.decode(a.request("POST", path+"/restore", `{"nextPaymentDate":"2028-03-05"}`, 200))
	if restored.Archived || restored.NextPaymentDate != "2028-03-05" || restored.Name != "Archived Internet" {
		t.Fatalf("invalid restoration: %+v", restored)
	}
	*a.handler = *app.New(testDB(t), a.config, &browserProvider{})
	a.handler.Now = func() time.Time { return a.now }
	if got := a.find(sub.ID, "active"); got == nil || *got != restored || a.find(sub.ID, "archived") != nil {
		t.Fatalf("restoration lost: %+v", got)
	}
	a.request("POST", path+"/restore", `{"nextPaymentDate":"2028-03-05"}`, 409)
}
