package tables

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestNestedCellsBSONRoundTrip(t *testing.T) {
	original := Row{ID: "receipt", Cells: map[string]any{
		"issued_at": "2026-11-04T18:23:45+01:00",
		"payees":    []any{"Konzum"},
		"attrs":     map[string]any{"vat": map[string]any{"rate": float64(25)}},
	}}
	encoded, err := bson.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var stored Row
	if err = bson.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	engine, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Eval(context.Background(), `row.attrs.vat.rate == 25 && row.payees[0] == "Konzum" && row.issued_at == "2026-11-04T18:23:45+01:00"`, map[string]any{"row": stored.Cells})
	if err != nil || result.Value != true {
		t.Fatalf("nested cells after storage: %v, %v (%#v)", result.Value, err, stored.Cells)
	}
}

func TestDecimalCellsPreserveStorageAndJSON(t *testing.T) {
	columns := []Column{{ID: "amount", Type: "decimal"}}
	cells := map[string]any{"amount": "10000000000000001.01"}
	if err := normalizeCells(columns, cells); err != nil {
		t.Fatal(err)
	}
	row := Row{ID: "one", Cells: cells, Version: 1}
	encoded, err := bson.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(encoded).Lookup("cells").Document().Lookup("amount")
	if raw.Type != bson.TypeDecimal128 {
		t.Fatalf("stored as %s", raw.Type)
	}
	var restored Row
	if err = bson.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	value, ok := restored.Cells["amount"].(ledger.Decimal)
	if !ok || value.String() != "10000000000000001.01" {
		t.Fatalf("inexact stored decimal: %#v", restored.Cells)
	}
	public, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(public), `"amount":"10000000000000001.01"`) || strings.Contains(string(public), "$numberDecimal") {
		t.Fatalf("invalid public decimal: %s", public)
	}
	for _, text := range []string{"0", "-0.01", "0.0000000000000000000000000000000001", "1234567890123456789012345678901234"} {
		if !validValue(Column{Type: "decimal"}, text) {
			t.Fatalf("rejected %s", text)
		}
	}
	for _, value := range []any{"NaN", "Infinity", "1e2", "12345678901234567890123456789012345", float64(1.23), "01.23"} {
		if validValue(Column{Type: "decimal"}, value) {
			t.Fatalf("accepted invalid decimal %v", value)
		}
	}
	if !validValue(Column{Type: "decimal", Enum: []any{"1.0"}}, ledger.MustDecimal("1.00")) {
		t.Fatal("decimal enum compares scale instead of value")
	}
}

func TestValueTypes(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		value      any
		want       bool
	}{
		{"legacy number", "number", 2.5, true},
		{"legacy string", "string", "22.62", true},
		{"legacy bool", "bool", false, true},
		{"legacy date", "date", "2026-11-04", true},
		{"legacy null", "number", nil, true},
		{"nonfinite number", "number", math.Inf(1), false},
		{"offset retained", "datetime", "2026-11-04T18:23:45.123+01:00", true},
		{"utc", "datetime", "2026-11-04T17:23:45Z", true},
		{"missing offset", "datetime", "2026-11-04T18:23:45", false},
		{"date without time", "datetime", "2026-11-04", false},
		{"invalid date", "datetime", "2026-02-30T18:23:45+01:00", false},
		{"invalid offset hour", "datetime", "2026-11-04T18:23:45+24:00", false},
		{"invalid offset minute", "datetime", "2026-11-04T18:23:45+01:60", false},
		{"single digit hour", "datetime", "2026-11-04T8:23:45+01:00", false},
		{"comma fraction", "datetime", "2026-11-04T18:23:45,123+01:00", false},
		{"string list", "list", []string{"Lidl", "Konzum"}, true},
		{"json list", "list", []any{"Lidl", "Konzum"}, true},
		{"stored list", "list", bson.A{"Lidl"}, true},
		{"empty list", "list", []any{}, true},
		{"mixed list", "list", []any{"Lidl", 12}, false},
		{"oversized list item", "list", []string{strings.Repeat("x", 10001)}, false},
		{"string is not list", "list", "Lidl,Konzum", false},
		{"nested object", "json", map[string]any{"vat": map[string]any{"rate": 25}, "items": []any{true, nil}}, true},
		{"array is not object", "json", []any{"x"}, false},
		{"string is not object", "json", "{}", false},
		{"exact json bound", "json", map[string]any{"x": strings.Repeat("x", 16*1024-8)}, true},
		{"oversized json", "json", map[string]any{"x": strings.Repeat("x", 16*1024-7)}, false},
		{"invalid json number", "json", map[string]any{"x": math.NaN()}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validValue(Column{Type: tc.kind}, tc.value); got != tc.want {
				t.Fatalf("validValue(%s, %T) = %v; want %v", tc.kind, tc.value, got, tc.want)
			}
		})
	}
}

