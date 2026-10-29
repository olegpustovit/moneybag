package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
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
		out["legacy_html"] = rendered.HTML
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
