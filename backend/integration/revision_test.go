package integration

import (
	"context"
	"errors"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
)

func TestRevisionTracksPartialWritesAndIsolation(t *testing.T) {
	ctx := context.Background()
	db := pgtest.New(t)
	if _, err := db.Exec(ctx, `CREATE TABLE synthetic(id text PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	other := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAW"}
	s := audit.NewService(audit.NewPostgresRepository(pgtest.New(t)))
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
		_, err := db.Exec(ctx, `INSERT INTO synthetic(id) VALUES ('written')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	partial := errors.New("after partial write")
	if err := s.Mutation(ctx, p, func(ctx context.Context) error {
		if _, err := db.Exec(ctx, `INSERT INTO synthetic(id) VALUES ('partial')`); err != nil {
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
