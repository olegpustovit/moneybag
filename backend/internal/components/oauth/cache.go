package oauth

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

func clientCacheTTL(h http.Header, now time.Time) (time.Duration, bool) {
	ttl := 5 * time.Minute
	noStore, noCache, maxAge := false, false, false
	for _, part := range strings.Split(strings.ToLower(h.Get("Cache-Control")), ",") {
		part = strings.TrimSpace(part)
		switch part {
		case "no-store":
			noStore = true
		case "no-cache":
			noCache = true
		}
		if raw, ok := strings.CutPrefix(part, "max-age="); ok {
			if n, e := strconv.Atoi(strings.Trim(raw, "\"")); e == nil {
				ttl = time.Duration(max(0, min(n, 3600))) * time.Second
				maxAge = true
			}
		}
	}
	if !maxAge {
		if expiry, e := http.ParseTime(h.Get("Expires")); e == nil {
			ttl = expiry.Sub(now)
		}
	}
	if raw := h.Get("Age"); raw != "" {
		if age, e := strconv.Atoi(raw); e == nil && age > 0 {
			ttl -= time.Duration(min(age, 3600)) * time.Second
		}
	}
	if noStore || noCache {
		return 0, noStore
	}
	return max(0, min(ttl, time.Hour)), false
}
