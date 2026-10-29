package expressions

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestCEL(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		expr  string
		value any
	}{{"sum([0.1, 0.2])", 0.30000000000000004}, {"round(100.0 / 3.0, 2)", 33.33}, {"[1,2,3].filter(x, x > 1).map(x, x * 2)", nil}, {"month('2026-10-28')", "2026-10"}, {"pct(12, 0)", float64(0)},
		{"month_of('2026-10-19')", "2026-10"}, {"days_in_month('2026-02')", float64(28)}, {"day_of_month('2026-10-19')", float64(19)},
		{"'a,b'.split(',').join('-')", "a-b"}, {"'ab'.substring(1)", "b"}, {"'ab'.upperAscii().lowerAscii()", "ab"}, {"'%d-%s'.format([3, 'x'])", "3-x"},
		{"[1, 1, 2].distinct().size()", float64(2)}, {"[{'k': 2}, {'k': 1}].sortBy(m, m.k)[0].k", float64(1)}, {"today().size()", float64(10)}, {"now() > timestamp('2020-01-01T00:00:00Z')", true}} {
		r, err := e.Eval(context.Background(), tc.expr, nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.expr, err)
		}
		if tc.value != nil && r.Value != tc.value {
			t.Fatalf("%s: %#v", tc.expr, r)
		}
	}
	for _, expr := range []string{"missing + 1", strings.Repeat("1", 2001), "row.__proto__", "sum(['x'])"} {
		if _, err = e.Eval(context.Background(), expr, nil); err == nil {
			t.Fatalf("accepted %s", expr)
		}
	}
}

func TestDataReferences(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		expr    string
		refs    []string
		dynamic bool
	}{
		{`data.foo + data["bar"][0] + size("data.fake")`, []string{"bar", "foo"}, false},
		{`data[params.key]`, nil, true},
		{`data.items.map(x, x.value)`, []string{"items"}, false},
	} {
		refs, dynamic, err := e.DataReferences(tc.expr)
		if err != nil || !slices.Equal(refs, tc.refs) || dynamic != tc.dynamic {
			t.Fatalf("%s: refs=%v dynamic=%v err=%v", tc.expr, refs, dynamic, err)
		}
	}
}

func TestVolatilityTracksNowAcrossChildContexts(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithVolatility(context.Background())
	if _, err := e.Eval(ctx, "today()", nil); err != nil || Volatile(ctx) {
		t.Fatalf("today: %v, volatile=%v", err, Volatile(ctx))
	}
	if _, err := e.Eval(context.WithValue(ctx, struct{}{}, 1), "now()", nil); err != nil || !Volatile(ctx) {
		t.Fatalf("now: %v, volatile=%v", err, Volatile(ctx))
	}
}

func TestPreparedEvaluationMatchesEvaluation(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, expr := range []string{`row.amount + 1`, `now() > timestamp('2020-01-01T00:00:00Z')`, `row.missing`} {
		program, err := e.Compile(expr)
		if err != nil {
			t.Fatal(err)
		}
		vars := map[string]any{"row": map[string]any{"amount": 2.0}}
		first, second := WithVolatility(context.Background()), WithVolatility(context.Background())
		want, wantErr := e.Eval(first, expr, vars)
		got, gotErr := EvalProgram(second, expr, program, vars)
		if got != want || (gotErr == nil) != (wantErr == nil) || Volatile(first) != Volatile(second) {
			t.Fatalf("%s: got %+v, %v; want %+v, %v", expr, got, gotErr, want, wantErr)
		}
	}
}

func TestVolatilityPrefilterMatchesOriginal(t *testing.T) {
	for _, src := range []string{"", "today()", "row.amount > 0", "now()", "now ()", "unknown", "snow now!", "'now'", "_now", "now_", "é now 日本", "now\n", "NOW()"} {
		ctx := WithVolatility(context.Background())
		markVolatile(ctx, src)
		if got, want := Volatile(ctx), nowCall.MatchString(src); got != want {
			t.Fatalf("%q: got %t, want %t", src, got, want)
		}
	}
}

func TestRowActivationMatchesCopiedBindings(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"params": map[string]any{"n": 3.0}, "data": map[string]any{"items": []any{1.0, 2.0}}, "row": map[string]any{"amount": 99.0}}
	row := map[string]any{"amount": 2.0}
	for _, tc := range []struct {
		expr string
		row  map[string]any
	}{
		{`row.amount + params.n`, row}, {`row.amount`, nil}, {`size(data.items)`, row}, {`size(computed) + size(rates)`, row}, {`row.missing`, row}, {`now() > timestamp('2020-01-01T00:00:00Z')`, row},
	} {
		program, err := e.Compile(tc.expr)
		if err != nil {
			t.Fatal(err)
		}
		copied := maps.Clone(vars)
		if tc.row != nil {
			copied["row"] = tc.row
		}
		before, after := WithVolatility(context.Background()), WithVolatility(context.Background())
		want, wantErr := EvalProgram(before, tc.expr, program, copied)
		got, gotErr := EvalProgramRow(after, tc.expr, program, vars, tc.row)
		if !reflect.DeepEqual(want, got) || (wantErr == nil) != (gotErr == nil) || wantErr != nil && gotErr != nil && wantErr.Error() != gotErr.Error() || Volatile(before) != Volatile(after) {
			t.Fatalf("%s: got %+v %v, want %+v %v", tc.expr, got, gotErr, want, wantErr)
		}
	}
	program, err := e.Compile(`size(row) + size(data) + size(params) + size(computed) + size(rates)`)
	if err != nil {
		t.Fatal(err)
	}
	want, wantErr := EvalProgram(context.Background(), "defaults", program, nil)
	got, gotErr := EvalProgramRow(context.Background(), "defaults", program, nil, nil)
	if !reflect.DeepEqual(want, got) || wantErr != nil || gotErr != nil {
		t.Fatalf("default bindings: got %+v %v, want %+v %v", got, gotErr, want, wantErr)
	}
	if vars["row"].(map[string]any)["amount"] != 99.0 {
		t.Fatal("row bindings were mutated")
	}
}
