package handlers

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type authLimiter struct {
	mu     sync.Mutex
	window time.Time
	counts map[string]int
}

func (l *authLimiter) allow(address string) bool {
	host, _, e := net.SplitHostPort(address)
	if e != nil {
		host = address
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.window) >= time.Minute {
		l.window = time.Now()
		l.counts = map[string]int{}
	}
	if l.counts == nil {
		l.counts = map[string]int{}
	}
	if l.counts[host] == 0 && len(l.counts) >= 1000 {
		return false
	}
	l.counts[host]++
	return l.counts[host] <= 60
}
func (h *Handler) limited(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet && h.cfg.IdentityMode {
		if r.URL.Path != "/api/auth/id/login" && r.URL.Path != "/api/auth/id/callback" {
			return false
		}
	} else if r.Method == http.MethodPost {
		switch r.URL.Path {
		case "/login", "/setup", "/register", "/oauth/register", "/oauth/token":
		default:
			return false
		}
	} else {
		return false
	}
	if !h.authLimit.allow(r.RemoteAddr) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too many attempts. Try again in a minute.", 429)
		return true
	}
	return false
}
