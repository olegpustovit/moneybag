package budget

import (
	"context"
	"errors"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestPostgresBudgetMoneyAndRevision(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read", "ledger:write"}, Actor: principal.Actor{Kind: "human", ID: "owner"}}
	a := audit.NewService(audit.NewPostgresRepository(pool))
	book := ledger.NewService(ledger.NewPostgresRepository(pool), a)
	for _, account := range []ledger.Account{{ID: "assets:bank", Type: "asset", Name: "Bank", Commodities: []string{"EUR"}}, {ID: "expenses:food", Type: "expense", Name: "Food", Commodities: []string{"EUR"}, Groups: map[string]string{"category": "food"}}} {
		if _, err := book.CreateAccount(ctx, p, account); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(NewPostgresRepository(pool), a, nil)
	limit := ledger.MustDecimal("-0.00")
	first, err := s.Set(ctx, p, SetInput{ID: "food", Limit: &limit, GroupRef: &GroupRef{Dimension: "category", Value: "food"}})
	if err != nil {
		t.Fatal(err)
	}
	lines, err := s.Lines(ctx, p)
	if err != nil || len(lines) != 1 || lines[0].Limit.String() != "-0.00" || len(lines[0].Accounts) != 1 || lines[0].Accounts[0] != "expenses:food" {
		t.Fatalf("lines: %+v %v", lines, err)
	}
	stale := first.Line.Revision - 1
	if _, err = s.Set(ctx, p, SetInput{ID: "food", ExpectedRevision: &stale}); !errors.Is(err, principal.ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	for _, value := range []string{"10000000000000000", "1", "-10000000000000000"} {
		v := ledger.MustDecimal(value)
		if _, err = book.Post(ctx, p, ledger.EntryInput{Date: "2026-01-01", Commit: true, Postings: []ledger.Posting{{Account: "expenses:food", Amount: ledger.Amount{Value: v, Commodity: "EUR"}}, {Account: "assets:bank", Amount: ledger.Amount{Value: v.Negate(), Commodity: "EUR"}}}}); err != nil {
			t.Fatal(err)
		}
	}
	months, err := s.Months(ctx, p, "2026-01-01", "2026-01-31")
	if err != nil || len(months) != 1 || months[0].Total != 1 || months[0].ByLine["food"] != 1 {
		t.Fatalf("exact monthly sum: %+v %v", months, err)
	}
	if err = s.Archive(ctx, p, "food", true, "test"); err != nil {
		t.Fatal(err)
	}
	lines, err = s.Lines(ctx, p)
	if err != nil || len(lines) != 0 {
		t.Fatalf("archived: %+v %v", lines, err)
	}
}
