package handlers

import "net/http"

func (h *Handler) apiSession(w http.ResponseWriter, r *http.Request) {
	p, session, err := h.session(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	user, err := h.services.Identity.User(r.Context(), p.UserID)
	if err != nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": "account unavailable"})
		return
	}
	respond(w, http.StatusOK, map[string]any{"user": user, "csrf": session.CSRF, "version": h.cfg.Version, "mcp_url": "/api/mcp"})
}

func (h *Handler) apiImports(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.session(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	batches, err := h.services.Imports.List(r.Context(), p)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load imports"})
		return
	}
	respond(w, http.StatusOK, map[string]any{"batches": batches})
}

func (h *Handler) apiSettings(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.session(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	feeds, err := h.services.Imports.Feeds(r.Context(), p)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load feeds"})
		return
	}
	agents, err := h.oauth.Grants(r.Context(), p)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load connections"})
		return
	}
	respond(w, http.StatusOK, map[string]any{"feeds": feeds, "agents": agents})
}
