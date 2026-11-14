package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
)

func TestPostgresLedgerQueries(t *testing.T) {
	pool := pgtest.New(t)
	repo := NewPostgresRepository(pool)
	s := NewService(repo, audit.NewService(audit.NewPostgresRepository(pool)))
	ctx := context.Background()
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read", "ledger:write"}, Actor: principal.Actor{Kind: "human", ID: "owner"}}
	for _, a := range []Account{{ID: "assets:bank", Type: "asset", Name: "Bank", Commodities: []string{"EUR"}}, {ID: "expenses:food", Type: "expense", Name: "Food", Commodities: []string{"EUR"}}} {
		if _, err := s.CreateAccount(ctx, p, a); err != nil {
			t.Fatal(err)
		}
	}
	posted, err := s.Post(ctx, p, EntryInput{Date: "2026-01-01", Payee: "Špar [1]", Commit: true, Postings: []Posting{{Account: "assets:bank", Amount: Amount{MustDecimal("-1.20"), "EUR"}}, {Account: "expenses:food", Amount: Amount{MustDecimal("1.20"), "EUR"}}}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Entries(ctx, p, EntriesInput{Text: "špar [1]", Sort: "amount", Account: "expenses:food"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != posted.Entry.ID || page.Items[0].Postings[1].Amount.Value.String() != "1.20" {
		t.Fatalf("literal Unicode search/amount sort: %+v %v", page, err)
	}
	page, err = s.Entries(ctx, p, EntriesInput{Cursor: 1 << 40})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("64-bit cursor: %+v %v", page, err)
	}
	if _, err = s.SetTags(ctx, p, TagInput{EntryIDs: []string{posted.Entry.ID}, Add: []string{"synthetic"}}); err != nil {
		t.Fatal(err)
	}
	page, err = s.Entries(ctx, p, EntriesInput{Tag: "synthetic"})
	if err != nil || len(page.Items) != 1 || len(page.Items[0].Tags) != 1 {
		t.Fatalf("tag overlay: %+v %v", page, err)
	}
	combined := WithCombinedBudget(WithCombinedJournal(reqcache.With(ctx), []string{"2026-01-01"}), "2026-01-01", "2026-01-31")
	count := 0
	if err = repo.PostedStream(combined, p, "", "", func(e Entry) error {
		count++
		if e.UserID != p.UserID {
			t.Fatal("raw journal lost owner")
		}
		return nil
	}); err != nil || count != 1 {
		t.Fatalf("combined stream: %d %v", count, err)
	}
	months, ok, err := repo.CombinedMonthRows(combined, p, "2026-01-01", "2026-01-31")
	if err != nil || !ok || len(months) != 1 || months[0].Amount.String() != "1.20" {
		t.Fatalf("combined monthly money: %+v %v %v", months, ok, err)
	}
	balances, err := s.Balances(ctx, p, BalancesInput{At: "2026-01-01"}, nil)
	if err != nil || len(balances.Rows) != 2 {
		t.Fatalf("balances: %+v %v", balances, err)
	}
	other := p
	other.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	if _, err = s.Get(ctx, other, posted.Entry.ID); !errors.Is(err, principal.ErrNotFound) {
		t.Fatal("cross-owner entry", err)
	}
	if _, err = s.Reverse(ctx, p, ReverseInput{ID: posted.Entry.ID, Reason: "synthetic reversal"}); err != nil {
		t.Fatal(err)
	}
	page, err = s.Entries(ctx, p, EntriesInput{Effective: true})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("effective reversal: %+v %v", page, err)
	}
	if err = s.Verify(ctx, p); err != nil {
		t.Fatal(err)
	}
}
