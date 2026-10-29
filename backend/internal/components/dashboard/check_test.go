package dashboard

import (
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"testing"
)

func TestCheckExpression(t *testing.T) {
	e, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		expr, role string
		ok         bool
		typ        string
	}{
		{"1.0 + 2.0", "value", true, "double"},
		{"now()", "value", true, "google.protobuf.Timestamp"},
		{"row.amount > 0", "where", true, "bool"},
		{"sum(row.amount)", "agg", true, "double"},
		{"last('text')", "agg", true, "string"},
		{"count()", "agg", true, "double"},
		{"sum(unknown)", "agg", false, ""},
		{"bogus(row.amount)", "agg", false, ""},
		{"row.", "derive", false, ""},
	} {
		got := CheckExpression(e, tc.expr, tc.role)
		if got.OK != tc.ok || tc.typ != "" && got.Type != tc.typ || !tc.ok && len(got.Issues) == 0 {
			t.Fatalf("%s: %+v", tc.expr, got)
		}
	}
	got := CheckExpression(e, `sum('😀' + missing)`, "agg")
	if got.OK || len(got.Issues) == 0 || got.Issues[0].Start != 11 || got.Issues[0].End != 12 {
		t.Fatalf("UTF-16 location: %+v", got)
	}
}
