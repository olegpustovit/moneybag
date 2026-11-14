package integration

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestBearerOneRead(t *testing.T) {
	db := pgtest.New(t)
	ctx := context.Background()
	var reads atomic.Int64
	config := db.Config().Copy()
	config.ConnConfig.Tracer = sqlQueryCounter{reads: &reads}
	traced, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	const user = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const resource = "https://example.test/mcp"
	future, past := time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond), time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	exec := func(query string, args pgx.NamedArgs) {
		t.Helper()
		if _, err := db.Exec(ctx, query, args); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id) VALUES (@user)`, pgx.NamedArgs{"user": user})
	exec(`INSERT INTO oauth_grants(id,user_id,client_id,resource,scopes,revoked,expires_at) VALUES ('grant',@user,'agent',@resource,ARRAY['ledger:read'],false,@future)`, pgx.NamedArgs{"user": user, "resource": resource, "future": future})
	exec(`INSERT INTO oauth_tokens(id,grant_id,access_expires_at) VALUES (@hash,'grant',@future)`, pgx.NamedArgs{"hash": identity.Hash("token"), "future": future})
	service := oauth.NewService(oauth.NewPostgresRepository(traced), identity.NewService(identity.NewPostgresRepository(db)), "https://example.test")
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
	if err := service.SetSuspended(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	check("token", resource, oauth.ErrAgentsSuspended)
	check("wrong", resource, principal.ErrForbidden)
	if err := service.SetSuspended(ctx, user, false); err != nil {
		t.Fatal(err)
	}
	check("token", resource, nil)
	exec(`UPDATE oauth_grants SET revoked=true WHERE id='grant'`, nil)
	check("token", resource, principal.ErrForbidden)
	exec(`UPDATE oauth_grants SET revoked=false,expires_at=@past WHERE id='grant'`, pgx.NamedArgs{"past": past})
	check("token", resource, principal.ErrForbidden)
	exec(`UPDATE oauth_grants SET expires_at=@future WHERE id='grant'`, pgx.NamedArgs{"future": future})
	exec(`UPDATE oauth_tokens SET access_expires_at=@past WHERE id=@hash`, pgx.NamedArgs{"past": past, "hash": identity.Hash("token")})
	check("token", resource, principal.ErrForbidden)
	exec(`UPDATE oauth_tokens SET access_expires_at=@future,grant_id='missing' WHERE id=@hash`, pgx.NamedArgs{"future": future, "hash": identity.Hash("token")})
	check("token", resource, principal.ErrForbidden)
	exec(`UPDATE oauth_tokens SET grant_id='grant' WHERE id=@hash`, pgx.NamedArgs{"hash": identity.Hash("token")})
	// The old BSON malformed-field fixtures are refused at the SQL write boundary.
	for _, tc := range []struct {
		query string
		args  pgx.NamedArgs
	}{
		{`UPDATE oauth_grants SET revoked=NULL WHERE id='grant'`, nil},
		{`UPDATE oauth_tokens SET grant_id=@value WHERE id=@hash`, pgx.NamedArgs{"value": []string{"grant"}, "hash": identity.Hash("token")}},
		{`UPDATE oauth_tokens SET access_expires_at=@value WHERE id=@hash`, pgx.NamedArgs{"value": []time.Time{future}, "hash": identity.Hash("token")}},
		{`UPDATE oauth_tokens SET used=@value WHERE id=@hash`, pgx.NamedArgs{"value": []bool{false}, "hash": identity.Hash("token")}},
	} {
		if _, err := db.Exec(ctx, tc.query, tc.args); err == nil {
			t.Fatalf("malformed authentication fixture accepted: %s", tc.query)
		}
		check("token", resource, nil)
	}
	exec(`DELETE FROM users WHERE id=@user`, pgx.NamedArgs{"user": user})
	check("token", resource, principal.ErrNotFound)
	exec(`INSERT INTO users(id) VALUES (@user)`, pgx.NamedArgs{"user": user})
	check("token", resource, nil)
}
