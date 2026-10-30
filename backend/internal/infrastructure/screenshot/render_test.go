package screenshot

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

// Proves the flags against a real browser: the page fetches, waits on a timer, then reports before the screenshot.
func TestCaptureWaitsForPageReport(t *testing.T) {
	binary := os.Getenv("MONEYBAG_CHROMIUM_BIN")
	if binary == "" {
		t.Skip("set MONEYBAG_CHROMIUM_BIN")
	}
	var reports atomic.Int64
	var width atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data":
			_, _ = w.Write([]byte(`{}`))
		case "/report":
			reports.Add(1)
			if r.URL.Query().Get("w") == "640" {
				width.Store(640)
			}
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><body>synthetic<script>fetch('/data').then(r=>r.json()).then(()=>setTimeout(()=>fetch('/report?w='+innerWidth,{method:'POST'}),3000))</script>`))
		}
	}))
	defer srv.Close()
	png, err := New(binary).Capture(context.Background(), srv.URL+"/page", 640, 480)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatal("not a PNG")
	}
	if reports.Load() != 1 || width.Load() != 640 {
		t.Fatalf("reports %d, width %d", reports.Load(), width.Load())
	}
}
