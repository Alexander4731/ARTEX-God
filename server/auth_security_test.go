package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestPasswordChangeRevokesOldSession(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	prior, err := m.pg.GetSettings([]string{authPassKey, authEpochKey})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, key := range []string{authPassKey, authEpochKey} {
			if value, ok := prior[key]; ok {
				m.pg.SetSetting(key, value)
			} else {
				m.pg.Exec(`DELETE FROM settings WHERE key=$1`, key)
			}
		}
	})
	hash, _ := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
	if err := m.pg.SetSettings(map[string]string{authPassKey: string(hash), authEpochKey: "old"}); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: m, jwtKey: []byte(strings.Repeat("k", 32))}
	old, _ := signJWT(s.jwtKey, "old")
	r := httptest.NewRequest("POST", "https://example.com/api/auth/change-password", strings.NewReader(`{"old_password":"old-password","new_password":"new-password"}`))
	r.Header.Set("Authorization", "Bearer "+old)
	w := httptest.NewRecorder()
	s.authChangePassword(w, r)
	if w.Code != 200 {
		t.Fatalf("change: %d %s", w.Code, w.Body.String())
	}
	if valid, err := s.validAuth(r); valid || err != nil {
		t.Fatalf("old token valid=%v err=%v", valid, err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatal("missing secure HttpOnly session cookie")
	}
	fresh := httptest.NewRequest("GET", "https://example.com/api/events", nil)
	fresh.AddCookie(cookies[0])
	if valid, err := s.validAuth(fresh); !valid || err != nil {
		t.Fatalf("new cookie valid=%v err=%v", valid, err)
	}
}

func TestAuthRejectsQueryTokenAndCrossSiteWrite(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/api/events?token=long-secret", nil)
	if extractToken(r) != "" {
		t.Fatal("URL credential accepted")
	}
	s := &Server{}
	h := s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	r = httptest.NewRequest("POST", "http://example.com/api/auth/login", nil)
	r.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-site mutation status=%d", w.Code)
	}
	for n := 0; n < 10; n++ {
		if !s.allowLogin(r) {
			t.Fatal("limit reached too early")
		}
	}
	if s.allowLogin(r) {
		t.Fatal("login attempts unbounded")
	}
}

func TestAuthOriginMatchesSchemeHostAndPort(t *testing.T) {
	for _, tc := range []struct {
		request, origin, forwarded string
		want                       bool
	}{
		{"http://127.0.0.1:8787/api/auth/login", "http://127.0.0.1:8787", "", true},
		{"http://127.0.0.1:8787/api/auth/login", "http://127.0.0.1:3000", "", false},
		{"http://localhost:8787/api/auth/login", "http://127.0.0.1:8787", "", false},
		{"https://example.com/api/auth/login", "http://example.com", "", false},
		{"http://example.com/api/auth/login", "https://example.com", "https", true},
	} {
		r := httptest.NewRequest("POST", tc.request, nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Forwarded-Proto", tc.forwarded)
		if got := allowedOrigin(r); got != tc.want {
			t.Errorf("request=%s origin=%s: allowed=%v", tc.request, tc.origin, got)
		}
	}
}
