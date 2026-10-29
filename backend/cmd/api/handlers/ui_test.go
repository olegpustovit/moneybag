package handlers

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUIRoutes(t *testing.T) {
	if err := ValidateSPA(); err != nil {
		t.Skip("run make ui to stage the SPA")
	}
	h := (&Handler{cfg: Config{UI: "spa"}}).Handler()
	for _, path := range []string{"/", "/dashboards/overview?version=draft"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" || !strings.Contains(rec.Body.String(), "<div id=\"root\">") {
			t.Fatalf("%s: %d %v", path, rec.Code, rec.Header())
		}
	}
	for _, path := range []string{"/assets/missing.js", "/api/nope", "/mcp/nope", "/mcp-unknown", "/oauth/nope", "/missing.txt"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 404 || strings.Contains(rec.Body.String(), "<div id=\"root\">") {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		if path == "/api/nope" && !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatal("API fallback must be JSON")
		}
	}
	entries, err := uiFS.ReadDir("dist/assets")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".js") {
			continue
		}
		found = true
		for _, accept := range []string{"br, gzip", "br;q=0, gzip;q=1", "gzip;q=0, br;q=0"} {
			req := httptest.NewRequest("GET", "/assets/"+entry.Name(), nil)
			req.Header.Set("Accept-Encoding", accept)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
				t.Fatalf("asset: %d %v", rec.Code, rec.Header())
			}
			want := "br"
			if strings.HasPrefix(accept, "br;q=0") {
				want = "gzip"
			}
			if strings.HasPrefix(accept, "gzip;q=0") {
				want = ""
			}
			if rec.Header().Get("Content-Encoding") != want {
				t.Fatalf("encoding %s: %s", accept, rec.Header().Get("Content-Encoding"))
			}
			if _, err := io.ReadAll(rec.Result().Body); err != nil {
				t.Fatal(err)
			}
		}
		break
	}
	if !found {
		t.Fatal("no hashed JS asset")
	}
}

func TestEncodingQuality(t *testing.T) {
	for _, tc := range []struct {
		header, name string
		want         float64
	}{{"br, gzip;q=0.5", "br", 1}, {"br;q=0,*;q=0.5", "br", 0}, {"br;q=garbage", "br", 0}, {"gzip;q=0.2", "gzip", 0.2}} {
		if got := encodingQuality(tc.header, tc.name); got != tc.want {
			t.Fatalf("%q %s: %v", tc.header, tc.name, got)
		}
	}
}
