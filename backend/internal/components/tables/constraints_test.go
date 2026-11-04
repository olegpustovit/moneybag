package tables

import (
	"context"
	"reflect"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
)

func TestKeyHashes(t *testing.T) {
	table := Table{Keys: [][]string{{"merchant", "code"}, {"gtin"}, {"amount"}}}
	a, err := keyHashes(table, map[string]any{"merchant": "HR:1", "code": "shop", "gtin": "", "amount": ledger.MustDecimal("1.00")})
	if err != nil {
		t.Fatal(err)
	}
	b, err := keyHashes(table, map[string]any{"merchant": "HR:1", "code": "shop", "amount": ledger.MustDecimal("1.0")})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 2 || !reflect.DeepEqual(a, b) {
		t.Fatal("optional keys or decimal normalization", a, b)
	}
	table.Keys[0] = []string{"code", "merchant"}
	c, err := keyHashes(table, map[string]any{"merchant": "HR:1", "code": "shop", "amount": ledger.MustDecimal("1")})
	if err != nil || !reflect.DeepEqual(a, c) {
		t.Fatal("key field ordering", c, err)
	}
	d, err := keyHashes(table, map[string]any{"merchant": "HR:2", "code": "shop", "amount": ledger.MustDecimal("1")})
	if err != nil || reflect.DeepEqual(a, d) {
		t.Fatal("different key values collide", d, err)
	}
}

func TestDefinitionChecks(t *testing.T) {
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{expr: expr}
	table := Table{ID: "products", Columns: []Column{{ID: "gtin", Type: "string"}}, Keys: [][]string{{"gtin"}}, Checks: []RowCheck{{Expr: "row.gtin=='' || gtin_valid(row.gtin)", Message: "Invalid GTIN"}}}
	if err = s.validateDefinition(table); err != nil {
		t.Fatal(err)
	}
	failed, warnings := s.rowChecks(context.Background(), table, Row{Cells: map[string]any{}})
	if len(failed) != 0 || len(warnings) != 0 {
		t.Fatal("optional GTIN missing", failed, warnings)
	}
	failed, _ = s.rowChecks(context.Background(), table, Row{Cells: map[string]any{"gtin": "bad"}})
	if len(failed) != 1 {
		t.Fatal("bad GTIN accepted", failed)
	}
	table.Checks[0].Level = "warn"
	failed, warnings = s.rowChecks(context.Background(), table, Row{Cells: map[string]any{"gtin": "bad"}})
	if len(failed) != 0 || len(warnings) != 1 {
		t.Fatal("warning rejected or hidden", failed, warnings)
	}
	for _, keys := range [][][]string{{{}}, {{"missing"}}, {{"gtin", "gtin"}}, {{"gtin"}, {"gtin"}}} {
		table.Keys = keys
		if s.validateDefinition(table) == nil {
			t.Fatal("bad keys accepted", keys)
		}
	}
	table.Keys = nil
	for _, check := range []RowCheck{{Expr: "1", Message: "bad"}, {Expr: "missing(", Message: "bad"}, {Expr: "true", Message: ""}, {Expr: "true", Message: "bad", Level: "ignore"}} {
		table.Checks = []RowCheck{check}
		if s.validateDefinition(table) == nil {
			t.Fatal("bad check accepted", check)
		}
	}
}
