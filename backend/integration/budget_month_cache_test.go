package integration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestBudgetMonthlyReadSharing(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	var reads atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "aggregate" && e.Command.Lookup("aggregate").StringValue() == "journal" {
			reads.Add(1)
		}
	}}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_budget_month_cache")
	if err = db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) }()
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	_, err = db.Collection("budgets").InsertOne(ctx, bson.M{"_id": "line", "user_id": p.UserID, "id": "food", "accounts": bson.A{"expenses:food"}, "limit": ledger.MustDecimal("100")})
	if err != nil {
		t.Fatal(err)
	}
	docs := []any{}
	for _, v := range []struct{ date, amount, currency, price string }{{"2026-01-01", "10000000000000000", "EUR", ""}, {"2026-01-01", "1", "EUR", ""}, {"2026-01-01", "-10000000000000000", "EUR", ""}, {"2026-01-02", "3", "USD", "2"}, {"2026-02-03", "0.1", "EUR", ""}, {"2026-02-03", "0.2", "EUR", ""}, {"2026-03-01", "9", "EUR", ""}} {
		post := ledger.Posting{Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal(v.amount), Commodity: v.currency}}
		if v.price != "" {
			post.Price = &ledger.Amount{Value: ledger.MustDecimal(v.price), Commodity: "EUR"}
		}
		docs = append(docs, bson.M{"user_id": p.UserID, "status": "posted", "date": v.date, "postings": []ledger.Posting{post}})
	}
	docs = append(docs, bson.M{"user_id": "01ARZ3NDEKTSV4RRFFQ69G5FAW", "status": "posted", "date": "2026-01-01", "postings": []ledger.Posting{{Account: "expenses:other", Amount: ledger.Amount{Value: ledger.MustDecimal("999"), Commodity: "EUR"}}}})
	if _, err = db.Collection("journal").InsertMany(ctx, docs); err != nil {
		t.Fatal(err)
	}
	svc := budget.NewService(budget.NewMongoRepository(db), nil, nil)
	ranges := [][2]string{{"2026-01-01", "2026-02-28"}, {"2026-02-01", "2026-03-31"}}
	expected := make([][]budget.Month, len(ranges))
	for i, r := range ranges {
		expected[i], err = svc.Months(ctx, p, r[0], r[1])
		if err != nil {
			t.Fatal(err)
		}
	}
	shared := budget.WithMonthReadWindow(reqcache.With(ctx), "2026-01-01", "2026-03-31")
	reads.Store(0)
	got := make([][]budget.Month, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, r := range ranges {
		wg.Add(1)
		go func(i int, r [2]string) { defer wg.Done(); got[i], errs[i] = svc.Months(shared, p, r[0], r[1]) }(i, r)
	}
	wg.Wait()
	for i := range got {
		if errs[i] != nil || !reflect.DeepEqual(got[i], expected[i]) {
			t.Fatalf("range %d differs: %v", i, errs[i])
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("overlapping ranges used %d journal aggregates", reads.Load())
	}
	outside, err := svc.Months(shared, p, "2026-03-01", "2026-04-30")
	if err != nil || len(outside) != 2 || outside[0].Total != 9 || reads.Load() != 2 {
		t.Fatal("out-of-window range did not use its own read")
	}
	other := p
	other.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	otherMonths, err := svc.Months(shared, other, "2026-01-01", "2026-02-28")
	if err != nil || len(otherMonths) != 2 || otherMonths[0].Total != 999 || reads.Load() != 3 {
		t.Fatal("shared monthly rows crossed user boundaries")
	}
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	engine := dashboard.Engine{Expr: expr, Reader: &dashboard.Sources{Budget: svc}}
	reads.Store(0)
	result, err := engine.Evaluate(ctx, p, "size(data.lines) + size(data.months)", map[string]dashboard.Query{"lines": {From: "budget_lines"}, "months": {From: "months"}}, map[string]any{"month": "2026-03"})
	if err != nil || result.Value == nil || reads.Load() != 1 {
		t.Fatalf("dashboard sources used %d aggregates: %v", reads.Load(), err)
	}
	many := make([]any, 5001)
	for i := range many {
		many[i] = bson.M{"user_id": p.UserID, "status": "posted", "date": "2025-12-01", "postings": []ledger.Posting{{Account: fmt.Sprintf("expenses:bulk%d", i), Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}}}}
	}
	if _, err = db.Collection("journal").InsertMany(ctx, many); err != nil {
		t.Fatal(err)
	}
	ctx = budget.WithMonthReadWindow(reqcache.With(ctx), "2025-12-01", "2026-03-31")
	reads.Store(0)
	narrow, err := svc.Months(ctx, p, "2026-01-01", "2026-02-28")
	if err != nil || !reflect.DeepEqual(narrow, expected[0]) || reads.Load() != 2 {
		t.Fatalf("wide-window fallback failed: %v, %d reads", err, reads.Load())
	}
	if _, err = svc.Months(ctx, p, "2025-12-01", "2026-03-31"); err == nil {
		t.Fatal("result cap bypassed")
	}
}
