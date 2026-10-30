package integration

import (
	"context"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestBearerOneRead(t *testing.T) {
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
		if e.CommandName == "find" || e.CommandName == "aggregate" || e.CommandName == "getMore" || e.CommandName == "killCursors" {
			reads.Add(1)
		}
	}}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_bearer")
	if err := db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	const user = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const resource = "https://example.test/mcp"
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
	if _, err := db.Collection("users").InsertOne(ctx, bson.M{"_id": user}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("oauth_grants").InsertOne(ctx, bson.M{"_id": "grant", "user_id": user, "client_id": "agent", "resource": resource, "scopes": bson.A{"ledger:read"}, "revoked": false, "expires_at": future}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("oauth_tokens").InsertOne(ctx, bson.M{"_id": identity.Hash("token"), "grant_id": "grant", "access_expires_at": future}); err != nil {
		t.Fatal(err)
	}
	service := oauth.NewService(oauth.NewMongoRepository(db), identity.NewService(identity.NewMongoRepository(db)), "https://example.test")
	check := func(raw, audience string, want error) principal.Principal {
		t.Helper()
		reads.Store(0)
		p, err := service.Bearer(ctx, raw, audience)
		if !errors.Is(err, want) {
			t.Fatalf("Bearer(%q, %q): %v, want %v", raw, audience, err, want)
		}
		if n := reads.Load(); n != 1 {
			t.Fatalf("Bearer read commands: %d, want 1", n)
		}
		return p
	}
	p := check("token", resource, nil)
	if p.UserID != user || p.Actor.Kind != "agent" || p.Actor.ID != "agent" || !reflect.DeepEqual(p.Scopes, []string{"ledger:read"}) {
		t.Fatalf("principal: %+v", p)
	}
	check("wrong", resource, principal.ErrForbidden)
	check("token", resource+"/readonly", principal.ErrForbidden)
	update := func(collection string, filter bson.M, update bson.M) {
		t.Helper()
		if _, err := db.Collection(collection).UpdateOne(ctx, filter, update); err != nil {
			t.Fatal(err)
		}
	}
	grant := bson.M{"_id": "grant"}
	tok := bson.M{"_id": identity.Hash("token")}
	update("oauth_grants", grant, bson.M{"$set": bson.M{"revoked": true}})
	check("token", resource, principal.ErrForbidden)
	update("oauth_grants", grant, bson.M{"$set": bson.M{"revoked": false, "expires_at": past}})
	check("token", resource, principal.ErrForbidden)
	update("oauth_grants", grant, bson.M{"$set": bson.M{"expires_at": future}, "$unset": bson.M{"revoked": ""}})
	check("token", resource, principal.ErrForbidden)
	update("oauth_grants", grant, bson.M{"$set": bson.M{"revoked": false}})
	update("oauth_tokens", tok, bson.M{"$set": bson.M{"access_expires_at": past}})
	check("token", resource, principal.ErrForbidden)
	update("oauth_tokens", tok, bson.M{"$set": bson.M{"access_expires_at": future, "grant_id": "missing"}})
	check("token", resource, principal.ErrForbidden)
	update("oauth_tokens", tok, bson.M{"$set": bson.M{"grant_id": "grant"}})
	update("oauth_tokens", tok, bson.M{"$set": bson.M{"grant_id": bson.A{"grant"}}})
	check("token", resource, principal.ErrForbidden)
	update("oauth_tokens", tok, bson.M{"$set": bson.M{"grant_id": "grant", "access_expires_at": bson.A{future}}})
	check("token", resource, principal.ErrForbidden)
	update("oauth_tokens", tok, bson.M{"$set": bson.M{"access_expires_at": future}})
	update("oauth_tokens", tok, bson.M{"$set": bson.M{"used": bson.A{false}}})
	check("token", resource, principal.ErrForbidden)
	update("oauth_tokens", tok, bson.M{"$unset": bson.M{"used": ""}})
	if _, err := db.Collection("users").DeleteOne(ctx, bson.M{"_id": user}); err != nil {
		t.Fatal(err)
	}
	check("token", resource, principal.ErrNotFound)
	if _, err := db.Collection("users").InsertOne(ctx, bson.M{"_id": user}); err != nil {
		t.Fatal(err)
	}
	check("token", resource, nil)
}
