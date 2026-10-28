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
