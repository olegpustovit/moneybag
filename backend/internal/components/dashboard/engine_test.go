package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	if total := out.Values["total"].(map[string]any); total["value"] != float64(30) || total["text"] != "€30" {
		t.Fatal("render failed", out)
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

func TestComponentErrorsKeepHealthyComponents(t *testing.T) {
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	engine := Engine{Expr: expr, Reader: fixtureReader{}}
	spec := Spec{Data: map[string]Query{}, Pages: []Page{{ID: "main", Layout: []Row{{Cols: []Placement{{6, "good"}, {6, "broken"}}}}}}, Components: []Component{{ID: "good", Type: "kpi", Value: "42"}, {ID: "broken", Type: "kpi", Value: "unknown_function(1)"}}}
	result, err := engine.Execute(context.Background(), principal.Principal{}, spec)
	if err != nil || result.Validation.OK || result.Values["good"] == nil || result.Values["broken"] != nil || len(result.Pages) != 1 {
		t.Fatal(result, err)
	}
	if len(result.Validation.Errors) != 1 || result.Validation.Errors[0].ComponentID != "broken" || result.Validation.Errors[0].Expr != "unknown_function(1)" {
		t.Fatal(result.Validation.Errors)
	}
	spec.Components[1].ID = "broken/~one"
	spec.Pages[0].Layout[0].Cols[1].Component = "broken/~one"
	result, err = engine.Execute(context.Background(), principal.Principal{}, spec)
	if err != nil || result.Values["good"] == nil || len(result.Validation.Errors) != 1 || result.Validation.Errors[0].ComponentID != "broken/~one" || result.Validation.Errors[0].Path != "/components/broken~1~0one/value" {
		t.Fatal("escaped component ID", result, err)
	}
	spec.Pages[0].Layout[0].Cols[1].Component = "broken"
	spec.Components[1] = Component{ID: "broken", Type: "table", Rows: "missing", Columns: []Column{{Label: "Value", Value: "row.value"}}}
	result, err = engine.Execute(context.Background(), principal.Principal{}, spec)
	if err != nil || result.Validation.OK || result.Values["good"] == nil || len(result.Validation.Errors) != 1 || result.Validation.Errors[0].ComponentID != "broken" || result.Validation.Errors[0].Hint == "" {
		t.Fatal(result, err)
	}
	spec.Data = map[string]Query{"bad": {From: "journal", Where: "unknown_function(1)"}}
	result, err = engine.Execute(context.Background(), principal.Principal{}, spec)
	if err != nil || result.Validation.OK || len(result.Values) != 0 {
		t.Fatal("global query error must not return misleading values", result, err)
	}
}
func TestAllChartMarksAndTextEscape(t *testing.T) {
	expr, _ := expressions.New()
	engine := Engine{Expr: expr, Reader: fixtureReader{}}
	for _, mark := range []string{"bar", "line", "area", "stack", "donut"} {
		spec := Spec{Data: map[string]Query{"rows": {From: "journal"}}, Pages: []Page{{ID: "main", Layout: []Row{{Cols: []Placement{{12, "chart"}}}}}}, Components: []Component{{ID: "chart", Type: "chart", Mark: mark, Rows: "rows", Encoding: &Encoding{X: Axis{Field: "name"}, Y: Axis{Field: "amount"}}}}}
		out, e := engine.Execute(context.Background(), principal.Principal{}, spec)
		if e != nil || !out.Validation.OK || len(out.Values["chart"].(map[string]any)["points"].([]chartPoint)) == 0 {
			b, _ := json.Marshal(out.Validation)
			t.Fatalf("%s: %s %v", mark, b, e)
		}
	}
	spec := Spec{Pages: []Page{{ID: "main", Layout: []Row{{Cols: []Placement{{12, "note"}}}}}}, Components: []Component{{ID: "note", Type: "text", MD: "<script>x</script> **bold**"}}}
	out, e := engine.Execute(context.Background(), principal.Principal{}, spec)
	if e != nil || out.Values["note"].(map[string]any)["html"] != "&lt;script&gt;x&lt;/script&gt; <strong>bold</strong>" {
		t.Fatal("text escape", out.Values, e)
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
	encoded, _ := json.Marshal(result.Values)
	for _, want := range []string{`"series":"A"`, `"y":20`, `"label":"Budget"`, `"y":25.5`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("missing chart value %q in %s", want, encoded)
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
	encoded, _ := json.Marshal(result.Values)
	if err != nil || !result.Validation.OK || !strings.Contains(string(encoded), "Set spending assumptions") {
		t.Fatal(result.Validation, err)
	}
}

type countingReader struct{ calls int }

func (r *countingReader) Rows(context.Context, principal.Principal, string) ([]map[string]any, error) {
	r.calls++
	return []map[string]any{{"amount": float64(1)}}, nil
}
func TestSourceReadOncePerExecutionAndRowsNotShared(t *testing.T) {
	expr, _ := expressions.New()
	reader := &countingReader{}
	engine := Engine{Expr: expr, Reader: reader}
	data := map[string]Query{"a": {From: "journal", Derive: map[string]string{"amount": "row.amount + 1.0"}}, "b": {From: "journal"}}
	spec := Spec{Data: data, Computed: map[string]string{"b": "data.b[0].amount", "a": "data.a[0].amount"}, Pages: []Page{{ID: "main"}}}
	out, e := engine.Execute(context.Background(), principal.Principal{}, spec)
	if e != nil || !out.Validation.OK || reader.calls != 1 {
		t.Fatal(out.Validation, e, reader.calls)
	}
}

func TestFailedSourceIsMemoized(t *testing.T) {
	ctx := reqcache.With(context.Background())
	sources := &Sources{}
	var first error
	for range 5 {
		_, err := sources.Rows(ctx, principal.Principal{UserID: "test"}, "missing")
		if err == nil {
			t.Fatal("expected source failure")
		}
		if first != nil && err != first {
			t.Fatal("source failure was rebuilt")
		}
		first = err
	}
}

func TestCachedViewCycleFailsBeforeWaiting(t *testing.T) {
	ctx := context.WithValue(reqcache.With(context.Background()), viewStackKey{}, []string{"loop"})
	_, err := (&Sources{}).Rows(ctx, principal.Principal{UserID: "test"}, "view:loop")
	if err == nil || !strings.Contains(err.Error(), "view cycle") {
		t.Fatalf("expected view cycle, got %v", err)
	}
}

type largeReader struct{}

func (largeReader) Rows(context.Context, principal.Principal, string) ([]map[string]any, error) {
	rows := make([]map[string]any, 6000)
	for i := range rows {
		rows[i] = map[string]any{"value": float64(i), "padding": strings.Repeat("x", 1600)}
	}
	return rows, nil
}

func TestLargeSourceCanBeAggregatedWithinResultBudget(t *testing.T) {
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	engine := Engine{Expr: expr, Reader: largeReader{}}
	result, err := engine.Evaluate(context.Background(), principal.Principal{}, "data.summary[0].count", map[string]Query{
		"summary": {From: "postings", Agg: map[string]string{"count": "count()"}},
	}, nil)
	if err != nil || result.Value != float64(6000) {
		t.Fatalf("aggregate: %+v, %v", result, err)
	}
}

type manyRowsReader struct{ n int }

func (r manyRowsReader) Rows(context.Context, principal.Principal, string) ([]map[string]any, error) {
	rows := make([]map[string]any, r.n)
	for i := range rows {
		rows[i] = map[string]any{"value": float64(i)}
	}
	return rows, nil
}

func TestEvaluationAndChartSupportLargerSources(t *testing.T) {
	expr, _ := expressions.New()
	engine := Engine{Expr: expr, Reader: manyRowsReader{n: 20001}}
	result, err := engine.Evaluate(context.Background(), principal.Principal{}, "data.summary[0].count", map[string]Query{
		"summary": {From: "postings", Where: "row.value >= 0.0", Agg: map[string]string{"count": "count()"}},
	}, nil)
	if err != nil || result.Value != float64(20001) {
		t.Fatalf("large source: %+v, %v", result, err)
	}
	engine.Reader = manyRowsReader{n: 601}
	spec := Spec{Data: map[string]Query{"rows": {From: "journal"}}, Pages: []Page{{ID: "main", Layout: []Row{{Cols: []Placement{{12, "chart"}}}}}}, Components: []Component{{ID: "chart", Type: "chart", Mark: "line", Rows: "rows", Encoding: &Encoding{X: Axis{Field: "value"}, Y: Axis{Field: "value"}}}}}
	out, err := engine.Execute(context.Background(), principal.Principal{}, spec)
	if err != nil || !out.Validation.OK {
		t.Fatalf("large chart: %+v, %v", out.Validation, err)
	}
}

type waitingReader struct{ done chan struct{} }

func (r waitingReader) Rows(ctx context.Context, _ principal.Principal, _ string) ([]map[string]any, error) {
	defer close(r.done)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestEvaluationStopsWhenRequestExpires(t *testing.T) {
	expr, _ := expressions.New()
	done := make(chan struct{})
	engine := Engine{Expr: expr, Reader: waitingReader{done: done}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := engine.Evaluate(ctx, principal.Principal{}, "1", map[string]Query{"slow": {From: "journal"}}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("deadline: %v after %v", err, time.Since(start))
	}
	select {
	case <-done:
	default:
		t.Fatal("source did not stop")
	}
}

type graphReader struct {
	calls   atomic.Int64
	active  atomic.Int64
	started chan struct{}
	once    sync.Once
	fail    bool
}

func (r *graphReader) Rows(ctx context.Context, _ principal.Principal, source string) ([]map[string]any, error) {
	r.calls.Add(1)
	if r.fail {
		return nil, fmt.Errorf("synthetic source failure")
	}
	if r.active.Add(1) == 4 {
		r.once.Do(func() { close(r.started) })
	}
	defer r.active.Add(-1)
	select {
	case <-r.started:
		return []map[string]any{{"value": float64(1)}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestQueryGraphRunsIndependentSourcesInParallel(t *testing.T) {
	expr, _ := expressions.New()
	reader := &graphReader{started: make(chan struct{})}
	engine := Engine{Expr: expr, Reader: reader}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := engine.Evaluate(ctx, principal.Principal{}, "data.e[0].value", map[string]Query{
		"a": {From: "a"}, "b": {From: "b"}, "c": {From: "c"}, "d": {From: "d"},
		"e": {From: "query:a", Where: "row.value == data.b[0].value"},
	}, nil)
	if err != nil || result.Value != float64(1) || reader.calls.Load() != 4 {
		t.Fatalf("parallel graph: %+v, %v, calls %d", result, err, reader.calls.Load())
	}
}

func TestQueryGraphPropagatesFailureOnce(t *testing.T) {
	expr, _ := expressions.New()
	reader := &graphReader{fail: true}
	engine := Engine{Expr: expr, Reader: reader}
	data := map[string]Query{"source": {From: "broken"}}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		data[name] = Query{From: "query:source"}
	}
	spec := Spec{Data: data, Pages: []Page{{ID: "main"}}}
	result, err := engine.Execute(context.Background(), principal.Principal{}, spec)
	if err != nil || result.Validation.OK || reader.calls.Load() != 1 {
		t.Fatalf("failed source: %+v, %v, calls %d", result.Validation, err, reader.calls.Load())
	}
	for _, issue := range result.Validation.Errors {
		if issue.Path != "/data/source" && !strings.Contains(issue.Msg, "dependency source failed") {
			t.Fatalf("unexpected failure: %+v", issue)
		}
	}
	if len(result.Validation.Errors) != 6 {
		t.Fatalf("expected source and five dependency errors: %+v", result.Validation.Errors)
	}
}

func TestQueryGraphReportsCyclesAndMissingDependencies(t *testing.T) {
	expr, _ := expressions.New()
	engine := Engine{Expr: expr, Reader: fixtureReader{}}
	for _, tc := range []struct {
		data map[string]Query
		want string
	}{
		{map[string]Query{"a": {From: "query:b"}, "b": {From: "query:a"}}, "cyclic query dependencies"},
		{map[string]Query{"a": {From: "query:missing"}}, "dependency missing not found"},
	} {
		_, err := engine.Evaluate(context.Background(), principal.Principal{}, "1", tc.data, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("expected %q: %v", tc.want, err)
		}
	}
}

func TestDynamicQueryDependencyUsesSerialResolver(t *testing.T) {
	expr, _ := expressions.New()
	engine := Engine{Expr: expr, Reader: fixtureReader{}}
	result, err := engine.Evaluate(context.Background(), principal.Principal{}, "data.range[0].i", map[string]Query{
		"base":  {From: "journal"},
		"range": {From: "range", N: `size(data[params.key])`},
	}, map[string]any{"key": "base"})
	if err != nil || result.Value != float64(0) {
		t.Fatalf("dynamic dependency: %+v, %v", result, err)
	}
}

type deadlineReader struct{ active atomic.Int64 }

func (r *deadlineReader) Rows(ctx context.Context, _ principal.Principal, _ string) ([]map[string]any, error) {
	r.active.Add(1)
	defer r.active.Add(-1)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestParallelQueriesFinishAtDeadline(t *testing.T) {
	expr, _ := expressions.New()
	reader := &deadlineReader{}
	engine := Engine{Expr: expr, Reader: reader}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := engine.Evaluate(ctx, principal.Principal{}, "1", map[string]Query{"a": {From: "a"}, "b": {From: "b"}, "c": {From: "c"}, "d": {From: "d"}, "e": {From: "e"}}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second || reader.active.Load() != 0 {
		t.Fatalf("deadline: %v after %v, %d readers active", err, time.Since(start), reader.active.Load())
	}
}

func TestParallelQueriesMemoizeFailedSource(t *testing.T) {
	expr, _ := expressions.New()
	reader := &graphReader{fail: true}
	engine := Engine{Expr: expr, Reader: reader}
	data := map[string]Query{}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		data[name] = Query{From: "broken"}
	}
	result, err := engine.Execute(context.Background(), principal.Principal{}, Spec{Data: data, Pages: []Page{{ID: "main"}}})
	if err != nil || result.Validation.OK || reader.calls.Load() != 1 || len(result.Validation.Errors) != 5 {
		t.Fatalf("failed source: %+v, %v, calls %d", result.Validation, err, reader.calls.Load())
	}
}

type valueReader float64

func (r valueReader) Rows(context.Context, principal.Principal, string) ([]map[string]any, error) {
	return []map[string]any{{"value": float64(r)}}, nil
}
func TestSourceMemoDoesNotCrossReaderBoundaries(t *testing.T) {
	expr, _ := expressions.New()
	ctx := reqcache.With(context.Background())
	data := map[string]Query{"rows": {From: "journal"}}
	for _, want := range []float64{1, 2} {
		engine := Engine{Expr: expr, Reader: valueReader(want)}
		result, err := engine.Evaluate(ctx, principal.Principal{UserID: "same-owner"}, "data.rows[0].value", data, nil)
		if err != nil || result.Value != want {
			t.Fatalf("reader isolation: %+v, %v; want %v", result, err, want)
		}
	}
}

func TestEvaluationBudgetLeavesNoActiveReaders(t *testing.T) {
	defer func(d time.Duration) { evalBudget = d }(evalBudget)
	evalBudget = 200 * time.Millisecond
	expr, _ := expressions.New()
	reader := &deadlineReader{}
	engine := Engine{Expr: expr, Reader: reader}
	start := time.Now()
	_, err := engine.Evaluate(context.Background(), principal.Principal{}, "1", map[string]Query{"a": {From: "a"}, "b": {From: "b"}, "c": {From: "c"}, "d": {From: "d"}, "e": {From: "e"}}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > evalBudget+100*time.Millisecond || reader.active.Load() != 0 {
		t.Fatalf("budget: %v after %v, %d readers active", err, time.Since(start), reader.active.Load())
	}
}

func TestPostingClassFilterPreservesExpressionBehavior(t *testing.T) {
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{{"class": "bank", "amount": float64(1)}, {"class": "expense", "amount": float64(2)}, {"class": "bank"}}
	for _, tc := range []struct {
		where string
		want  int
		fails bool
	}{
		{`row.class == 'expense' && row.amount > 0.0`, 1, false},
		{`row.class == 'expense' || row.class == 'bank'`, 3, false},
		{`row.class == 'expense' && unknown_function()`, 0, true},
		{`row.class == 'expense' && row.missing > 0`, 0, true},
	} {
		x := &execution{ctx: context.Background(), engine: &Engine{Expr: expr, Reader: retainedReader{rows: rows}}, vars: map[string]any{"data": map[string]any{}}}
		got, err := x.query(Query{From: "postings", Where: tc.where})
		if tc.fails {
			if err == nil {
				t.Fatalf("%s: expected error", tc.where)
			}
			continue
		}
		if err != nil || len(got.([]map[string]any)) != tc.want {
			t.Fatalf("%s: %v %v", tc.where, got, err)
		}
	}
	for _, tc := range []struct {
		row   map[string]any
		fails bool
	}{{map[string]any{"amount": float64(1)}, true}, {map[string]any{"class": float64(1)}, false}} {
		x := &execution{ctx: context.Background(), engine: &Engine{Expr: expr, Reader: retainedReader{rows: []map[string]any{{"class": "bank"}, tc.row}}}, vars: map[string]any{"data": map[string]any{}}}
		got, err := x.query(Query{From: "postings", Where: `row.class == 'expense' && true`})
		if (err != nil) != tc.fails || err == nil && len(got.([]map[string]any)) != 0 {
			t.Fatalf("class %v: %v %v", tc.row, got, err)
		}
	}

}

func TestSimpleRowSelectorsMatchCEL(t *testing.T) {
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"value": "text", "nested": map[string]any{"value": true}},
		{"value": 2.5, "nested": map[string]any{"value": -4.5}},
		{"value": int64(8), "nested": map[string]any{"value": nil}},
		{"value": nil, "nested": map[string]string{"value": "text"}},
		{"nested": "not a map"},
		{},
		nil,
	}
	for _, source := range []string{"row.value", "row.nested.value", "row.value + 1"} {
		program, err := expr.Compile(source)
		if err != nil {
			t.Fatal(err)
		}
		x := &execution{ctx: context.Background(), engine: &Engine{Expr: expr}, vars: map[string]any{}}
		for _, row := range rows {
			before := x.cost
			got, gotErr := x.eval(source, row)
			want, wantErr := expressions.EvalProgramRow(context.Background(), source, program, x.vars, row)
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want.Value) || x.cost-before != want.Cost {
				t.Fatalf("%s: got %#v cost %d error %v; want %#v cost %d error %v", source, got, x.cost-before, gotErr, want.Value, want.Cost, wantErr)
			}
		}
	}
	x := &execution{ctx: context.Background(), engine: &Engine{Expr: expr}, cost: 99999999}
	if _, err := x.eval("row.value", map[string]any{"value": "text"}); err == nil || !strings.Contains(err.Error(), "100 million") {
		t.Fatalf("selector did not enforce page cost limit: %v", err)
	}
}
