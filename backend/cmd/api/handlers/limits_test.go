package handlers

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthenticationBoundaries(t *testing.T) {
	var l authLimiter
	for i := 0; i < 60; i++ {
		if !l.allow("127.0.0.1:80") {
			t.Fatal("rate limited too soon")
		}
	}
	if l.allow("127.0.0.1:81") {
		t.Fatal("source port bypassed limit")
	}
	l.window = time.Now().Add(-2 * time.Minute)
	if !l.allow("127.0.0.1:80") {
		t.Fatal("window did not reset")
	}
	h := Handler{cfg: Config{PublicURL: "https://moneybag.example"}}
	for _, origin := range []string{"null", "https://evil.example", "https://moneybag.example.evil.test"} {
		r := httptest.NewRequest("POST", "/login", nil)
		r.Header.Set("Origin", origin)
		if h.authOrigin(httptest.NewRecorder(), r) {
			t.Fatalf("accepted %s", origin)
		}
	}
	r := httptest.NewRequest("POST", "/login", nil)
	r.Header.Set("Origin", h.cfg.PublicURL)
	if !h.authOrigin(httptest.NewRecorder(), r) {
		t.Fatal("own origin rejected")
	}
}

func TestIdentitySDKRoutesShareTheExistingAuthenticationBudget(t *testing.T) {
	h := &Handler{cfg: Config{IdentityMode: true}}
	for i := 0; i < 60; i++ {
		path := "/api/auth/id/login"
		if i%2 == 1 {
			path = "/api/auth/id/callback"
		}
		request := httptest.NewRequest("GET", path, nil)
		request.RemoteAddr = "127.0.0.1:1234"
		if h.limited(httptest.NewRecorder(), request) {
			t.Fatal("SDK request limited before existing budget")
		}
	}
	request := httptest.NewRequest("GET", "/api/auth/id/callback", nil)
	request.RemoteAddr = "127.0.0.1:5678"
	out := httptest.NewRecorder()
	if !h.limited(out, request) || out.Code != 429 || out.Header().Get("Retry-After") != "60" {
		t.Fatal("SDK callback bypassed authentication budget")
	}
	logout := httptest.NewRequest("POST", "/api/auth/id/logout", nil)
	logout.RemoteAddr = request.RemoteAddr
	if h.limited(httptest.NewRecorder(), logout) {
		t.Fatal("authentication attempts prevented app signout")
	}
}
