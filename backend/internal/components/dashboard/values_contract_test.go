package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestTemplateComponentValuesContract(t *testing.T) {
	components := []Component{
		{ID: "k", Type: "kpi", Value: "1234.0", Format: "eur0", Tone: "'neg'", Sub: "'sub'", Delta: "-5.0"},
		{ID: "kup", Type: "kpi", Value: "1", Delta: "2"},
		{ID: "kzero", Type: "kpi", Value: "1", Delta: "0"},
		{ID: "ktext", Type: "kpi", Value: "1", Delta: "'steady'"},
		{ID: "t", Type: "table", Rows: "items", Columns: []Column{{Label: "Amount", Value: "row.amount", Format: "eur0", Tone: "'ok'"}, {Label: "Amount", Value: "row.amount"}}},
		{ID: "s", Type: "scenario_compare", Rows: "items", Columns: []Column{{Label: "Name", Value: "row.name"}}},
		{ID: "b", Type: "budget_bars", Rows: "items", Label: "row.name", Actual: "row.amount", Limit: "15.0"},
		{ID: "n", Type: "text", MD: "**safe** <script>bad</script>"},
	}
	for _, mark := range []string{"bar", "line", "area", "stack", "donut"} {
		components = append(components, Component{ID: "chart-" + mark, Type: "chart", Mark: mark, Rows: "items", Format: "usd0", Encoding: &Encoding{X: Axis{Field: "name"}, Y: Axis{Field: "amount"}}, Rule: &Rule{Y: "15.0", Label: "'limit'"}})
	}
	covered := map[string]bool{}
	for _, c := range components {
		covered[c.Type] = true
	}
	for _, spec := range Templates {
		for _, c := range spec.Components {
			if !covered[c.Type] {
				t.Fatalf("template component type %q lacks contract coverage", c.Type)
			}
		}
	}
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	out, err := (&Engine{Expr: expr, Reader: fixtureReader{}}).Execute(context.Background(), principal.Principal{}, Spec{Data: map[string]Query{"items": {From: "journal"}}, Pages: []Page{{ID: "main"}}, Components: components})
	if err != nil || !out.Validation.OK {
		t.Fatalf("render: %v %+v", err, out.Validation)
	}
	// JSON is the external contract and exercises the same representation as MCP.
	encoded, err := json.Marshal(out.Values)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]map[string]any
	if err := json.Unmarshal(encoded, &values); err != nil {
		t.Fatal(err)
	}
	if values["k"]["sub"] != "sub · €−5" || values["k"]["sub_text"] != "sub" || values["k"]["delta"].(map[string]any)["direction"] != "down" || values["k"]["type"] != "kpi" || values["k"]["value"] != float64(1234) || values["k"]["text"] != "€1,234" || values["k"]["tone"] != "neg" {
		t.Fatal(values["k"])
	}
	for id, want := range map[string]string{"kup": "up", "kzero": "unchanged", "ktext": "neutral"} {
		if got := values[id]["delta"].(map[string]any)["direction"]; got != want {
			t.Fatalf("%s direction = %v, want %s", id, got, want)
		}
	}
	table := values["t"]
	cols := table["columns"].([]any)
	rows := table["rows"].([]any)
	row := rows[0].(map[string]any)
	if table["type"] != "table" || len(cols) != 2 || row["c0"].(map[string]any)["t"] != "€20" || row["c0"].(map[string]any)["tone"] != "ok" || row["c1"] == nil {
		t.Fatal(table)
	}
	if values["s"]["type"] != "scenario_compare" || values["b"]["type"] != "budget" || values["b"]["rows"].([]any)[0].(map[string]any)["status"] != "over" {
		t.Fatal(values)
	}
	if values["n"]["type"] != "text" || strings.Contains(values["n"]["html"].(string), "<script>") || !strings.Contains(values["n"]["html"].(string), "<strong>safe</strong>") {
		t.Fatal(values["n"])
	}
	for _, mark := range []string{"bar", "line", "area", "stack", "donut"} {
		chart := values["chart-"+mark]
		if chart["type"] != "chart" || chart["mark"] != mark || len(chart["points"].([]any)) != 2 || chart["y_format"] != "usd0" || mark != "donut" && chart["rule"].(map[string]any)["y"] != float64(15) {
			t.Fatal(chart)
		}
	}
}
