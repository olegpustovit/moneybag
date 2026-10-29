package handlers

import (
	"net/http"
	"strings"

	"github.com/klauspost/compress/gzhttp"
)

// Compression leaves MCP event streams uncompressed and compresses supported response types.
func Compression() (func(http.Handler) http.Handler, error) {
	wrap, err := gzhttp.NewWrapper(gzhttp.EnableZstd(false), gzhttp.ContentTypes([]string{
		"application/json", "text/html", "text/css", "text/javascript", "image/svg+xml",
	}))
	if err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		compressed := wrap(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && (r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/")) {
				next.ServeHTTP(w, r)
				return
			}
			compressed.ServeHTTP(w, r)
		})
	}, nil
}
