package integration

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestRevisionTracksPartialWritesAndIsolation(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_revision")
	if err := db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(ctx); _ = client.Disconnect(ctx) })
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	other := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAW"}
	s := audit.NewService(audit.NewMongoRepository(db))
	states := []bool{}
	s.OnChange = func(id string, finished bool) {
		if id != p.UserID {
			t.Errorf("wrong owner")
		}
		states = append(states, finished)
	}
	if rev, err := s.Revision(ctx, p); err != nil || rev != 0 {
		t.Fatalf("initial revision: %d, %v", rev, err)
	}
	cached := reqcache.With(ctx)
	if _, err := s.Revision(cached, p); err != nil {
		t.Fatal(err)
	}
	if err := s.Mutation(ctx, p, func(ctx context.Context) error {
		_, err := db.Collection("synthetic").InsertOne(ctx, bson.M{"_id": "written"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	partial := errors.New("after partial write")
	if err := s.Mutation(ctx, p, func(ctx context.Context) error {
		if _, err := db.Collection("synthetic").InsertOne(ctx, bson.M{"_id": "partial"}); err != nil {
			return err
		}
		return partial
	}); !errors.Is(err, partial) {
		t.Fatalf("partial write error: %v", err)
	}
	if rev, err := s.Revision(ctx, p); err != nil || rev != 2 {
		t.Fatalf("revision after writes: %d, %v", rev, err)
	}
	if rev, err := s.Revision(cached, p); err != nil || rev != 0 {
		t.Fatalf("request cache: %d, %v", rev, err)
	}
	if rev, err := s.Revision(ctx, other); err != nil || rev != 0 {
		t.Fatalf("other user revision: %d, %v", rev, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Mutation(cancelled, p, func(ctx context.Context) error { return ctx.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutation: %v", err)
	}
	if rev, err := s.Revision(ctx, p); err != nil || rev != 3 {
		t.Fatalf("revision after cancellation: %d, %v", rev, err)
	}
	if len(states) != 6 || states[0] || !states[1] || states[2] || !states[3] || states[4] || !states[5] {
		t.Fatalf("invalidation boundaries: %v", states)
	}
}
