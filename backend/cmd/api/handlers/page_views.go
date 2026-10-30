package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/preview"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
)

// writeEncoded answers from cached bytes: 304 on a matching ETag, gzip when accepted.
func writeEncoded(w http.ResponseWriter, r *http.Request, body dashboard.Encoded) {
	w.Header().Set("ETag", body.ETag)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Add("Vary", "Accept-Encoding")
	if etagMatches(r.Header.Get("If-None-Match"), body.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if encodingQuality(r.Header.Get("Accept-Encoding"), "gzip") > 0 {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(body.Gzip)))
		_, _ = w.Write(body.Gzip)
		return
	}
	raw, err := body.Raw()
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not read response"})
		return
	}
	_, _ = w.Write(raw)
}

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

// viewQuery accepts only the documented parameters, which also bounds what the view cache can hold.
func viewQuery(r *http.Request) (string, map[string]any, error) {
	query := r.URL.Query()
	version := query.Get("version")
	if version != "" && version != "draft" && version != "published" {
		if n, err := strconv.Atoi(version); err != nil || n < 1 {
			return "", nil, fmt.Errorf("version must be draft, published or a positive integer")
		}
	}
	params := map[string]any{}
	for name, layout := range map[string]string{"from": time.DateOnly, "to": time.DateOnly, "month": "2006-01"} {
		value := query.Get(name)
		if value == "" {
			continue
		}
		if _, err := time.Parse(layout, value); err != nil {
			return "", nil, fmt.Errorf("%s must look like %s", name, layout)
		}
		params[name] = value
	}
	return version, params, nil
}

func viewError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, principal.ErrNotFound), errors.Is(err, dashboard.ErrNotPublished):
		respond(w, http.StatusNotFound, map[string]string{"error": "dashboard unavailable"})
	case errors.Is(err, principal.ErrForbidden):
		respond(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	default:
		if r.Context().Err() == nil {
			slog.Error("dashboard view failed", "error", err)
		}
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load dashboard"})
	}
}

// apiDashboardView enforces what dashboard_preview does: the same access check and the same recorded shared read.
func (h *Handler) apiDashboardView(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.session(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	version, params, err := viewQuery(r)
	if err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := reqcache.With(r.Context())
	access, err := h.services.Sharing.Access(ctx, p, r.PathValue("id"), false)
	if err != nil {
		viewError(w, r, err)
		return
	}
	body, err := access.Dashboard.PageView(ctx, access.Principal, access.ID, version, params)
	if err == nil && access.Grant != nil {
		err = h.services.Sharing.RecordEditorRead(r.Context(), p, access)
	}
	if err != nil {
		viewError(w, r, err)
		return
	}
	writeEncoded(w, r, body)
}

type navItem struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Icon      string `json:"icon"`
	Dashboard string `json:"dashboard,omitempty"`
	Route     string `json:"route,omitempty"`
}

// defaultNav is the contract's navigation until nav_set stores another.
var defaultNav = []navItem{
	{ID: "now", Label: "Now", Icon: "gauge", Route: "/"},
	{ID: "spending", Label: "Spending", Icon: "receipt", Dashboard: "spending"},
	{ID: "income", Label: "Income", Icon: "banknote", Dashboard: "income"},
	{ID: "budget", Label: "Budget", Icon: "wallet", Route: "/budgets"},
	{ID: "future", Label: "Future", Icon: "trending-up", Route: "/scenarios"},
	{ID: "ledger", Label: "Ledger", Icon: "book-open", Route: "/journal"},
	{ID: "data", Label: "Data", Icon: "database", Route: "/databases"},
	{ID: "changes", Label: "Changes", Icon: "history", Route: "/activity"},
}

// bootstrapExtras reads what agents set through nav_set and label_set, and the open flags.
func (h *Handler) bootstrapExtras(ctx context.Context, p principal.Principal) (nav []navItem, labels map[string]string, feedback int64, err error) {
	stored, err := h.services.Semantic.Nav(ctx, p)
	if err != nil {
		return nil, nil, 0, err
	}
	nav = defaultNav
	if len(stored) > 0 {
		nav = make([]navItem, len(stored))
		for i, item := range stored {
			nav[i] = navItem(item)
		}
	}
	if labels, err = h.services.Semantic.Labels(ctx, p); err != nil {
		return nil, nil, 0, err
	}
	feedback, err = h.services.Feedback.OpenCount(ctx, p)
	return nav, labels, feedback, err
}

