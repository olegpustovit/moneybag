package oauth

import (
	"net/http"
	"testing"
	"time"
)

func TestCIMDCacheHeaders(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		control, age string
		want         time.Duration
		noStore      bool
	}{{"max-age=600", "60", 9 * time.Minute, false}, {"no-store, max-age=3600", "", 0, true}, {"max-age=600, no-cache", "", 0, false}, {"max-age=99999", "", time.Hour, false}, {"max-age=\"60\"", "", time.Minute, false}, {"max-age=-1", "", 0, false}} {
		h := http.Header{"Cache-Control": {tc.control}, "Age": {tc.age}}
		ttl, noStore := clientCacheTTL(h, now)
		if ttl != tc.want || noStore != tc.noStore {
			t.Fatal(tc, ttl, noStore)
		}
	}
}
