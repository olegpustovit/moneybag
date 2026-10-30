package integration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
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

func TestNestedLeafViewMemoization(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	var viewReads atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "find" && e.Command.Lookup("find").StringValue() == "views" {
			viewReads.Add(1)
		}
	}}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_leaf_views")
	if err = db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) }()
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	other := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", Scopes: []string{"ledger:read"}}
	repo := ledger.NewMongoRepository(db)
	if err = repo.Init(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, err = db.Collection("accounts").InsertOne(ctx, ledger.Account{Key: p.UserID + ":assets:test", UserID: p.UserID, ID: "assets:test", Type: "asset", Name: "Test", Commodities: []string{"EUR"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Collection("journal").InsertOne(ctx, ledger.Entry{UserID: p.UserID, ID: "one", Seq: 1, Date: "2026-01-01", Status: "posted", Payee: "Example", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []dashboard.View{{ID: "leaf", Query: dashboard.Query{From: "postings"}}, {ID: "middle", Query: dashboard.Query{From: "view:leaf"}}, {ID: "left", Query: dashboard.Query{From: "view:middle", Derive: map[string]string{"label": "row.payee + '-left'"}}}, {ID: "right", Query: dashboard.Query{From: "view:middle", Derive: map[string]string{"label": "row.payee + '-right'"}}}, {ID: "cycle-a", Query: dashboard.Query{From: "view:cycle-b"}}, {ID: "cycle-b", Query: dashboard.Query{From: "view:cycle-a"}}} {
		v.UserID = p.UserID
		v.Key = p.UserID + ":" + v.ID
		if _, err = db.Collection("views").InsertOne(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 7; i++ {
		id, next := fmt.Sprintf("deep-%d", i), fmt.Sprintf("deep-%d", i+1)
		if i == 6 {
			next = "middle"
		}
		if _, err = db.Collection("views").InsertOne(ctx, dashboard.View{Key: p.UserID + ":" + id, UserID: p.UserID, ID: id, Query: dashboard.Query{From: "view:" + next}}); err != nil {
			t.Fatal(err)
		}
	}
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	sources := &dashboard.Sources{Ledger: ledger.NewService(repo, nil), Valuer: testValuer{}, Expr: expr}
	engine := &dashboard.Engine{Expr: expr, Reader: sources}
	sources.Views = dashboard.NewService(dashboard.NewMongoRepository(db), engine, nil)
	viewReads.Store(0)
	result, err := engine.Evaluate(ctx, p, "[data.left[0].label, data.right[0].label]", map[string]dashboard.Query{"left": {From: "view:left"}, "right": {From: "view:right"}}, nil)
	if err != nil || fmt.Sprint(result.Value) != "[Example-left Example-right]" {
		t.Fatalf("results: %v, %v", result.Value, err)
	}
	if got := viewReads.Load(); got != 4 {
		t.Fatalf("view definitions read %d times, want 4", got)
	}
	cache, err := dashboard.NewSnapshots(audit.NewService(audit.NewMongoRepository(db)))
	if err != nil {
		t.Fatal(err)
	}
	sources.Snapshots = cache
	request := reqcache.With(ctx)
	before, err := engine.Evaluate(request, p, "data.left[0].label", map[string]dashboard.Query{"left": {From: "view:left"}}, nil)
	if err != nil || before.Value != "Example-left" {
		t.Fatalf("before edit: %v, %v", before.Value, err)
	}
	built := false
	_, err = reqcache.Do[any](request, "view-chain|"+p.UserID+"|0|view:middle|null", func() (any, error) {
		built = true
		return nil, nil
	})
	if err != nil || built {
		t.Fatalf("nested non-leaf was not cached: built=%t, %v", built, err)
	}
	if _, err = engine.Evaluate(request, p, "1", map[string]dashboard.Query{"deep": {From: "view:deep-0"}}, nil); err == nil || !strings.Contains(err.Error(), "nest deeper") {
		t.Fatalf("cached view bypassed nesting limit: %v", err)
	}
	cache.Changed(p.UserID, false)
	_, err = db.Collection("views").UpdateOne(ctx, bson.M{"user_id": p.UserID, "id": "leaf"}, bson.M{"$set": bson.M{"query": dashboard.Query{From: "postings", Derive: map[string]string{"payee": "'Changed'"}}}})
	cache.Changed(p.UserID, true)
	if err != nil {
		t.Fatal(err)
	}
	after, err := engine.Evaluate(request, p, "data.right[0].label", map[string]dashboard.Query{"right": {From: "view:right"}}, nil)
	if err != nil || after.Value != "Changed-right" {
		t.Fatalf("stale leaf after edit: %v, %v", after.Value, err)
	}
	if _, err = engine.Evaluate(ctx, other, "size(data.left)", map[string]dashboard.Query{"left": {From: "view:left"}}, nil); err == nil {
		t.Fatal("other owner read view")
	}
	cycleCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	_, err = engine.Evaluate(cycleCtx, p, "1", map[string]dashboard.Query{"a": {From: "view:cycle-a"}, "b": {From: "view:cycle-b"}}, nil)
	if err == nil || cycleCtx.Err() != nil {
		t.Fatalf("cycle must fail without deadlock: %v", err)
	}
}
