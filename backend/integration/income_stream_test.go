package integration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestIncomeReconcileStreamsJournalAndPreservesLimit(t *testing.T) {
	ctx, p, sources, reads := postingsFixture(t)
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	parsed, _ := url.Parse(uri)
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_postings")

	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	service := model.NewService(model.NewMongoRepository(db), tables.NewService(tables.NewMongoRepository(db), nil, expr), nil, expr)
	service.Ledger = sources.Ledger
	reads.Store(0)
	empty, err := service.IncomeReconcile(ctx, p, model.ReconcileInput{From: "2026-01-01", To: "2026-01-31"})
	if err != nil || len(empty) != 0 {
		t.Fatal("empty streams", empty, err)
	}
	if n := reads.Load(); n > 3 {
		t.Fatalf("empty streams needed %d reads", n)
	}
	if _, err = service.IncomeReconcile(ctx, p, model.ReconcileInput{StreamID: "missing", From: "2026-01-01", To: "2026-01-31"}); !errors.Is(err, principal.ErrNotFound) {
		t.Fatal("missing stream", err)
	}
	streams := []model.Income{{ID: "tagged", Currency: "EUR", Frequency: "monthly", PayerMatch: `"synthetic" in row.tags`, LandsIn: "assets:test"}}
	if _, err = db.Collection("income_models").InsertOne(ctx, bson.M{"_id": p.UserID, "streams": streams}); err != nil {
		t.Fatal(err)
	}
	extra := make([]any, 497)
	for i := range extra {
		extra[i] = ledger.Entry{UserID: p.UserID, ID: fmt.Sprintf("extra-%04d", i), Seq: int64(21000 + i), Date: "2026-01-01", Status: "posted", Postings: []ledger.Posting{{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}}}}
	}
	if _, err = db.Collection("journal").InsertMany(ctx, extra); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Collection("entry_tags").InsertMany(ctx, []any{bson.M{"_id": p.UserID + ":extra-0496", "user_id": p.UserID, "entry_id": "extra-0496", "tags": []string{"synthetic"}}, bson.M{"_id": p.UserID + ":entry-00001", "user_id": p.UserID, "entry_id": "entry-00001", "tags": []string{"synthetic"}}}); err != nil {
		t.Fatal(err)
	}
	reads.Store(0)
	out, err := service.IncomeReconcile(ctx, p, model.ReconcileInput{StreamID: "tagged", From: "2026-01-01", To: "2026-01-31"})
	if err != nil || len(out) != 1 || out[0].Actual != 3 || len(out[0].EntryIDs) != 3 || out[0].EntryIDs[0] != "extra-0496" || out[0].EntryIDs[1] != "entry-00001" || out[0].EntryIDs[2] != "entry-00000" {
		t.Fatalf("income result: %+v, %v", out, err)
	}
	if n := reads.Load(); n > 8 {
		t.Fatalf("income reconciliation needed %d read commands", n)
	}
	if _, err = db.Collection("journal").InsertOne(ctx, ledger.Entry{UserID: p.UserID, ID: "over-limit", Seq: 22000, Date: "2026-01-01", Status: "posted"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.IncomeReconcile(ctx, p, model.ReconcileInput{StreamID: "tagged", From: "2026-01-01", To: "2026-01-31"}); err == nil || !strings.Contains(err.Error(), "too many entries") {
		t.Fatal("expected range limit", err)
	}
}