// apiBootstrap replaces session, discover and list round trips. Bytes are cached until the user's next write.
func (h *Handler) apiBootstrap(w http.ResponseWriter, r *http.Request) {
	p, session, err := h.session(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	ctx := reqcache.With(r.Context())
	body, err := h.services.Dashboard.Cached(ctx, p, "bootstrap", "", session.CSRF, func() (dashboard.Encoded, error) {
		return h.bootstrap(ctx, p, session)
	})
	if err != nil {
		if r.Context().Err() == nil {
			slog.Error("bootstrap failed", "error", err)
		}
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load the app"})
		return
	}
	writeEncoded(w, r, body)
}

func (h *Handler) bootstrap(ctx context.Context, p principal.Principal, session identity.Session) (dashboard.Encoded, error) {
	user, err := h.services.Identity.User(ctx, p.UserID)
	if err != nil {
		return dashboard.Encoded{}, err
	}
	dashboards, err := h.services.Dashboard.List(ctx, p)
	if err != nil {
		return dashboard.Encoded{}, err
	}
	tables, err := h.services.Tables.List(ctx, p)
	if err != nil {
		return dashboard.Encoded{}, err
	}
	imports, err := h.services.Imports.StagedCount(ctx, p)
	if err != nil {
		return dashboard.Encoded{}, err
	}
	reconcile, err := h.services.Reconcile.OpenCount(ctx, p)
	if err != nil {
		return dashboard.Encoded{}, err
	}
	nav, labels, feedback, err := h.bootstrapExtras(ctx, p)
	if err != nil {
		return dashboard.Encoded{}, err
	}
	return dashboard.Encode(map[string]any{
		"session":    map[string]any{"user": user, "csrf": session.CSRF, "version": h.cfg.Version, "mcp_url": "/api/mcp"},
		"nav":        nav,
		"dashboards": dashboards,
		"tables":     tables,
		"counts":     map[string]int64{"imports": imports, "reconcile": reconcile, "feedback": feedback},
		"labels":     labels,
	})
}

// renderAccess re-checks the token's principal and grant on every read, so a revoked or changed grant stops it at once.
func (h *Handler) renderAccess(w http.ResponseWriter, r *http.Request) (preview.Session, sharing.Access, bool) {
	unavailable := func() (preview.Session, sharing.Access, bool) {
		respond(w, http.StatusNotFound, map[string]string{"error": "render unavailable"})
		return preview.Session{}, sharing.Access{}, false
	}
	if h.services.Previews == nil {
		return unavailable()
	}
	session, err := h.services.Previews.Resolve(r.PathValue("token"))
	if err != nil {
		return unavailable()
	}
	access, err := h.services.Sharing.Access(r.Context(), session.Principal, session.Target.Dashboard, false)
	if err != nil {
		return unavailable()
	}
	hash := ""
	if access.Grant != nil {
		hash = sharing.PermissionHash(*access.Grant)
	}
	if hash != session.GrantHash {
		return unavailable()
	}
	return session, access, true
}

// renderShell serves the SPA without chrome or a session; the page reads only through its token.
func (h *Handler) renderShell(w http.ResponseWriter, r *http.Request) {
	if h.services.Previews == nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.services.Previews.Resolve(r.PathValue("token")); err != nil {
		http.NotFound(w, r)
		return
	}
	serveUI(w, r)
}

func (h *Handler) apiRenderView(w http.ResponseWriter, r *http.Request) {
	session, access, ok := h.renderAccess(w, r)
	if !ok {
		return
	}
	body, err := access.Dashboard.FreshPageView(r.Context(), access.Principal, access.ID, session.Target.Version, session.Target.Params)
	if err != nil {
		viewError(w, r, err)
		return
	}
	writeEncoded(w, r, body)
}

// apiRenderCube checks the token, then hands over to renderCube, the seam for the cube handler.
func (h *Handler) apiRenderCube(w http.ResponseWriter, r *http.Request) {
	session, access, ok := h.renderAccess(w, r)
	if !ok {
		return
	}
	h.renderCube(w, r, session, access)
}

// renderCube serves the owner's cube. A grant's token stays denied: the cube has no notion of a grant's source and row limits.
func (h *Handler) renderCube(w http.ResponseWriter, r *http.Request, session preview.Session, access sharing.Access) {
	if access.Grant != nil {
		respond(w, http.StatusNotFound, map[string]string{"error": "cube unavailable"})
		return
	}
	revision, e := h.services.Audit.Revision(r.Context(), session.Principal)
	if e != nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": "could not read the data revision"})
		return
	}
	cube, e := h.services.Semantic.Cube(r.Context(), session.Principal, revision)
	if e != nil {
		h.services.Logger.ErrorContext(r.Context(), "build render cube", "error", e)
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not build the cube"})
		return
	}
	respond(w, http.StatusOK, cube)
}

func (h *Handler) apiRenderReport(w http.ResponseWriter, r *http.Request) {
	if h.services.Previews == nil {
		respond(w, http.StatusNotFound, map[string]string{"error": "render unavailable"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var report preview.Report
	if err := decode(r, &report); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid report: " + err.Error()})
		return
	}
	if err := h.services.Previews.Report(r.PathValue("token"), report); err != nil {
		if errors.Is(err, principal.ErrNotFound) {
			respond(w, http.StatusNotFound, map[string]string{"error": "render unavailable"})
			return
		}
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
