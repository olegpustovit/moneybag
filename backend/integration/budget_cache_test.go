package integration

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
)

func TestBudgetLinesRequestCache(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var reads atomic.Int64
	pool := tracedPool(t, queryObserver(func(sql string) {
		if strings.Contains(sql, "FROM budgets") {
			reads.Add(1)
		}
	}))
	var err error
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	other := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", Scopes: []string{"ledger:read"}}
	_, err = pool.Exec(ctx, `INSERT INTO budgets(storage_key,user_id,id,accounts,group_ref) VALUES
 ('owner:food',@user,'food',ARRAY['expenses:food'],'{"dimension":"kind","value":"food"}'),
 ('other:food',@other,'food',ARRAY['expenses:other'],NULL)`, pgx.NamedArgs{"user": p.UserID, "other": other.UserID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO accounts(storage_key,user_id,id,type,groups) VALUES ('owner:food',@user,'expenses:market','expense','{"kind":"food"}')`, pgx.NamedArgs{"user": p.UserID})
	if err != nil {
		t.Fatal(err)
	}
	service := budget.NewService(budget.NewPostgresRepository(pool), nil, nil)
	ctx = reqcache.With(ctx)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lines, e := service.Lines(ctx, p)
			if e != nil {
				errs <- e
				return
			}
			if len(lines) != 1 || len(lines[0].Accounts) != 2 {
				errs <- fmt.Errorf("unexpected synthetic lines: %+v", lines)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("concurrent calls read budgets %d times", n)
	}
	lines, err := service.Lines(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	lines[0].Accounts[0] = "modified"
	lines[0].LiteralAccounts[0] = "modified"
	lines[0].GroupRef.Value = "modified"
	again, err := service.Lines(ctx, p)
	if err != nil || again[0].Accounts[0] != "expenses:food" || again[0].LiteralAccounts[0] != "expenses:food" || again[0].GroupRef.Value != "food" {
		t.Fatal("cached result was mutated")
	}
	theirs, err := service.Lines(ctx, other)
	if err != nil || len(theirs) != 1 || theirs[0].Accounts[0] != "expenses:other" || reads.Load() != 2 {
		t.Fatal("user cache isolation failed")
	}
	denied := p
	denied.Scopes = nil
	if _, err := service.Lines(ctx, denied); err == nil {
		t.Fatal("cached lines bypassed permission check")
	}
}
