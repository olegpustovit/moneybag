package handlers

import (
	"net/http"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func (h *Handler) apiDashboardValidate(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if _, err := h.services.Dashboard.Get(r.Context(), p, r.PathValue("id"), "draft"); err != nil {
		respond(w, http.StatusNotFound, map[string]string{"error": "dashboard unavailable"})
		return
	}
	var in struct {
		Spec dashboard.Spec `json:"spec"`
	}
	if err := decode(r, &in); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid dashboard specification: " + err.Error()})
		return
	}
	result, err := h.services.Dashboard.ValidateSpec(r.Context(), p, in.Spec)
	if err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	respond(w, http.StatusOK, result)
}

func (h *Handler) apiExpressionCatalog(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.session(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	catalog, err := h.services.Dashboard.Catalog(r.Context(), p, h.services.Tables)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load expression catalog"})
		return
	}
	respond(w, http.StatusOK, catalog)
}

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

func (h *Handler) apiImportCount(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.session(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	count, err := h.services.Imports.StagedCount(r.Context(), p)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load import count"})
		return
	}
	respond(w, http.StatusOK, map[string]int64{"count": count})
}

func (h *Handler) apiReconcileCount(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.session(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	count, err := h.services.Reconcile.OpenCount(r.Context(), p)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load discrepancy count"})
		return
	}
	respond(w, http.StatusOK, map[string]int64{"count": count})
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
	suspended, err := h.oauth.Suspended(r.Context())
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load agent status"})
		return
	}
	respond(w, http.StatusOK, map[string]any{"feeds": feeds, "agents": agents, "agents_suspended": suspended, "is_admin": p.Admin})
}

func (h *Handler) apiActivityAgents(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.session(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	agents, err := h.oauth.AgentNames(r.Context(), p)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load activity agents"})
		return
	}
	respond(w, http.StatusOK, map[string]any{"agents": agents})
}
