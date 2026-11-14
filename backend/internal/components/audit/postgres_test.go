package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestPostgresPreservesPayloadBytesAndChain(t *testing.T) {
	pool := pgtest.New(t)
	s := NewService(NewPostgresRepository(pool))
	ctx := context.Background()
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}, Actor: principal.Actor{Kind: "human", ID: "owner"}}
	payload := json.RawMessage(`{"z":"1.00","a":9007199254740993}`)
	id, err := s.Write(ctx, p, "synthetic", Target{Collection: "synthetic", ID: "one"}, "", payload, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	event, err := s.Get(ctx, p, id)
	if err != nil || !bytes.Equal(event.Before, payload) || !bytes.Equal(event.After, payload) || event.Hash != Digest(event) {
		t.Fatalf("payload or hash changed: %s %s, %v", event.Before, event.After, err)
	}
	if err := s.Verify(ctx, p); err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestCompleted(ctx, p, "synthetic", event.Target)
	if err != nil || latest == nil || latest.ID != id || latest.Outcome == nil || !bytes.Equal(latest.Before, payload) {
		t.Fatalf("completed event: %+v, %v", latest, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_outcomes SET before=@payload WHERE id=@id`, pgx.NamedArgs{"payload": []byte(`{"z":"1.01"}`), "id": id}); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, p); err == nil {
		t.Fatal("changed outcome bytes passed audit verification")
	}
}