func TestLegacySchemaJSONUnchanged(t *testing.T) {
	var table Table
	const legacy = `{"id":"old","name":"Old","columns":[{"id":"amount","type":"string"}]}`
	if err := json.Unmarshal([]byte(legacy), &table); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != legacy {
		t.Fatalf("schema changed: %s", encoded)
	}
}

func TestColumnSchemaValidation(t *testing.T) {
	engine, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{expr: engine}
	computed := Column{ID: "status", Type: "formula", Formula: "'ok'", Enum: []any{"ok", "bad"}, Pattern: "^[a-z]+$"}
	if err := s.validateColumns([]Column{computed}); err != nil {
		t.Fatal("computed enum/pattern rejected", err)
	}
	if err := validateCells([]Column{computed}, map[string]any{"status": "other"}); err == nil {
		t.Fatal("computed enum was not enforced")
	}
	for _, kind := range []string{"number", "string", "bool", "date", "datetime", "list", "json"} {
		if err := s.validateColumns([]Column{{ID: "value", Type: kind, Description: "Source facts"}}); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	for _, columns := range [][]Column{
		{{ID: "value", Type: "unknown"}},
		{{ID: "value", Type: "string", Description: strings.Repeat("x", 10001)}},
		{{ID: "value", Type: "json"}, {ID: "value", Type: "list"}},
		{{ID: "value", Type: "formula"}},
		{{ID: "value", Type: "ref"}},
		{{ID: "value", Type: "string", Table: "target"}},
		{{ID: "value", Type: "string", Pattern: "["}},
		{{ID: "value", Type: "number", Pattern: "[0-9]"}},
		{{ID: "value", Type: "string", Enum: []any{float64(1)}}},
		{{ID: "value", Type: "string", Enum: []any{nil}}},
		{{ID: "value", Type: "string", Enum: make([]any, 101)}},
	} {
		if err := s.validateColumns(columns); err == nil {
			t.Fatalf("accepted invalid columns: %#v", columns)
		}
	}
}

func TestColumnConstraints(t *testing.T) {
	if err := validateCells([]Column{{ID: "computed_at", Type: "datetime", Formula: "'bad'"}}, map[string]any{"computed_at": "bad"}); err == nil {
		t.Fatal("computed value bypassed its declared datetime type")
	}
	if err := validateCells([]Column{{ID: "computed_entry", Type: "entry", Formula: "1"}}, map[string]any{"computed_entry": float64(1)}); err == nil {
		t.Fatal("computed value bypassed its declared entry type")
	}
	for _, tc := range []struct {
		name   string
		column Column
		value  any
		valid  bool
	}{
		{"optional missing", Column{Type: "string", Enum: []any{"EUR"}}, nil, true},
		{"required missing", Column{Type: "string", Required: true}, nil, false},
		{"required empty", Column{Type: "string", Required: true}, "", false},
		{"required zero", Column{Type: "number", Required: true}, float64(0), true},
		{"required false", Column{Type: "bool", Required: true}, false, true},
		{"enum match", Column{Type: "string", Enum: []any{"EUR", "USD"}}, "EUR", true},
		{"enum mismatch", Column{Type: "string", Enum: []any{"EUR", "USD"}}, "GBP", false},
		{"numeric enum bson", Column{Type: "number", Enum: []any{int32(1), int32(2)}}, float64(1), true},
		{"bool enum", Column{Type: "bool", Enum: []any{false}}, true, false},
		{"pattern match", Column{Type: "string", Pattern: "^[A-Z]{2}$"}, "HR", true},
		{"pattern mismatch", Column{Type: "string", Pattern: "^[A-Z]{2}$"}, "HR1", false},
		{"pattern and enum", Column{Type: "string", Pattern: "^[A-Z]{2}$", Enum: []any{"hr"}}, "hr", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validValue(tc.column, tc.value); got != tc.valid {
				t.Fatalf("got %v; want %v", got, tc.valid)
			}
		})
	}
	if err := validateCells([]Column{{ID: "value", Type: "string", Required: true}}, map[string]any{}); err == nil {
		t.Fatal("missing required cell accepted")
	}
	if err := validateCells([]Column{{ID: "value", Type: "formula", Formula: "1", Required: true}}, map[string]any{"value": float64(1)}); err != nil {
		t.Fatal(err)
	}
}

func TestStructuredEnumsSurviveBSON(t *testing.T) {
	definition := Table{Columns: []Column{
		{ID: "attrs", Type: "json", Enum: []any{map[string]any{"country": "HR", "rate": float64(25)}}},
		{ID: "tags", Type: "list", Enum: []any{[]any{"receipt", "food"}}},
	}}
	encoded, err := bson.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	var restored Table
	if err = bson.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if err = validateCells(restored.Columns, map[string]any{"attrs": map[string]any{"country": "HR", "rate": float64(25)}, "tags": []any{"receipt", "food"}}); err != nil {
		t.Fatal(err)
	}
	if err = validateCells(restored.Columns, map[string]any{"attrs": map[string]any{"country": "HR", "rate": float64(24)}, "tags": []any{"receipt", "food"}}); err == nil {
		t.Fatal("structured enum mismatch accepted")
	}
}
