package app_test

import (
	"net/http/httptest"
	"testing"
	"tracking/internal/app"
)

func TestAnonymousCannotReadSession(t *testing.T) {
	h := app.New(nil, app.Config{}, nil)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/api/session", nil))
	if r.Code != 401 {
		t.Fatalf("got %d: %s", r.Code, r.Body.String())
	}
}
