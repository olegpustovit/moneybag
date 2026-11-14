package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Explicit opt-in: both stores are disposable restored copies after parity writes.
func TestParityAuditChains(t *testing.T) {
	uri, database, target := os.Getenv("MONEYBAG_PARITY_AUDIT_MONGO_URI"), os.Getenv("MONEYBAG_PARITY_AUDIT_MONGO_DB"), os.Getenv("MONEYBAG_PARITY_AUDIT_PG_URL")
	if uri == "" || database == "" || target == "" {
		t.Skip("set the three MONEYBAG_PARITY_AUDIT_* variables for restored-copy chain verification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal("connect source")
	}
	defer client.Disconnect(context.Background())
	db := client.Database(database)
	outcomes, err := db.Collection("audit_outcomes").Find(ctx, bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]audit.Outcome{}
	for outcomes.Next(ctx) {
		var legacy legacyAuditOutcome
		if err = decodeLegacy(outcomes.Current, &legacy); err != nil {
			t.Fatal(err)
		}
		outcome := legacy.value()
		byID[outcome.ID] = outcome
	}
	if err = outcomes.Err(); err != nil {
		t.Fatal(err)
	}
	outcomes.Close(ctx)
	events, err := db.Collection("events").Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "seq", Value: 1}}))
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close(ctx)
	hashes := map[string]string{}
	sequences := map[string]int64{}
	count := 0
	for events.Next(ctx) {
		var legacy legacyAuditEvent
		if err = decodeLegacy(events.Current, &legacy); err != nil {
			t.Fatal(err)
		}
		event := legacy.value()
		if event.PrevHash != hashes[event.UserID] || event.Seq <= sequences[event.UserID] || audit.Digest(event) != event.Hash {
			t.Fatalf("source audit chain invalid at document %d", count)
		}
		outcome, ok := byID[event.ID]
		if !ok || audit.OutcomeDigest(event.Hash, outcome) != outcome.Hash {
			t.Fatalf("source outcome invalid at document %d", count)
		}
		hashes[event.UserID], sequences[event.UserID] = event.Hash, event.Seq
		count++
	}
	if err = events.Err(); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(ctx, target)
	if err != nil {
		t.Fatal("connect target")
	}
	defer pool.Close()
	var targetEvents, targetOutcomes int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM events), (SELECT count(*) FROM audit_outcomes)`).Scan(&targetEvents, &targetOutcomes); err != nil {
		t.Fatal(err)
	}
	if targetEvents != count || targetOutcomes != len(byID) || len(byID) != count {
		t.Fatal("source and target audit event/outcome counts differ")
	}
	service := audit.NewService(audit.NewPostgresRepository(pool))
	for user := range hashes {
		if err = service.Verify(ctx, identity.Principal(identity.User{ID: user})); err != nil {
			t.Fatal("target audit verification failed", err)
		}
	}
	t.Logf("Verified %d source event/outcome digests and all target chains after write parity", count)
}
