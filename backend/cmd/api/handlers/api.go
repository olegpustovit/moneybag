package handlers

import (
	"net/http"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
)

func (h *Handler) apiExpressionCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Expr string `json:"expr"`
		Role string `json:"role"`
		From string `json:"from"`
	}
	if err := decode(r, &in); err != nil || len(in.Expr) > 2000 || len(in.From) > 200 {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid expression request"})
		return
	}
	switch in.Role {
	case "where", "derive", "agg", "value":
	default:
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid expression role"})
		return
	}
	respond(w, http.StatusOK, dashboard.CheckExpression(h.services.Expr, in.Expr, in.Role))
}

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
