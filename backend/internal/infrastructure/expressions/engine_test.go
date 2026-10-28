package expressions

import (
	"context"
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
	}{{"sum([0.1, 0.2])", 0.30000000000000004}, {"round(100.0 / 3.0, 2)", 33.33}, {"[1,2,3].filter(x, x > 1).map(x, x * 2)", nil}, {"month('2026-10-28')", "2026-10"}, {"pct(12, 0)", float64(0)}} {
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
