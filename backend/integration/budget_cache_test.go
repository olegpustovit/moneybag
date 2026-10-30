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

	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestBudgetLinesRequestCache(t *testing.T) {
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
		if e.CommandName == "find" && e.Command.Lookup("find").StringValue() == "budgets" {
			reads.Add(1)
		}
	}}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_budget_cache")
	if err := db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) }()
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	other := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", Scopes: []string{"ledger:read"}}
	_, err = db.Collection("budgets").InsertMany(ctx, []any{
		bson.M{"_id": "owner:food", "user_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV", "id": "food", "accounts": bson.A{"expenses:food"}, "group_ref": bson.M{"dimension": "kind", "value": "food"}},
		bson.M{"_id": "other:food", "user_id": "01ARZ3NDEKTSV4RRFFQ69G5FAW", "id": "food", "accounts": bson.A{"expenses:other"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Collection("accounts").InsertOne(ctx, bson.M{"_id": "owner:food", "user_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV", "id": "expenses:market", "type": "expense", "groups": bson.M{"kind": "food"}})
	if err != nil {
		t.Fatal(err)
	}
	service := budget.NewService(budget.NewMongoRepository(db), nil, nil)
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
