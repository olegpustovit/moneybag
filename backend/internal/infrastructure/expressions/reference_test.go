package expressions

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReferenceFieldsAndIdentity(t *testing.T) {
	engine, err := New()
	if err != nil {
		t.Fatal(err)
	}
	value := Reference("original", map[string]any{"id": "merchant-id", "label": "Shop"})
	for _, expr := range []string{`row.store.label == 'Shop'`, `row.store == 'original'`, `'original' == row.store`, `row.store != 'other'`, `'other' != row.store`, `row.store in ['original']`, `row.store.id == 'merchant-id'`} {
		result, err := engine.Eval(context.Background(), expr, map[string]any{"row": map[string]any{"store": value}})
		if err != nil || result.Value != true {
			t.Fatalf("%s: %#v %v", expr, result.Value, err)
		}
	}
	raw, err := json.Marshal(value)
	if err != nil || string(raw) != `{"id":"merchant-id","label":"Shop"}` {
		t.Fatalf("JSON: %s %v", raw, err)
	}
}
