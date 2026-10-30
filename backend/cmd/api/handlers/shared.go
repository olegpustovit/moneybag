package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/history"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func (h *Handler) apiShared(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.session(r)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	view, err := h.services.Sharing.View(r.Context(), p, r.PathValue("id"))
	if err != nil {
		respond(w, http.StatusNotFound, map[string]string{"error": "shared dashboard unavailable"})
		return
	}
	result, err := sharedOutput(view)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not render shared dashboard"})
		return
	}
	respond(w, http.StatusOK, result)
}

// sharedOutput exposes only what the existing rendered dashboard reveals to viewers.
func sharedOutput(view sharing.SharedView) (map[string]any, error) {
	rendered := view.Snapshot.Rendered
	pages := append([]dashboard.Page(nil), rendered.Pages...)
	for i := range pages {
		pages[i].ID = fmt.Sprintf("page-%d", i+1)
	}
	out := map[string]any{"title": view.Snapshot.Title, "version": view.Snapshot.Version, "role": view.Grant.Role, "mode": view.Grant.Mode, "at": view.Snapshot.At, "pages": pages}
	if len(rendered.Pages) == 0 {
		out["legacy_html"] = view.LegacyHTML
		return out, nil
	}
	raw, err := json.Marshal(rendered.Values)
	if err != nil {
		return nil, err
	}
	var values map[string]map[string]any
	if err = json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, page := range rendered.Pages {
		for _, row := range page.Layout {
			for _, col := range row.Cols {
				allowed[col.Component] = true
			}
		}
	}
	visible := map[string]any{}
	for id, value := range values {
		if !allowed[id] {
			continue
		}
		var result map[string]any
		switch value["type"] {
		case "kpi":
			result = fields(value, "type", "title", "text", "tone", "sub")
			result["value"] = nil
		case "text":
			result = fields(value, "type", "title", "html")
		case "table", "scenario_compare":
			result = fields(value, "type", "title")
			result["columns"] = projectList(value["columns"], "key", "label", "format", "align")
			rows := []any{}
			for _, item := range list(value["rows"]) {
				row := map[string]any{}
				for key, cell := range object(item) {
					c := fields(object(cell), "t", "tone")
					c["v"] = nil
					row[key] = c
				}
				rows = append(rows, row)
			}
			result["rows"] = rows
		case "chart":
			result = fields(value, "type", "title", "mark", "y_format")
			result["x"] = fields(object(value["x"]), "kind")
			result["x"].(map[string]any)["field"] = ""
			result["series"] = projectList(value["series"], "name", "color_index")
			result["points"] = projectList(value["points"], "x", "y", "series")
			if value["rule"] != nil {
				result["rule"] = fields(object(value["rule"]), "y", "label")
			}
		case "budget":
			result = fields(value, "type", "title")
			rows := []any{}
			max := 1.0
			for _, item := range list(value["rows"]) {
				row := object(item)
				actual, _ := row["actual"].(float64)
				limit, _ := row["limit"].(float64)
				max = math.Max(max, math.Max(actual, limit))
			}
			for _, item := range list(value["rows"]) {
				row := object(item)
				projected := fields(row, "label", "status", "actual_text", "limit_text")
				actual, _ := row["actual"].(float64)
				limit, _ := row["limit"].(float64)
				projected["actual"] = math.Round(actual/max*100000) / 100000
				projected["limit"] = math.Round(limit/max*100000) / 100000
				rows = append(rows, projected)
			}
			result["rows"] = rows
		default:
			continue
		}
		visible[id] = result
	}
	out["values"] = visible
	issues := map[string]string{}
	for _, issue := range rendered.Validation.Errors {
		if allowed[issue.ComponentID] {
			issues[issue.ComponentID] = "This component could not be displayed."
		}
	}
	if len(issues) > 0 {
		out["issues"] = issues
	}
	return out, nil
}
func fields(source map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, key := range keys {
		if value, ok := source[key]; ok {
			out[key] = value
		}
	}
	return out
}
func object(value any) map[string]any { result, _ := value.(map[string]any); return result }
func list(value any) []any            { result, _ := value.([]any); return result }
func projectList(value any, keys ...string) []any {
	out := []any{}
	for _, item := range list(value) {
		out = append(out, fields(object(item), keys...))
	}
	return out
}

func (h *Handler) sharedEditor(w http.ResponseWriter, r *http.Request) (sharing.Access, bool) {
	p, err := principal.From(r.Context())
	if err != nil {
		p, _, err = h.session(r)
	}
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return sharing.Access{}, false
	}
	access, err := h.services.Sharing.EditorAccess(r.Context(), p, r.PathValue("id"))
	if err != nil {
		respond(w, http.StatusNotFound, map[string]string{"error": "shared dashboard unavailable"})
		return sharing.Access{}, false
	}
	return access, true
}
func (h *Handler) recheckSharedEditor(w http.ResponseWriter, r *http.Request, access sharing.Access) bool {
	p, err := principal.From(r.Context())
	if err != nil {
		p, _, err = h.session(r)
	}
	if err == nil {
		err = h.services.Sharing.RecordEditorRead(r.Context(), p, access)
	}
	if err != nil {
		respond(w, http.StatusNotFound, map[string]string{"error": "shared dashboard unavailable"})
		return false
	}
	return true
}
func (h *Handler) apiSharedDraft(w http.ResponseWriter, r *http.Request) {
	access, ok := h.sharedEditor(w, r)
	if !ok {
		return
	}
	result, err := access.Dashboard.Get(r.Context(), access.Principal, access.ID, "draft")
	if err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.recheckSharedEditor(w, r, access) {
		return
	}
	respond(w, http.StatusOK, result)
}

