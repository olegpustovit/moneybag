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
