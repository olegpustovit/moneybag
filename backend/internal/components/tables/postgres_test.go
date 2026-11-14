package tables

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestPostgresRowKeysRollbackAndDecimalFormula(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	repo := NewPostgresRepository(pool)
	s := NewService(repo, nil, nil)
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	table := Table{Key: p.UserID + ":test", UserID: p.UserID, ID: "test", Name: "Test", Columns: []Column{{ID: "code", Type: "string"}, {ID: "computed", Type: "formula", Formula: "1"}}, Keys: [][]string{{"code"}}}
	if _, err := repo.insertTable(ctx, table); err != nil {
		t.Fatal(err)
	}
	row := func(id, code string) Row {
		r := Row{Key: p.UserID + ":test:" + id, UserID: p.UserID, TableID: table.ID, ID: id, Cells: map[string]any{"code": code, "computed": ledger.MustDecimal("10000000000000001.01")}, Version: 1}
		var err error
		r.KeyHashes, err = keyHashes(table, r.Cells)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b := row("a", "A"), row("b", "B")
	if err := s.writeBatch(ctx, p, BatchState{Tables: map[string][]Row{table.ID: {a, b}}}); err != nil {
		t.Fatal(err)
	}
	conflict := row("b", "A")
	conflict.Version = 2
	if err := s.writeBatch(ctx, p, BatchState{Tables: map[string][]Row{table.ID: {conflict}}}); !errors.Is(err, principal.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	stored, err := repo.row(ctx, p.UserID, table, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := stored.Cells["computed"].(ledger.Decimal)
	if !ok || value.String() != "10000000000000001.01" || stored.Version != 1 || stored.Cells["code"] != "B" {
		t.Fatalf("failed write changed row or decimal type: %+v", stored)
	}
	var hash string
	if err = pool.QueryRow(ctx, `SELECT key_hash FROM row_keys WHERE user_id=@user AND table_id='test' AND row_id='b'`, pgx.NamedArgs{"user": p.UserID}).Scan(&hash); err != nil || hash != b.KeyHashes[0] {
		t.Fatalf("failed write changed key: %s %v", hash, err)
	}
	a.Archived = true
	if err = s.writeBatch(ctx, p, BatchState{Tables: map[string][]Row{table.ID: {a}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.writeBatch(ctx, p, BatchState{Tables: map[string][]Row{table.ID: {conflict}}}); err != nil {
		t.Fatalf("archive did not release key: %v", err)
	}
}