func (h *Handler) sharedWriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, principal.ErrNotFound) || errors.Is(err, principal.ErrForbidden) {
		respond(w, http.StatusNotFound, map[string]string{"error": "shared dashboard unavailable"})
		return
	}
	respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}
func (h *Handler) apiSharedPatch(w http.ResponseWriter, r *http.Request) {
	access, ok := h.sharedEditor(w, r)
	if !ok {
		return
	}
	var in struct {
		Ops json.RawMessage `json:"ops"`
	}
	if err := decode(r, &in); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid patch"})
		return
	}
	p, _ := principal.From(r.Context())
	var result dashboard.EditResult
	err := h.services.Sharing.EditorWrite(r.Context(), p, access, func(tx context.Context, current sharing.Access) error {
		var err error
		result, err = current.Dashboard.Patch(tx, current.Principal, current.ID, in.Ops)
		return err
	})
	if err != nil {
		h.sharedWriteError(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}
func (h *Handler) apiSharedPublish(w http.ResponseWriter, r *http.Request) {
	access, ok := h.sharedEditor(w, r)
	if !ok {
		return
	}
	var in struct {
		Version int    `json:"version"`
		Note    string `json:"note"`
	}
	if err := decode(r, &in); err != nil || in.Version < 1 {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid version"})
		return
	}
	p, _ := principal.From(r.Context())
	var result dashboard.PublishResult
	err := h.services.Sharing.EditorWrite(r.Context(), p, access, func(tx context.Context, current sharing.Access) error {
		var err error
		result, err = current.Dashboard.PublishDraft(tx, current.Principal, current.ID, in.Version, in.Note)
		return err
	})
	if err != nil {
		h.sharedWriteError(w, err)
		return
	}
	if err = h.services.Sharing.RefreshDashboard(r.Context(), access.Principal, access.ID); err != nil {
		result.Warnings = append(result.Warnings, "Published successfully; some shared snapshots could not refresh. Maintenance will retry.")
	}
	result.URL = "/shared/" + access.Grant.ID
	respond(w, http.StatusOK, result)
}
func (h *Handler) apiSharedUndo(w http.ResponseWriter, r *http.Request) {
	access, ok := h.sharedEditor(w, r)
	if !ok {
		return
	}
	var in history.UndoInput
	if err := decode(r, &in); err != nil || in.EventID == "" || strings.TrimSpace(in.Reason) == "" {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid undo"})
		return
	}
	p, _ := principal.From(r.Context())
	result, err := h.services.History.UndoShared(r.Context(), p, access, in)
	if err != nil {
		h.sharedWriteError(w, err)
		return
	}
	respond(w, http.StatusOK, result)
}

func (h *Handler) apiSharedEvaluate(w http.ResponseWriter, r *http.Request) {
	access, ok := h.sharedEditor(w, r)
	if !ok {
		return
	}
	var in struct {
		Expr   string                     `json:"expr"`
		Data   map[string]dashboard.Query `json:"data"`
		Params map[string]any             `json:"params"`
	}
	if err := decode(r, &in); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid expression request"})
		return
	}
	result, err := access.Dashboard.Evaluate(r.Context(), access.Principal, in.Expr, in.Data, in.Params)
	if err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.recheckSharedEditor(w, r, access) {
		return
	}
	respond(w, http.StatusOK, result)
}

func (h *Handler) apiSharedValidate(w http.ResponseWriter, r *http.Request) {
	access, ok := h.sharedEditor(w, r)
	if !ok {
		return
	}
	var in struct {
		Spec dashboard.Spec `json:"spec"`
	}
	if err := decode(r, &in); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid dashboard specification: " + err.Error()})
		return
	}
	result, err := access.Dashboard.ValidateSpec(r.Context(), access.Principal, in.Spec)
	if !h.recheckSharedEditor(w, r, access) {
		return
	}
	if err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	respond(w, http.StatusOK, result)
}

func (h *Handler) apiSharedCatalog(w http.ResponseWriter, r *http.Request) {
	access, ok := h.sharedEditor(w, r)
	if !ok {
		return
	}
	catalog, err := access.Dashboard.Catalog(r.Context(), access.Principal, h.services.Tables)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load formula catalog"})
		return
	}
	sources := catalog.Sources[:0]
	for _, source := range catalog.Sources {
		if slices.Contains(access.Grant.Sources, source.Name) {
			sources = append(sources, source)
		}
	}
	catalog.Sources = sources
	if !h.recheckSharedEditor(w, r, access) {
		return
	}
	respond(w, http.StatusOK, catalog)
}
