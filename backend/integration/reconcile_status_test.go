package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestReconcileStatusAggregation(t *testing.T) {
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
		if e.CommandName == "find" || e.CommandName == "aggregate" || e.CommandName == "getMore" {
			reads.Add(1)
		}
	}}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_reconcile")
	if err := db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(ctx); _ = client.Disconnect(ctx) })
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	docs := []any{
		bson.M{"user_id": p.UserID, "account": "a", "date": "2026-01-01", "ok": true},
		bson.M{"user_id": p.UserID, "account": "a", "date": "2026-02-01", "ok": false},
		bson.M{"user_id": p.UserID, "account": "b", "date": "2026-03-01", "ok": false},
		bson.M{"user_id": p.UserID, "account": "b", "date": "2026-04-01", "ok": true, "archived": true},
		bson.M{"user_id": "other", "account": "c", "date": "2026-05-01", "ok": true},
	}
	if _, err := db.Collection("balance_assertions").InsertMany(ctx, docs); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("discrepancies").InsertOne(ctx, bson.M{"_id": "d", "user_id": p.UserID, "assertion_id": "x", "account": "c", "status": "open", "diff": bson.M{"v": bson.Decimal128{}, "c": "EUR"}}); err != nil {
		t.Fatal(err)
	}
	s := reconcile.NewService(reconcile.NewMongoRepository(db), nil, nil)
	reads.Store(0)
	got, err := s.Status(ctx, p, "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"account":"a","last_ok":"2026-01-01","open_discrepancies":[]},{"account":"b","last_ok":"","open_discrepancies":[]},{"account":"c","last_ok":"","open_discrepancies":[{"id":"d","assertion_id":"x","account":"c","diff":{"v":"0","c":"EUR"},"status":"open"}]}]`
	if string(encoded) != want {
		t.Fatalf("status: %s", encoded)
	}
	if reads.Load() != 2 {
		t.Fatalf("read operations: %d", reads.Load())
	}
	got, err = s.Status(ctx, p, "a")
	if err != nil || len(got) != 1 || got[0].LastOK != "2026-01-01" {
		t.Fatalf("account filter: %v, %v", got, err)
	}
	many := make([]any, 150)
	for i := range many {
		many[i] = bson.M{"user_id": p.UserID, "account": fmt.Sprintf("extra-%03d", i), "date": "2026-01-01", "ok": true}
	}
	if _, err := db.Collection("balance_assertions").InsertMany(ctx, many); err != nil {
		t.Fatal(err)
	}
	reads.Store(0)
	got, err = s.Status(ctx, p, "")
	if err != nil || len(got) != 153 || reads.Load() != 2 {
		t.Fatalf("many accounts: %d accounts, %d reads, %v", len(got), reads.Load(), err)
	}
}
