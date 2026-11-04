package expressions

import (
	"reflect"
	"testing"
)

func TestRequiredPredicates(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		expr string
		want []RowPredicate
	}{
		{`row.entry=='one'`, []RowPredicate{{"entry", []any{"one"}}}},
		{`'one'==row['entry'] && row.line==2`, []RowPredicate{{"entry", []any{"one"}}, {"line", []any{int64(2)}}}},
		{`row.store in ['one','two'] && row.amount>0`, []RowPredicate{{"store", []any{"one", "two"}}}},
		{`row.entry=='one' || row.entry=='two'`, []RowPredicate{}},
		{`!(row.entry=='one')`, []RowPredicate{}},
		{`row.entry.id=='one'`, []RowPredicate{}},
		{`row.entry==params.entry`, []RowPredicate{}},
		{`row.entry in [params.entry]`, []RowPredicate{}},
	} {
		got, err := e.RequiredPredicates(tc.expr)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: %#v %v; want %#v", tc.expr, got, err, tc.want)
		}
	}
}
