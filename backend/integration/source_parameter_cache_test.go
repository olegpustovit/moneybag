package integration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestSourceCacheSharesOnlyRelevantParameters(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	var journalReads atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "aggregate" && e.Command.Lookup("aggregate").StringValue() == "journal" {
			journalReads.Add(1)
		}
	}}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_source_parameters")
	if err = db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) }()
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	other := p
	other.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	for _, owner := range []principal.Principal{p, other} {
		_, err = db.Collection("accounts").InsertOne(ctx, ledger.Account{Key: owner.UserID + ":assets:test", UserID: owner.UserID, ID: "assets:test", Type: "asset", Name: "Test", Commodities: []string{"EUR"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	entry := func(owner principal.Principal, id, date string, seq int64) ledger.Entry {
		return ledger.Entry{ID: id, UserID: owner.UserID, Seq: seq, Date: date, Status: "posted", Kind: "entry", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}}}}
	}
	if _, err = db.Collection("journal").InsertMany(ctx, []any{entry(p, "a", "2026-01-01", 1), entry(p, "b", "2026-02-01", 2), entry(other, "c", "2026-01-01", 1)}); err != nil {
		t.Fatal(err)
	}
	a := audit.NewService(audit.NewMongoRepository(db))
	cache, err := dashboard.NewSnapshots(a)
	if err != nil {
		t.Fatal(err)
	}
	a.OnChange = cache.Changed
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	sources := &dashboard.Sources{Ledger: ledger.NewService(ledger.NewMongoRepository(db), nil), Valuer: testValuer{}, Snapshots: cache}
	engine := dashboard.Engine{Expr: expr, Reader: sources}
	data := map[string]dashboard.Query{"rows": {From: "postings", Agg: map[string]string{"count": "count()"}}}
	run := func(owner principal.Principal, params map[string]any) (float64, error) {
		r, e := engine.Evaluate(ctx, owner, "data.rows[0].count", data, params)
		if e != nil {
			return 0, e
		}
		n, ok := r.Value.(float64)
		if !ok {
			return 0, fmt.Errorf("unexpected result type")
		}
		return n, nil
	}
	start := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got, e := run(p, map[string]any{"unrelated": i})
			if e != nil || got != 2 {
				errs <- fmt.Errorf("concurrent result: %v", e)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if n := journalReads.Load(); n != 1 {
		t.Fatalf("concurrent requests used %d journal aggregates", n)
	}
	if n, e := run(p, map[string]any{"from": "2026-02-01"}); e != nil || n != 1 || journalReads.Load() != 2 {
		t.Fatal("from filter reused an incompatible source")
	}
	if n, e := run(p, map[string]any{"to": "2026-01-01"}); e != nil || n != 1 || journalReads.Load() != 3 {
		t.Fatal("to filter reused an incompatible source")
	}
	if n, e := run(other, map[string]any{"unrelated": "other"}); e != nil || n != 1 || journalReads.Load() != 4 {
		t.Fatal("source cache crossed owner boundaries")
	}
	if err = a.Mutation(ctx, p, func(tx context.Context) error {
		_, e := db.Collection("journal").InsertOne(tx, entry(p, "d", "2026-03-01", 3))
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if n, e := run(p, map[string]any{"unrelated": "after write"}); e != nil || n != 3 || journalReads.Load() != 5 {
		t.Fatal("source cache remained stale after write")
	}
	balances := map[string]dashboard.Query{"accounts": {From: "accounts"}, "history": {From: "balance_history"}}
	journalReads.Store(0)
	first, err := engine.Evaluate(ctx, p, "size(data.accounts) + size(data.history)", balances, map[string]any{"unrelated": 1})
	if err != nil || first.Value == nil || journalReads.Load() != 1 {
		t.Fatal("balance sources did not build")
	}
	second, err := engine.Evaluate(ctx, p, "size(data.accounts) + size(data.history)", balances, map[string]any{"unrelated": 2})
	if err != nil || second.Value != first.Value || journalReads.Load() != 1 {
		t.Fatal("balance sources missed the shared cache")
	}
}
