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

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type testValuer struct{}

func (testValuer) Convert(_ context.Context, _ principal.Principal, a ledger.Amount, target, _ string) (ledger.Amount, error) {
	return ledger.Amount{Value: a.Value, Commodity: target}, nil
}

func postingsFixture(tb testing.TB) (context.Context, principal.Principal, *dashboard.Sources, *atomic.Int64) {
	tb.Helper()
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		tb.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		tb.Fatal("dedicated test database required")
	}
	dbName := strings.TrimPrefix(parsed.Path, "/") + "_postings"
	var reads atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "aggregate" || e.CommandName == "find" || e.CommandName == "getMore" {
			reads.Add(1)
		}
	}}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(monitor))
	if err != nil {
		tb.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	tb.Cleanup(cancel)
	db := client.Database(dbName)
	if err := db.Drop(ctx); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	repo := ledger.NewMongoRepository(db)
	if err := repo.Init(ctx, p); err != nil {
		tb.Fatal(err)
	}
	if _, err := db.Collection("accounts").InsertOne(ctx, ledger.Account{Key: p.UserID + ":assets:test", UserID: p.UserID, ID: "assets:test", Type: "asset", Class: "bank", Name: "Test", Commodities: []string{"EUR"}}); err != nil {
		tb.Fatal(err)
	}
	docs := make([]any, 20000)
	for i := range docs {
		docs[i] = ledger.Entry{UserID: p.UserID, ID: fmt.Sprintf("entry-%05d", i), Seq: int64(i + 1), Date: "2026-01-01", Kind: "entry", Status: "posted", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}}}}
	}
	if _, err := db.Collection("journal").InsertMany(ctx, docs); err != nil {
		tb.Fatal(err)
	}
	reversals := []any{
		ledger.Entry{UserID: p.UserID, ID: "rev-1", Seq: 20001, Date: "2026-01-01", Kind: "reversal", Status: "posted", Links: []ledger.Link{{Rel: "reverses", ID: "entry-00000"}}},
		ledger.Entry{UserID: p.UserID, ID: "rev-2", Seq: 20002, Date: "2026-01-01", Kind: "reversal", Status: "posted", Links: []ledger.Link{{Rel: "reverses", ID: "rev-1"}}},
		ledger.Entry{UserID: p.UserID, ID: "rev-3", Seq: 20003, Date: "2026-01-01", Kind: "reversal", Status: "posted", Links: []ledger.Link{{Rel: "reverses", ID: "entry-00001"}}},
		ledger.Entry{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", ID: "other-user", Seq: 1, Date: "2026-01-01", Kind: "entry", Status: "posted", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}}}},
	}
	if _, err := db.Collection("journal").InsertMany(ctx, reversals); err != nil {
		tb.Fatal(err)
	}
	if _, err := db.Collection("entry_tags").InsertOne(ctx, bson.M{"_id": p.UserID + ":entry-00000", "user_id": p.UserID, "entry_id": "entry-00000", "tags": []string{"synthetic"}}); err != nil {
		tb.Fatal(err)
	}
	sources := &dashboard.Sources{Ledger: ledger.NewService(repo, nil), Valuer: testValuer{}}
	return ctx, p, sources, &reads
}

func TestPostingsStreamLargeJournal(t *testing.T) {
	ctx, p, sources, reads := postingsFixture(t)
	reads.Store(0)
	started := time.Now()
	rows, err := sources.Rows(reqcache.With(ctx), p, "postings")
	if err != nil || len(rows) != 19999 {
		t.Fatalf("postings: %d rows, %v", len(rows), err)
	}
	if rows[0]["seq"] != float64(20000) || rows[len(rows)-1]["seq"] != float64(1) {
		t.Fatal("journal sequence order changed")
	}
	if n := reads.Load(); n > 4 {
		t.Fatalf("20,000 entries required %d reads; want at most three journal reads and one accounts read", n)
	}
	seen := false
	for _, row := range rows {
		if row["entry"] == "entry-00001" || row["entry"] == "other-user" {
			t.Fatalf("unexpected entry: %v", row["entry"])
		}
		if row["entry"] == "entry-00000" {
			seen = true
			if tags := row["tags"].([]any); len(tags) != 1 || tags[0] != "synthetic" {
				t.Fatalf("tag overlay: %v", tags)
			}
		}
	}
	if !seen {
		t.Fatal("even reversal chain should retain original")
	}
	t.Logf("20,000 entries in %s; %d read commands including accounts and cursor batches", time.Since(started), reads.Load())
}

func BenchmarkPostingsSource(b *testing.B) {
	ctx, p, sources, _ := postingsFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := sources.Rows(reqcache.With(ctx), p, "postings")
		if err != nil || len(rows) != 19999 {
			b.Fatalf("postings: %d rows, %v", len(rows), err)
		}
	}
}
