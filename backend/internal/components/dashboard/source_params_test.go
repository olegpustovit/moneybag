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
