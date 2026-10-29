package dashboard

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"strings"
	"testing"
)

func run(t *testing.T, spec Spec) Rendered {
	t.Helper()
	expr, _ := expressions.New()
	out, e := (&Engine{Expr: expr, Reader: fixtureReader{}}).Execute(context.Background(), principal.Principal{}, spec)
	if e != nil {
		t.Fatal(e)
	}
	return out
}

func TestTemplatedTextRowsExpressionAndHideZero(t *testing.T) {
	spec := Spec{
		Data:     map[string]Query{"items": {From: "journal"}, "z": {From: "query:items", Group: []string{"group"}, Agg: map[string]string{"n": "sum(row.amount * 0.0)"}, HideZero: true}},
		Computed: map[string]string{"net": "1234.6"},
		Pages:    []Page{{ID: "main", Title: "P {{ computed.net | usd0 }}", Layout: []Row{{Cols: []Placement{{12, "k"}}}, {Cols: []Placement{{12, "t"}}}, {Cols: []Placement{{12, "n"}}}}}},
		Components: []Component{
			{ID: "k", Type: "kpi", Title: "Net {{ computed.net | eur }}", Value: "computed.net", Sub: "'ratio {{ 0.256 * 100.0 | pct0 }} on {{ \"2026-10-29T10:00:00Z\" | date }}'"},
			{ID: "t", Type: "table", Rows: "data.items.filter(r, r.amount > 15.0)", Columns: []Column{{Label: "Amt {{ computed.net | usd0 }}", Value: "row.amount"}}},
			{ID: "n", Type: "text", MD: "{{ data.items[0].amount | usd }}"},
		},
	}
	out := run(t, spec)
	if !out.Validation.OK {
		t.Fatal(out.Validation)
	}
	if out.Values["k"].(map[string]any)["title"] != "Net €1,234.60" {
		t.Fatalf("component title was not resolved: %#v", out.Values["k"])
	}
	for _, want := range []string{"Net €1,234.60", "ratio 26% on 2026-10-29", "Amt $1,235", "$20.00"} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("missing %q in %s", want, out.HTML)
		}
	}
	if v, _ := out.Values["t"].(map[string]any)["rows"].([]map[string]any); len(v) != 1 {
		t.Fatal("rows expression", out.Values["t"])
	}
	// hide_zero drops the all-zero group row.
	expr, _ := expressions.New()
	r, e := (&Engine{Expr: expr, Reader: fixtureReader{}}).Evaluate(context.Background(), principal.Principal{}, "size(data.z)", spec.Data, nil)
	if e != nil || fmt.Sprint(r.Value) != "0" {
		t.Fatal("hide_zero", r.Value, e)
	}
}

func TestUnresolvedRefNamesComponentAndField(t *testing.T) {
	spec := Spec{Pages: []Page{{ID: "main"}}, Components: []Component{{ID: "a", Type: "text"}, {ID: "b", Type: "kpi", Value: "1", Title: "{{ computed.missing | usd }}"}, {ID: "c", Type: "table", Rows: "nope", Columns: []Column{{Label: "x", Value: "row.a"}}}}}
	out := run(t, spec)
	if out.Validation.OK || len(out.Validation.Errors) < 2 {
		t.Fatal(out.Validation)
	}
	got := map[string]Issue{}
	for _, i := range out.Validation.Errors {
		got[i.ComponentID] = i
	}
	if got["b"].Field != "title" || got["c"].Field != "rows" || !strings.Contains(got["c"].Hint, "data source") {
		t.Fatalf("%+v", out.Validation.Errors)
	}
}

func TestAggregates(t *testing.T) {
	rows := []map[string]any{{"x": 1.0, "y": 2.0}, {"x": 2.0, "y": 4.0}, {"x": 3.0, "y": 6.0}, {"x": 3.0, "y": 6.0}}
	expr, _ := expressions.New()
	x := &execution{ctx: context.Background(), engine: &Engine{Expr: expr}, vars: map[string]any{}}
	for agg, want := range map[string]any{"median(row.x)": 2.5, "mean(row.x)": 2.25, "percentile(row.x, 100)": 3.0, "count_distinct(row.x)": 3.0, "linear_slope(row.x, row.y)": 2.0} {
		if got, e := x.aggregate(agg, rows); e != nil || got != want {
			t.Errorf("%s = %v, %v; want %v", agg, got, e, want)
		}
	}
	if _, e := x.aggregate("percentile(row.x)", rows); e == nil {
		t.Error("percentile needs p")
	}
	if got, _ := x.aggregate("linear_slope(row.x, row.y)", rows[:1]); got != nil {
		t.Error("one point has no slope")
	}
}
