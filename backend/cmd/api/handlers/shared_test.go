package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
)

func TestSharedOutputOnlyExposesPlacedRenderedValues(t *testing.T) {
	view := sharing.SharedView{Snapshot: sharing.Snapshot{Title: "Shared", Rendered: dashboard.Rendered{
		Pages:      []dashboard.Page{{ID: "p", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 6, Component: "k"}, {Span: 6, Component: "t"}, {Span: 12, Component: "b"}, {Span: 12, Component: "c"}}}}}},
		Validation: dashboard.Validation{Errors: []dashboard.Issue{{ComponentID: "b", Msg: "error-secret", Expr: "expression-secret", Hint: "hint-secret"}, {ComponentID: "hidden", Msg: "hidden-error"}}},
		Computed:   map[string]any{"secret": "computed-secret"}, Values: map[string]any{
			"hidden": map[string]any{"type": "text", "html": "hidden-secret"},
			"k":      map[string]any{"type": "kpi", "title": "K", "value": "raw-secret", "text": "$1", "tone": "", "sub": ""},
			"t":      map[string]any{"type": "table", "title": "T", "columns": []any{map[string]any{"key": "c0", "label": "Visible", "format": "", "align": "left"}}, "rows": []any{map[string]any{"c0": map[string]any{"v": "cell-secret", "t": "shown", "tone": ""}}}},
			"b":      map[string]any{"type": "budget", "rows": []any{map[string]any{"label": "B", "actual": 100.0, "limit": 200.0, "actual_text": "100", "limit_text": "200", "status": "within", "accounts": []string{"expenses:secret-account"}}}},
			"c":      map[string]any{"type": "chart", "x": map[string]any{"field": "field-secret", "kind": "category"}, "mark": "bar", "series": []any{}, "points": []any{map[string]any{"x": "X", "y": 10.0}}, "y_format": ""},
		},
	}}}
	got, err := sharedOutput(view)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"computed-secret", "hidden-secret", "raw-secret", "cell-secret", "field-secret", "error-secret", "expression-secret", "hint-secret", "hidden-error", "expenses:secret-account"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("exposed %s: %s", secret, raw)
		}
	}
	values := got["values"].(map[string]any)
	issues := got["issues"].(map[string]string)
	if len(issues) != 1 || issues["b"] != "This component could not be displayed." {
		t.Fatal(issues)
	}
	if len(values) != 4 {
		t.Fatal(values)
	}
	budget := values["b"].(map[string]any)["rows"].([]any)[0].(map[string]any)
	if budget["actual"] != 0.5 || budget["limit"] != 1.0 {
		t.Fatal(budget)
	}
	points := values["c"].(map[string]any)["points"].([]any)
	if points[0].(map[string]any)["y"] != float64(10) {
		t.Fatal(points)
	}
	legacy, err := sharedOutput(sharing.SharedView{Snapshot: sharing.Snapshot{Rendered: dashboard.Rendered{HTML: "<p>frozen</p>"}}})
	if err != nil || legacy["legacy_html"] != "<p>frozen</p>" {
		t.Fatal(legacy, err)
	}
}
