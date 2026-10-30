package dashboard

import (
	"reflect"
	"testing"
)

func TestSourceParams(t *testing.T) {
	base := map[string]any{"from": "2026-01-01", "to": "2026-02-28", "month": "2026-02", "window_months": float64(6), "include_current": true, "unrelated": 1}
	extra := map[string]any{"from": "2026-01-01", "to": "2026-02-28", "month": "2026-02", "window_months": float64(6), "include_current": true, "unrelated": 2}
	dates := []string{"2026-01-31", "2026-02-28"}
	for _, source := range []string{"postings", "journal", "effective_entries", "accounts", "balance_history", "budget_lines", "months"} {
		if !reflect.DeepEqual(sourceParams(source, base, dates), sourceParams(source, extra, dates)) {
			t.Fatalf("%s depends on an unrelated parameter", source)
		}
	}
	changed := map[string]any{"from": "2026-02-01", "to": "2026-02-28", "month": "2026-03", "window_months": float64(3), "include_current": false}
	for _, source := range []string{"postings", "budget_lines", "months"} {
		if reflect.DeepEqual(sourceParams(source, base, dates), sourceParams(source, changed, dates)) {
			t.Fatalf("%s ignored a relevant parameter", source)
		}
	}
	if reflect.DeepEqual(sourceParams("balance_history", base, dates), sourceParams("balance_history", base, []string{"2026-03-31"})) {
		t.Fatal("balance history ignored dates")
	}
	if reflect.DeepEqual(sourceParams("view:custom", base, nil), sourceParams("view:custom", extra, nil)) {
		t.Fatal("custom views lost arbitrary parameters")
	}
	if !reflect.DeepEqual(sourceParams("postings", nil, nil), sourceParams("postings", map[string]any{"from": 42}, nil)) {
		t.Fatal("ignored non-string filter changed the key")
	}
}

func TestLeafViewParams(t *testing.T) {
	leaf := View{ID: "leaf", Query: Query{From: "postings", Where: "row.amount > 0"}}
	a, ok := leafViewParams(leaf, map[string]any{"unused": 1})
	if !ok {
		t.Fatal("parameter-independent leaf not shareable")
	}
	b, ok := leafViewParams(leaf, map[string]any{"unused": 2})
	if !ok || !reflect.DeepEqual(a, b) {
		t.Fatal("unrelated parameter changed leaf key")
	}
	c, ok := leafViewParams(leaf, map[string]any{"from": "2026-01-01"})
	if !ok || reflect.DeepEqual(a, c) {
		t.Fatal("leaf key ignored source date")
	}
	changed := leaf
	changed.Query.Where = "row.amount > 1"
	d, ok := leafViewParams(changed, nil)
	if !ok || reflect.DeepEqual(a, d) {
		t.Fatal("leaf key ignored view definition")
	}
	for _, query := range []Query{{From: "postings", Where: "row.amount > params.floor"}, {From: "postings", Where: "row.name == 'params'"}, {From: "postings", Where: "now() != null"}, {From: "view:other"}, {From: "accounts"}} {
		if _, ok := leafViewParams(View{ID: "unsafe", Query: query}, nil); ok {
			t.Fatalf("unsafe view was shareable: %q", query.From)
		}
	}
}
