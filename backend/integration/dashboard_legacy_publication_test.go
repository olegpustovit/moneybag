package integration

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestLegacyDashboardPublicationTime(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	var commands atomic.Int64
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "find" || e.CommandName == "aggregate" || e.CommandName == "getMore" || e.CommandName == "killCursors" {
			commands.Add(1)
		}
	}}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_legacy_publication")
	if err = db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	const user = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	actor := principal.Actor{Kind: "agent", ID: "agent"}
	p := principal.Principal{UserID: user, Scopes: []string{"ledger:read"}, Actor: actor}
	if _, err = db.Collection("dashboards").InsertOne(ctx, bson.M{"_id": "head", "user_id": user, "id": "sample", "published_version": 1, "published_by": actor}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Collection("dashboard_versions").InsertOne(ctx, bson.M{"_id": "v1", "user_id": user, "dashboard_id": "sample", "version": 1, "spec": bson.M{"id": "sample", "title": "Sample", "version": 1}, "author": actor}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	add := func(id string, seq int, owner, coll, status string, version int, who principal.Actor, when time.Time) {
		t.Helper()
		target := audit.Target{Collection: coll, ID: "sample"}
		_, err := db.Collection("events").InsertOne(ctx, audit.Event{ID: id, UserID: owner, Seq: int64(seq), At: when, Actor: who, Tool: "dashboard_publish", Target: target})
		if err != nil {
			t.Fatal(err)
		}
		if status != "missing" {
			payload, _ := json.Marshal(map[string]int{"published_version": version})
			_, err = db.Collection("audit_outcomes").InsertOne(ctx, audit.Outcome{ID: id, UserID: owner, Status: status, After: payload, At: when})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	add("good", 1, user, "dashboards", "completed", 1, actor, base)
	add("failed", 2, user, "dashboards", "failed", 2, actor, base.Add(time.Minute))
	add("interrupted", 3, user, "dashboards", "missing", 2, actor, base.Add(2*time.Minute))
	add("other-target", 4, user, "accounts", "completed", 1, actor, base.Add(3*time.Minute))
	add("other-user", 5, "01ARZ3NDEKTSV4RRFFQ69G5FAW", "dashboards", "completed", 1, actor, base.Add(4*time.Minute))
	service := dashboard.NewService(dashboard.NewMongoRepository(db), nil, audit.NewService(audit.NewMongoRepository(db)))
	commands.Store(0)
	got, err := service.Get(ctx, p, "sample", "published")
	if err != nil || got.PublishedAt == nil || !got.PublishedAt.Equal(base) {
		t.Fatalf("legacy time: %v, %v", got.PublishedAt, err)
	}
	if n := commands.Load(); n != 3 {
		t.Fatalf("legacy lookup commands: %d", n)
	}
	add("different-version", 6, user, "dashboards", "completed", 2, actor, base.Add(5*time.Minute))
	got, err = service.Get(ctx, p, "sample", "published")
	if err != nil || got.PublishedAt != nil {
		t.Fatalf("mismatched version: %v, %v", got.PublishedAt, err)
	}
	add("different-actor", 7, user, "dashboards", "completed", 1, principal.Actor{Kind: "agent", ID: "other"}, base.Add(6*time.Minute))
	got, err = service.Get(ctx, p, "sample", "published")
	if err != nil || got.PublishedAt != nil {
		t.Fatalf("mismatched publisher: %v, %v", got.PublishedAt, err)
	}
	saved := base.Add(7 * time.Minute)
	if _, err = db.Collection("dashboards").UpdateOne(ctx, bson.M{"_id": "head"}, bson.M{"$set": bson.M{"published_at": saved}}); err != nil {
		t.Fatal(err)
	}
	commands.Store(0)
	got, err = service.Get(ctx, p, "sample", "published")
	if err != nil || got.PublishedAt == nil || !got.PublishedAt.Equal(saved) {
		t.Fatalf("saved time: %v, %v", got.PublishedAt, err)
	}
	if n := commands.Load(); n != 2 {
		t.Fatalf("saved time lookup commands: %d", n)
	}
}
