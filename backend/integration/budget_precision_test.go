package integration

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestBudgetMonthlyPrecision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var err error
	p := principal.Principal{UserID: ulid.Make().String(), Scopes: []string{"ledger:read"}}
	_, err = pgtest.New(t).Exec(ctx, `INSERT INTO budgets(storage_key,user_id,id,accounts) VALUES (@key,@user,'food',ARRAY['expenses:food'])`, pgx.NamedArgs{"key": p.UserID + ":food", "user": p.UserID})
	if err != nil {
		t.Fatal(err)
	}
	values := []struct{ account, value, commodity, price string }{
		{"expenses:food", "10000000000000000", "EUR", ""},
		{"expenses:food", "1", "EUR", ""},
		{"expenses:food", "-10000000000000000", "EUR", ""},
		{"expenses:food", "3", "USD", "2"},
		{"expenses:other", "2", "EUR", ""},
	}
	for i, v := range values {
		posting := ledger.Posting{Account: v.account, Amount: ledger.Amount{Value: ledger.MustDecimal(v.value), Commodity: v.commodity}}
		if v.price != "" {
			posting.Price = &ledger.Amount{Value: ledger.MustDecimal(v.price), Commodity: "EUR"}
		}
		_, err = insertJournal(ctx, pgtest.New(t), ledger.Entry{ID: fmt.Sprintf("jan-%d", i), Seq: int64(i + 1), UserID: p.UserID, Date: fmt.Sprintf("2026-01-%02d", i+1), Status: "posted", Postings: []ledger.Posting{posting}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, value := range []string{"0.1", "0.2"} {
		posting := ledger.Posting{Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal(value), Commodity: "EUR"}}
		_, err = insertJournal(ctx, pgtest.New(t), ledger.Entry{ID: fmt.Sprintf("feb-%d", i), Seq: int64(i + 100), UserID: p.UserID, Date: fmt.Sprintf("2026-02-%02d", i+1), Status: "posted", Postings: []ledger.Posting{posting}})
		if err != nil {
			t.Fatal(err)
		}
	}
	service := budget.NewService(budget.NewPostgresRepository(pgtest.New(t)), nil, nil)
	for i := 0; i < 20; i++ {
		months, err := service.Months(ctx, p, "2026-01-01", "2026-02-28")
		if err != nil || len(months) != 2 || months[0].ByLine["food"] != 7 || months[0].ByLine["unmapped"] != 2 || months[0].Total != 9 || months[1].ByLine["food"] != 0.3 || months[1].Total != 0.3 {
			t.Fatalf("monthly totals: %+v, %v", months, err)
		}
	}
}
