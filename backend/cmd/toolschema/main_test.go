package main

import (
	"reflect"
	"testing"
)

func TestNamespaceRefs(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{"a": map[string]any{"$ref": "#/$defs/Amount"}, "b": []any{map[string]any{"$ref": "https://example.test/schema"}}}, "$defs": map[string]any{"Amount": map[string]any{"$ref": "#/$defs/Other"}}}
	namespaceRefs(schema, "entry_input")
	want := map[string]any{"properties": map[string]any{"a": map[string]any{"$ref": "#/$defs/entry_input/$defs/Amount"}, "b": []any{map[string]any{"$ref": "https://example.test/schema"}}}, "$defs": map[string]any{"Amount": map[string]any{"$ref": "#/$defs/entry_input/$defs/Other"}}}
	if !reflect.DeepEqual(schema, want) {
		t.Fatalf("got %#v", schema)
	}
}
