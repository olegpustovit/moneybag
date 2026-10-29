package handlers

import (
	"bufio"
	"compress/gzip"
	"context"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestCompressionAndLegacyCacheHeaders(t *testing.T) {
	wrap, err := Compression()
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{templates: template.Must(template.ParseFS(webFS, "web/*.html"))}
	app := wrap(h.Handler())
	for _, tc := range []struct {
		method, path, cache string
		status              int
	}{
		{"GET", "/assets/app.js", "no-cache", 200},
		{"GET", "/assets/style.css", "no-cache", 200},
		{"GET", "/assets/missing.js", "no-store", 404},
		{"GET", "/login", "no-store", 200},
		{"POST", "/api/mcp", "no-store", 401},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != tc.status || rec.Header().Get("Cache-Control") != tc.cache {
			t.Fatalf("%s: status %d cache %q", tc.path, rec.Code, rec.Header().Get("Cache-Control"))
		}
		if tc.path == "/assets/app.js" {
			if rec.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
				t.Fatalf("asset compression: %v", rec.Header())
			}
			z, err := gzip.NewReader(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(z)
			if err != nil {
				t.Fatal(err)
			}
			_ = z.Close()
			original, err := webFS.ReadFile("web/app.js")
			if err != nil || string(body) != string(original) {
				t.Fatal("compressed asset changed", err)
			}
		}
	}
	payload := strings.Repeat(`{"synthetic":"entry"}`, 200)
	jsonHandler := wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	for _, tc := range []struct{ accept, encoding string }{{"gzip", "gzip"}, {"gzip;q=0", ""}, {"br", ""}} {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.Header.Set("Accept-Encoding", tc.accept)
		rec := httptest.NewRecorder()
		jsonHandler.ServeHTTP(rec, req)
		if rec.Header().Get("Content-Encoding") != tc.encoding {
			t.Fatalf("%q: %v", tc.accept, rec.Header())
		}
		if tc.encoding == "gzip" && rec.Body.Len() >= 1500 {
			t.Fatalf("JSON did not compress: %d bytes", rec.Body.Len())
		}
	}
}

func TestEventStreamFlushesWithoutCompression(t *testing.T) {
	wrap, err := Compression()
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	srv := httptest.NewServer(wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
	})))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	close(release)
	_ = resp.Body.Close()
	if err != nil || line != "data: first\n" || resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("stream: %q, %v, %v", line, err, resp.Header)
	}
}

func TestEventStreamFlushesThroughFiber(t *testing.T) {
	wrap, err := Compression()
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	app := fiber.New()
	app.All("/*", wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
	})))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- app.Listener(listener, fiber.ListenConfig{DisableStartupMessage: true}) }()
	defer func() { close(release); _ = app.Shutdown(); <-done }()
	for _, tc := range []struct{ method, path string }{{"GET", "/mcp"}, {"POST", "/api/mcp"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, err := http.NewRequestWithContext(ctx, tc.method, "http://"+listener.Addr().String()+tc.path, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		_ = resp.Body.Close()
		cancel()
		if err != nil || line != "data: first\n" || resp.Header.Get("Content-Encoding") != "" {
			t.Fatalf("%s fiber stream: %q, %v, %v", tc.path, line, err, resp.Header)
		}
	}
}
