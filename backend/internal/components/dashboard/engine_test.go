package dashboard

import (
	"context"
	"encoding/json"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"strings"
	"testing"
)

type fixtureReader struct{}

func (fixtureReader) Rows(_ context.Context, _ principal.Principal, source string) ([]map[string]any, error) {
	return []map[string]any{{"name": "<script>bad</script>", "amount": float64(20), "group": "A"}, {"name": "Coffee", "amount": float64(10), "group": "A"}}, nil
}
func TestServerRenderAndValidation(t *testing.T) {
	expr, e := expressions.New()
	if e != nil {
		t.Fatal(e)
	}
	engine := Engine{Expr: expr, Reader: fixtureReader{}}
	spec := Spec{ID: "test", Title: "Synthetic dashboard", Data: map[string]Query{"grouped": {From: "query:items", Group: []string{"group"}, Agg: map[string]string{"total": "sum(row.amount)"}}, "items": {From: "journal", Where: "row.amount > 0"}}, Computed: map[string]string{"later": "computed.total + 1.0", "total": "sum(data.items.map(x, x.amount))"}, Pages: []Page{{ID: "main", Title: "Overview", Layout: []Row{{Cols: []Placement{{6, "total"}, {6, "rows"}}}}}}, Components: []Component{{ID: "total", Type: "kpi", Title: "Total", Value: "computed.total", Format: "eur0"}, {ID: "rows", Type: "table", Rows: "items", Columns: []Column{{Label: "Name", Value: "row.name"}, {Label: "Amount", Value: "row.amount"}}}}}
	out, e := engine.Execute(context.Background(), principal.Principal{}, spec)
	if e != nil || !out.Validation.OK {
		t.Fatal(out.Validation, e)
	}
	if out.Values["total"] != float64(30) || !strings.Contains(out.HTML, "€30") || strings.Contains(out.HTML, "<script>") {
		t.Fatal("render or escaping failed", out)
	}
	spec.Components[0].Value = "missing.reference"
	out, e = engine.Execute(context.Background(), principal.Principal{}, spec)
	if e != nil || out.Validation.OK || len(out.Validation.Errors) == 0 {
		t.Fatal("invalid expression passed", out, e)
	}
	spec.Components[0].Value = "1"
	spec.Pages[0].Layout[0].Cols[0].Component = "absent"
	out, e = engine.Execute(context.Background(), principal.Principal{}, spec)
	if e != nil || out.Validation.OK {
		t.Fatal("missing layout component passed", out, e)
	}
}
func TestAllChartMarksAndTextEscape(t *testing.T) {
	expr, _ := expressions.New()
	engine := Engine{Expr: expr, Reader: fixtureReader{}}
	for _, mark := range []string{"bar", "line", "area", "stack", "donut"} {
		spec := Spec{Data: map[string]Query{"rows": {From: "journal"}}, Pages: []Page{{ID: "main", Layout: []Row{{Cols: []Placement{{12, "chart"}}}}}}, Components: []Component{{ID: "chart", Type: "chart", Mark: mark, Rows: "rows", Encoding: &Encoding{X: Axis{Field: "name"}, Y: Axis{Field: "amount"}}}}}
		out, e := engine.Execute(context.Background(), principal.Principal{}, spec)
		if e != nil || !out.Validation.OK || !strings.Contains(out.HTML, "<svg") || !strings.Contains(out.HTML, "View chart data") || !strings.Contains(out.HTML, `tabindex="0" role="region"`) {
			b, _ := json.Marshal(out.Validation)
			t.Fatalf("%s: %s %v", mark, b, e)
		}
	}
}

