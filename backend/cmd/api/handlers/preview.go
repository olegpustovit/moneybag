package handlers

import (
	"net/http"
	"strings"
)

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	p, _, e := h.session(r)
	if raw := r.Header.Get("Authorization"); strings.HasPrefix(raw, "Bearer ") {
		p, e = h.oauth.Bearer(r.Context(), strings.TrimPrefix(raw, "Bearer "), h.cfg.PublicURL+"/mcp")
		if e != nil {
			p, e = h.oauth.Bearer(r.Context(), strings.TrimPrefix(raw, "Bearer "), h.cfg.PublicURL+"/mcp/readonly")
		}
	}
	if e != nil {
		http.Error(w, "sign in to view this preview", 401)
		return
	}
	file := r.PathValue("file")
	png := strings.HasSuffix(file, ".png")
	id := strings.TrimSuffix(file, ".html")
	if png {
		id = strings.TrimSuffix(file, ".png")
	}
	if id == file || h.services.Previews == nil {
		http.NotFound(w, r)
		return
	}
	b, e := h.services.Previews.Read(r.Context(), p, id, png)
	if e != nil {
		http.Error(w, "preview unavailable or expired; request a fresh preview", 404)
		return
	}
	if png {
		w.Header().Set("Content-Type", "image/png")
	} else {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	_, _ = w.Write(b)
}
