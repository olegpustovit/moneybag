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

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestQuoteCacheLoadsRequestedPairsOnce(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	var reads atomic.Int64
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "find" {
			reads.Add(1)
		}
	}}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_quotecache")
	if err = db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	_, err = db.Collection("prices").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "pair", Value: 1}, {Key: "at", Value: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	other := p
	other.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	docs := []any{
		rates.Quote{ID: "a", UserID: p.UserID, Pair: "EUR/USD", At: "2026-01-01T00:00:00Z", Value: ledger.MustDecimal("1")},
		rates.Quote{ID: "b", UserID: p.UserID, Pair: "EUR/USD", At: "2026-02-01T00:00:00Z", Value: ledger.MustDecimal("2")},
		rates.Quote{ID: "c", UserID: p.UserID, Pair: "EUR/USD", At: "2026-03-01T00:00:00Z", Value: ledger.MustDecimal("3"), Archived: true},
		rates.Quote{ID: "d", UserID: p.UserID, Pair: "GBP/EUR", At: "2026-01-01T00:00:00Z", Value: ledger.MustDecimal("2")},
		rates.Quote{ID: "e", UserID: other.UserID, Pair: "EUR/USD", At: "2026-01-01T00:00:00Z", Value: ledger.MustDecimal("9")},
	}
	for i := 0; i < 30; i++ {
		docs = append(docs, rates.Quote{ID: fmt.Sprintf("extra-%d", i), UserID: p.UserID, Pair: fmt.Sprintf("C%02d/USD", i), At: "2026-01-01T00:00:00Z", Value: ledger.MustDecimal("4")})
	}
	if _, err = db.Collection("prices").InsertMany(ctx, docs); err != nil {
		t.Fatal(err)
	}
	service := rates.NewService(rates.NewMongoRepository(db), nil)
	cached := rates.WithQuoteCache(ctx)
	reads.Store(0)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, e := service.Get(cached, p, []string{"EUR/USD"}, "2026-12-01T00:00:00Z")
			if e != nil {
				errs <- e
			} else if len(q) != 1 || q[0].ID != "b" {
				errs <- fmt.Errorf("wrong quote: %v", q)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("same pair used %d queries", n)
	}
	q, err := service.Get(cached, p, []string{"EUR/USD"}, "2026-01-15T00:00:00Z")
	if err != nil || q[0].ID != "a" || reads.Load() != 1 {
		t.Fatalf("historical: %v %v %d", q, err, reads.Load())
	}
	inverse, err := service.Get(cached, p, []string{"EUR/GBP"}, "2026-12-01T00:00:00Z")
	if err != nil || inverse[0].Value.String() != "0.5" || reads.Load() != 3 {
		t.Fatalf("inverse: %v %v %d", inverse, err, reads.Load())
	}
	theirs, err := service.Get(cached, other, []string{"EUR/USD"}, "2026-12-01T00:00:00Z")
	if err != nil || theirs[0].ID != "e" || reads.Load() != 4 {
		t.Fatalf("user isolation: %v %v %d", theirs, err, reads.Load())
	}
	_, err = service.Get(cached, p, []string{"EUR/GBP"}, "2025-01-01T00:00:00Z")
	if err == nil || reads.Load() != 4 {
		t.Fatalf("missing historical quote: %v %d", err, reads.Load())
	}
}