func TestChartDataExposesExactValuesAndReference(t *testing.T) {
	expr, _ := expressions.New()
	engine := Engine{Expr: expr, Reader: fixtureReader{}}
	spec := Spec{Data: map[string]Query{"rows": {From: "journal"}}, Pages: []Page{{ID: "main", Layout: []Row{{Cols: []Placement{{12, "chart"}}}}}}, Components: []Component{{ID: "chart", Type: "chart", Title: "Monthly values", Mark: "bar", Rows: "rows", Encoding: &Encoding{X: Axis{Field: "name"}, Y: Axis{Field: "amount"}, Color: &Axis{Field: "group"}}, Rule: &Rule{Y: "25.5", Label: "'Budget'"}}}}
	result, err := engine.Execute(context.Background(), principal.Principal{}, spec)
	if err != nil || !result.Validation.OK {
		t.Fatal(result.Validation, err)
	}
	for _, want := range []string{`<th scope="row">A</th>`, `<td class="r">20</td>`, `Reference: Budget`, `25.5`, `&lt;script&gt;bad&lt;/script&gt;`} {
		if !strings.Contains(result.HTML, want) {
			t.Fatalf("missing accessible chart data %q", want)
		}
	}
}

func TestExpressionUsesFullQueryPipeline(t *testing.T) {
	expr, _ := expressions.New()
	engine := Engine{Expr: expr, Reader: fixtureReader{}}
	r, e := engine.Evaluate(context.Background(), principal.Principal{}, "data.grouped[0].total", map[string]Query{"grouped": {From: "query:items", Group: []string{"group"}, Agg: map[string]string{"total": "sum(row.doubled)"}}, "items": {From: "journal", Where: "row.amount >= 20.0", Derive: map[string]string{"doubled": "row.amount * 2.0"}}}, nil)
	if e != nil || r.Value != float64(40) || r.Cost == 0 {
		t.Fatal(r, e)
	}
}

func TestDataByteBudget(t *testing.T) {
	if e := boundedData(map[string]any{"rows": []string{strings.Repeat("x", 5*1024*1024), strings.Repeat("y", 5*1024*1024)}}); e == nil {
		t.Fatal("query byte budget was not enforced")
	}
}

func TestDeriveOverwriteDependenciesUseNewValues(t *testing.T) {
	expr, _ := expressions.New()
	engine := Engine{Expr: expr, Reader: fixtureReader{}}
	for range 20 {
		result, e := engine.Evaluate(context.Background(), principal.Principal{}, "data.rows[0].adjusted", map[string]Query{"rows": {From: "journal", Derive: map[string]string{"amount": "row.amount * 2.0", "adjusted": "row.amount + 1.0"}}}, nil)
		if e != nil || result.Value != float64(41) {
			t.Fatal("derive depended on map order", result, e)
		}
	}
}

type emptyModelReader struct{}

func (emptyModelReader) Rows(_ context.Context, _ principal.Principal, source string) ([]map[string]any, error) {
	if source == "scenarios" {
		return []map[string]any{}, nil
	}
	out := []map[string]any{}
	for key, value := range map[string]float64{"start_net_usd": 0, "income_usd": 0, "spend_eur": 0, "eurusd": 1, "gold_g": 0, "gold_usd_g": 0, "gold_shock_pct": 0, "horizon": 12} {
		out = append(out, map[string]any{"key": key, "value": value})
	}
	return out, nil
}
func TestForecastTemplateWithEmptyNewUser(t *testing.T) {
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	engine := Engine{Expr: expr, Reader: emptyModelReader{}}
	result, err := engine.Execute(context.Background(), principal.Principal{}, Templates["forecast"])
	if err != nil || !result.Validation.OK || !strings.Contains(result.HTML, "Set spending assumptions") {
		t.Fatal(result.Validation, err)
	}
}

func TestNiceStep(t *testing.T) {
	for raw, want := range map[float64]float64{537.5: 1000, 450: 500, 120: 200, 0.3: 0.5, 1: 1, 7: 10} {
		if got := niceStep(raw); got != want {
			t.Errorf("niceStep(%v) = %v, want %v", raw, got, want)
		}
	}
}
