package app_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"tracking/internal/app"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func encoded(v any) string                                            { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }

// The fake is at the external HTTP boundary. Real token validation, routes,
// migrations and PostgreSQL remain in use.
type telegramFixture struct {
	key                    *rsa.PrivateKey
	mode, nonce, challenge string
	now                    time.Time
	t                      *testing.T
}

func (f *telegramFixture) transport(r *http.Request) (*http.Response, error) {
	var value any
	if strings.HasSuffix(r.URL.Path, "jwks.json") {
		value = map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "test", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes())}}}
	} else {
		r.ParseForm()
		id, secret, ok := r.BasicAuth()
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || id != "client" || secret != "secret" || base64.RawURLEncoding.EncodeToString(digest[:]) != f.challenge || r.Form.Get("grant_type") != "authorization_code" {
			f.t.Error("invalid code exchange / PKCE")
		}
		claims := map[string]any{"iss": "https://oauth.telegram.org", "aud": "client", "sub": "unrelated-subject", "id": 42, "nonce": f.nonce, "iat": f.now.Unix(), "exp": f.now.Add(time.Hour).Unix()}
		switch f.mode {
		case "foreign":
			claims["id"] = 99
		case "expired":
			claims["exp"] = f.now.Add(-time.Second).Unix()
		case "nonce":
			claims["nonce"] = "other"
		case "issuer":
			claims["iss"] = "https://attacker.invalid"
		case "audience":
			claims["aud"] = "other"
		case "missing-id":
			delete(claims, "id")
		case "missing-sub":
			delete(claims, "sub")
		case "missing-iat":
			delete(claims, "iat")
		case "future":
			claims["iat"] = f.now.Add(time.Hour).Unix()
		}
		body := encoded(map[string]string{"alg": "RS256", "kid": "test"}) + "." + encoded(claims)
		sum := sha256.Sum256([]byte(body))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
		if f.mode == "signature" {
			sig[0] ^= 1
		}
		value = map[string]string{"id_token": body + "." + base64.RawURLEncoding.EncodeToString(sig)}
	}
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Header: make(http.Header)}, nil
}
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires TEST_DATABASE_URL with a real PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = app.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestOwnerLoginSessionAndLogout(t *testing.T) {
	db := testDB(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &telegramFixture{key: key, now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), t: t}
	cfg := app.Config{OwnerID: 42, ClientID: "client", ClientSecret: "secret"}
	provider := app.NewTelegram(cfg)
	provider.Client = &http.Client{Transport: roundTrip(fixture.transport)}
	handler := app.New(db, cfg, provider)
	handler.Now = func() time.Time { return fixture.now }
	var active atomic.Pointer[app.Server]
	active.Store(handler)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { active.Load().ServeHTTP(w, r) }))
	defer server.Close()
	cfg.Origin = server.URL
	// The public origin is fixed before any request.
	handler = app.New(db, cfg, provider)
	handler.Now = func() time.Time { return fixture.now }
	active.Store(handler)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, csrf, origin string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, nil)
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Origin", origin)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	begin := func() string {
		t.Helper()
		res := request("GET", "/api/auth/telegram/start", "", "")
		if res.StatusCode != 302 {
			t.Fatalf("start: %d", res.StatusCode)
		}
		u, _ := url.Parse(res.Header.Get("Location"))
		fixture.nonce = u.Query().Get("nonce")
		fixture.challenge = u.Query().Get("code_challenge")
		if u.Query().Get("code_challenge_method") != "S256" {
			t.Fatal("PKCE not S256")
		}
		return "/api/auth/telegram/callback?code=code&state=" + url.QueryEscape(u.Query().Get("state"))
	}
	status := func(res *http.Response, want int) {
		t.Helper()
		if res.StatusCode != want {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("want %d got %d: %s", want, res.StatusCode, b)
		}
	}
	status(request("GET", "/api/session?id=42", "", ""), 401)
	path := begin()
	status(request("GET", path+"bad", "", ""), 403)
	loginResponse := request("GET", path, "", "")
 status(loginResponse,303)
 var savedCookie *http.Cookie
 for _,cookie := range loginResponse.Cookies() {
  if cookie.Name=="session" { savedCookie=cookie; if !cookie.HttpOnly || cookie.SameSite!=http.SameSiteLaxMode || cookie.MaxAge!=604800 { t.Fatal("unsafe session cookie") } }
 }
 if savedCookie==nil { t.Fatal("no session cookie") }
 res := request("GET", "/api/session", "", "")
	status(res, 200)
	var session struct {
		CSRF string `json:"csrfToken"`
	}
	json.NewDecoder(res.Body).Decode(&session)
	if session.CSRF == "" {
		t.Fatal("missing CSRF")
	}
	status(request("GET", path, "", ""), 403) // callback cannot be replayed
	status(request("POST", "/api/logout", "", server.URL), 403)
	status(request("POST", "/api/logout", session.CSRF, "https://attacker.invalid"), 403)
	status(request("GET", "/api/session", "", ""), 200)
	// A new application instance and database connection retain the session.
	secondDB := testDB(t)
	second := app.New(secondDB, cfg, provider)
	second.Now = handler.Now
	active.Store(second)
	status(request("GET", "/api/session", "", ""), 200)
	status(request("POST", "/api/logout", session.CSRF, server.URL), 204)
	status(request("GET", "/api/session", "", ""), 401)
 originURL,_:=url.Parse(server.URL)
 jar.SetCookies(originURL,[]*http.Cookie{savedCookie})
 status(request("GET", "/api/session", "", ""), 401)
 jar.SetCookies(originURL,[]*http.Cookie{{Name:"session",Value:"",Path:"/",MaxAge:-1}})
 for _, mode := range []string{"foreign", "expired", "nonce", "issuer", "audience", "missing-id", "missing-sub", "missing-iat", "future", "signature"} {
		t.Run(mode, func(t *testing.T) {
			fixture.mode = mode
			path := begin()
			res := request("GET", path, "", "")
			status(res, 303)
			if res.Header.Get("Location") != "/?login=denied" {
				t.Fatal("identity accepted")
			}
			status(request("GET", "/api/session", "", ""), 401)
		})
	}
	fixture.mode = ""
	path = begin()
	fixture.now = fixture.now.Add(11 * time.Minute)
	status(request("GET", path, "", ""), 403)
	path = begin()
	status(request("GET", path, "", ""), 303)
	fixture.now = fixture.now.Add(7 * 24 * time.Hour)
	status(request("GET", "/api/session", "", ""), 401)
	status(request("GET", "/health/ready", "", ""), 200)
 secondDB.Close()
 status(request("GET", "/health/ready", "", ""), 503)
 status(request("GET", "/health/live", "", ""), 200)
}

func TestBrowserFixture(t *testing.T) {
	if os.Getenv("BROWSER_FIXTURE") != "1" {
		t.Skip("browser fixture only")
	}
	db := testDB(t)
	cfg := app.Config{Origin: "http://127.0.0.1:18080", OwnerID: 42, StaticDir: "../../../frontend/dist"}
	p := &browserProvider{}
	handler := app.New(db, cfg, p)
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.HandleFunc("/test-login", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		fmt.Fprintf(w, `<a href="/api/auth/telegram/callback?state=%s&code=owner">Владелец</a> <a href="/api/auth/telegram/callback?state=%s&code=foreign">Посторонний</a>`, url.QueryEscape(state), url.QueryEscape(state))
	})
	if err := http.ListenAndServe("127.0.0.1:18080", mux); err != nil {
		t.Fatal(err)
	}
}

type browserProvider struct{}

func (*browserProvider) AuthorizationURL(state, nonce, challenge string) string {
	return "/test-login?state=" + url.QueryEscape(state)
}
func (*browserProvider) Exchange(ctx context.Context, code, verifier, nonce string, now time.Time) (int64, error) {
	if code == "owner" {
		return 42, nil
	}
	return 99, nil
}
