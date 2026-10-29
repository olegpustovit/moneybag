package handlers

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed all:dist
var uiFS embed.FS

var hashedUIAsset = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

func ValidateSPA() error {
	if _, err := uiFS.ReadFile("dist/index.html"); err != nil {
		return fmt.Errorf("SPA build is missing: %w", err)
	}
	return nil
}

func encodingQuality(header, name string) float64 {
	quality, wildcard := -1.0, -1.0
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		q := 1.0
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(strings.TrimSpace(f), "q="); ok {
				parsed, err := strconv.ParseFloat(v, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					q = 0
				} else {
					q = parsed
				}
			}
		}
		if strings.TrimSpace(fields[0]) == name {
			quality = q
		}
		if strings.TrimSpace(fields[0]) == "*" {
			wildcard = q
		}
	}
	if quality >= 0 {
		return quality
	}
	if wildcard >= 0 {
		return wildcard
	}
	return 0
}

func serveUIAsset(w http.ResponseWriter, r *http.Request) bool {
	name := "assets/" + r.PathValue("file")
	if !fs.ValidPath(name) || strings.HasSuffix(name, ".br") || strings.HasSuffix(name, ".gz") {
		return false
	}
	raw, err := uiFS.ReadFile("dist/" + name)
	if err != nil {
		return false
	}
	if hashedUIAsset.MatchString(name) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	w.Header().Add("Vary", "Accept-Encoding")
	br, gz := encodingQuality(r.Header.Get("Accept-Encoding"), "br"), encodingQuality(r.Header.Get("Accept-Encoding"), "gzip")
	candidates := []struct {
		name, ext string
		q         float64
	}{{"br", ".br", br}, {"gzip", ".gz", gz}}
	if gz > br {
		candidates[0], candidates[1] = candidates[1], candidates[0]
	}
	for _, candidate := range candidates {
		if candidate.q <= 0 {
			continue
		}
		if b, err := uiFS.ReadFile("dist/" + name + candidate.ext); err == nil {
			raw = b
			w.Header().Set("Content-Encoding", candidate.name)
			break
		}
	}
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(raw))
	return true
}

func serveUI(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/mcp") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name != "" && name != "index.html" && path.Ext(name) != "" {
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		raw, err := uiFS.ReadFile("dist/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(raw))
		return
	}
	raw, err := uiFS.ReadFile("dist/index.html")
	if err != nil {
		http.Error(w, "SPA build unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(raw))
}
