package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrSchema = errors.New("incompatible schema")

const sessionTTL = 7 * 24 * time.Hour
const loginTTL = 10 * time.Minute

type LoginProvider interface {
	AuthorizationURL(state, nonce, challenge string) string
	Exchange(context.Context, string, string, string, time.Time) (int64, error)
}

type Server struct {
	db       *sql.DB
	config   Config
	provider LoginProvider
	Now      func() time.Time
	static   http.Handler
}

func New(db *sql.DB, config Config, provider LoginProvider) *Server {
	return &Server{db: db, config: config, provider: provider, Now: time.Now, static: http.FileServer(http.Dir(config.StaticDir))}
}
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("random source unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hash(s string) string   { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func (s *Server) cookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.config.Origin, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds()), Expires: s.Now().Add(ttl)})
}
func cookieValue(r *http.Request, name string) string {
	c, e := r.Cookie(name)
	if e != nil {
		return ""
	}
	return c.Value
}
func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if r.Method == "GET" {
		switch r.URL.Path {
		case "/health/live":
			w.WriteHeader(200)
			return
		case "/health/ready":
			var version int
			if s.db == nil || s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version) != nil || version != schemaVersion {
				fail(w, 503, "not_ready", "Приложение пока не готово")
				return
			}
			w.WriteHeader(200)
			return
		case "/api/auth/telegram/start":
			s.start(w, r)
			return
		case "/api/auth/telegram/callback":
			s.callback(w, r)
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		var owner int64
		var csrf string
		token := cookieValue(r, "session")
		if token == "" {
			fail(w, 401, "unauthorized", "Войдите через Telegram")
			return
		}
		err := s.db.QueryRowContext(ctx, `SELECT owner_id, csrf_token FROM owner_sessions WHERE token_hash=$1 AND expires_at>$2`, hash(token), s.Now()).Scan(&owner, &csrf)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != s.config.OwnerID) {
			fail(w, 401, "unauthorized", "Сессия завершена. Войдите снова")
			return
		}
		if err != nil {
			fail(w, 503, "unavailable", "Сервис временно недоступен")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && (r.Header.Get("Origin") != s.config.Origin || !equal(r.Header.Get("X-CSRF-Token"), csrf)) {
			fail(w, 403, "csrf", "Обновите страницу и повторите действие")
			return
		}
		switch {
		case r.URL.Path == "/api/meta" && r.Method == "GET":
			s.metadata(w)
			return
		case r.URL.Path == "/api/subscriptions" && (r.Method == "GET" || r.Method == "POST"):
			s.subscriptions(w, r)
			return
		case r.Method == "GET" && r.URL.Path == "/api/session":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			json.NewEncoder(w).Encode(map[string]any{"owner": map[string]string{"telegramId": strconvID(owner)}, "csrfToken": csrf})
			return
		case r.Method == "POST" && r.URL.Path == "/api/logout":
			if _, err := s.db.ExecContext(ctx, `DELETE FROM owner_sessions WHERE token_hash=$1`, hash(token)); err != nil {
				fail(w, 503, "unavailable", "Не удалось завершить сессию")
				return
			}
			s.cookie(w, "session", "", -time.Second)
			w.WriteHeader(204)
			return
		default:
			fail(w, 404, "not_found", "Маршрут не найден")
			return
		}
	}
	if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/assets/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	s.static.ServeHTTP(w, r)
}
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	state, browser, verifier, nonce := randomToken(), randomToken(), randomToken(), randomToken()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "unavailable", "Не удалось начать вход")
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), `DELETE FROM login_requests WHERE expires_at<=$1`, s.Now())
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM owner_sessions WHERE expires_at<=$1`, s.Now())
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO login_requests VALUES ($1,$2,$3,$4,$5)`, hash(state), hash(browser), verifier, nonce, s.Now().Add(loginTTL))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 503, "unavailable", "Не удалось начать вход")
		return
	}
	s.cookie(w, "login", browser, loginTTL)
	challenge := sha256.Sum256([]byte(verifier))
	http.Redirect(w, r, s.provider.AuthorizationURL(state, nonce, base64.RawURLEncoding.EncodeToString(challenge[:])), http.StatusFound)
}
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	var verifier, nonce string
	state, browser := r.URL.Query().Get("state"), cookieValue(r, "login")
	if state == "" || browser == "" {
		fail(w, 403, "invalid_login", "Вход не подтверждён. Вернитесь на главную и повторите вход")
		return
	}
	err := s.db.QueryRowContext(r.Context(), `DELETE FROM login_requests WHERE state_hash=$1 AND browser_hash=$2 AND expires_at>$3 RETURNING verifier,nonce`, hash(state), hash(browser), s.Now()).Scan(&verifier, &nonce)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			fail(w, 503, "unavailable", "Сервис временно недоступен")
			return
		}
		fail(w, 403, "invalid_login", "Запрос входа истёк или уже использован. Повторите вход")
		return
	}
	s.cookie(w, "login", "", -time.Second)
	code := r.URL.Query().Get("code")
	if code == "" || r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, "/?login=failed", 303)
		return
	}
	owner, err := s.provider.Exchange(r.Context(), code, verifier, nonce, s.Now())
	if err != nil || owner != s.config.OwnerID {
		http.Redirect(w, r, "/?login=denied", 303)
		return
	}
	token, csrf := randomToken(), randomToken()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "unavailable", "Не удалось сохранить сессию")
		return
	}
	defer tx.Rollback()
	// Rotate any previous browser session only after a verified owner login.
	if old := cookieValue(r, "session"); old != "" {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM owner_sessions WHERE token_hash=$1`, hash(old))
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO owner_sessions VALUES ($1,$2,$3,$4)`, hash(token), owner, csrf, s.Now().Add(sessionTTL))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 503, "unavailable", "Не удалось сохранить сессию")
		return
	}
	s.cookie(w, "session", token, sessionTTL)
	http.Redirect(w, r, "/", 303)
}

// All /api/ routes pass the guard in ServeHTTP before dispatch.
func callbackURL(origin string) string {
	u, _ := url.Parse(origin)
	u.Path = "/api/auth/telegram/callback"
	return u.String()
}
