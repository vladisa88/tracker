package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	"tracking/internal/app"
)

// All controls live exclusively in this test binary; no production bypass.
func TestBotBrowserFixture(t *testing.T) {
	if os.Getenv("BOT_BROWSER_FIXTURE") != "1" {
		t.Skip("browser fixture only")
	}
	db := testDB(t)
	var sequence int64 = time.Now().UnixMicro()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			app.New(db, app.Config{}, nil).ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("fixture-owner")
		if err != nil {
			http.SetCookie(w, &http.Cookie{Name: "fixture-owner", Value: strconv.FormatInt(atomic.AddInt64(&sequence, 1), 10), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
			http.Redirect(w, r, "/", 303)
			return
		}
		owner, err := strconv.ParseInt(cookie.Value, 10, 64)
		if err != nil {
			http.Error(w, "invalid fixture owner", 400)
			return
		}
		cfg := app.Config{Origin: "http://127.0.0.1:18081", OwnerID: owner, StaticDir: "../../../frontend/dist", BotUsername: "tracking_test_bot", BotToken: "123:test-secret"}
		switch r.URL.Path {
		case "/test-login":
			fmt.Fprintf(w, `<a href="/api/auth/telegram/callback?code=owner&state=%s">Владелец</a>`, url.QueryEscape(r.URL.Query().Get("state")))
		case "/test-bot":
			f := &botFixture{t: t, update: fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":%d},"chat":{"id":%d,"type":"private"},"text":"/start"}}`, atomic.AddInt64(&sequence, 1), owner, owner)}
			if r.URL.Query().Get("mode") == "error" {
				f.failure = 403
			}
			telegram := app.NewBot(cfg)
			telegram.Client = &http.Client{Transport: roundTrip(f.transport)}
			if err := app.NewBotWorker(db, cfg, telegram).Poll(context.Background()); err != nil {
				http.Error(w, "fixture poll failed", 500)
				return
			}
			w.WriteHeader(204)
		default:
			app.New(db, cfg, &fixedBotLogin{id: owner}).ServeHTTP(w, r)
		}
	})
	if err := http.ListenAndServe("127.0.0.1:18081", mux); err != nil {
		t.Fatal(err)
	}
}
