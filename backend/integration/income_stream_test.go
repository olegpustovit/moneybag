package integration

import (
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestIncomeReconcileStreamsJournalAndPreservesLimit(t *testing.T) {
	ctx, p, sources, reads := postingsFixture(t)
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	service := model.NewService(model.NewPostgresRepository(pgtest.New(t)), tables.NewService(tables.NewPostgresRepository(pgtest.New(t)), nil, expr), nil, expr)
	service.Ledger = sources.Ledger
	reads.Store(0)
	empty, err := service.IncomeReconcile(ctx, p, model.ReconcileInput{From: "2026-01-01", To: "2026-01-31"})
	if err != nil || len(empty) != 0 {
		t.Fatal("empty streams", empty, err)
	}
	if n := reads.Load(); n > 3 {
		t.Fatalf("empty streams needed %d reads", n)
	}
	if _, err = service.IncomeReconcile(ctx, p, model.ReconcileInput{StreamID: "missing", From: "2026-01-01", To: "2026-01-31"}); !errors.Is(err, principal.ErrNotFound) {
		t.Fatal("missing stream", err)
	}
	streams := []model.Income{{ID: "tagged", Currency: "EUR", Frequency: "monthly", PayerMatch: `"synthetic" in row.tags`, LandsIn: "assets:test"}}
	if _, err = pgtest.New(t).Exec(ctx, `INSERT INTO income_models(id,streams) VALUES (@user,@streams)`, pgx.NamedArgs{"user": p.UserID, "streams": streams}); err != nil {
		t.Fatal(err)
	}
	extra := make([]ledger.Entry, 497)
	for i := range extra {
		extra[i] = ledger.Entry{UserID: p.UserID, ID: fmt.Sprintf("extra-%04d", i), Seq: int64(21000 + i), Date: "2026-01-01", Status: "posted", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}}}}
	}
	if _, err = insertJournal(ctx, pgtest.New(t), extra...); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"extra-0496", "entry-00001"} {
		if _, err = insertTags(ctx, pgtest.New(t), p.UserID, id, []string{"synthetic"}); err != nil {
			t.Fatal(err)
		}
	}
	reads.Store(0)
	out, err := service.IncomeReconcile(ctx, p, model.ReconcileInput{StreamID: "tagged", From: "2026-01-01", To: "2026-01-31"})
	if err != nil || len(out) != 1 || out[0].Actual != 3 || len(out[0].EntryIDs) != 3 || out[0].EntryIDs[0] != "extra-0496" || out[0].EntryIDs[1] != "entry-00001" || out[0].EntryIDs[2] != "entry-00000" {
		t.Fatalf("income result: %+v, %v", out, err)
	}
	if n := reads.Load(); n > 8 {
		t.Fatalf("income reconciliation needed %d read commands", n)
	}
	if _, err = insertJournal(ctx, pgtest.New(t), ledger.Entry{UserID: p.UserID, ID: "over-limit", Seq: 22000, Date: "2026-01-01", Status: "posted"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.IncomeReconcile(ctx, p, model.ReconcileInput{StreamID: "tagged", From: "2026-01-01", To: "2026-01-31"}); err == nil || !strings.Contains(err.Error(), "too many entries") {
		t.Fatal("expected range limit", err)
	}
}
