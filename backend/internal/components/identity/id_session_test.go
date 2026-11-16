package identity

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestIdentitySessionsArePurposeBoundAndPreserveLegacyRollback(t *testing.T) {
	pool := pgtest.New(t)
	service := NewService(NewPostgresRepository(pool))
	user := identityFixtureUser(t, pool, "eval-session@example.test", false)
	legacy, legacySession, err := service.StartSession(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	current, idSession, err := service.StartIdentitySession(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	if idSession.Hash == Hash(current) || legacySession.Hash != Hash(legacy) || idSession.CSRF == "" || idSession.ExpiresAt.Sub(legacySession.ExpiresAt) > time.Minute {
		t.Fatal("session purposes/lifetime changed")
	}
	if _, _, err := service.IdentitySession(t.Context(), legacy); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("renamed legacy token accepted in ID mode")
	}
	if _, _, err := service.Session(t.Context(), current); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("ID token accepted in legacy mode")
	}
	if err := service.Logout(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	if err := service.LogoutIdentity(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	if p, _, err := service.IdentitySession(t.Context(), current); err != nil || p.UserID != user.ID || p.Actor.Kind != "human" {
		t.Fatal("wrong-purpose logout revoked ID session")
	}
	if _, _, err := service.Session(t.Context(), legacy); err != nil {
		t.Fatal("wrong-purpose logout revoked rollback session")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET is_admin=true WHERE id=@id`, pgx.NamedArgs{"id": user.ID}); err != nil {
		t.Fatal(err)
	}
	if p, _, err := service.IdentitySession(t.Context(), current); err != nil || !p.Admin {
		t.Fatal("ID session did not use current local permissions")
	}
	if err := service.LogoutIdentity(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.IdentitySession(t.Context(), current); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("ID logout did not revoke its row")
	}
	if _, _, err := service.Session(t.Context(), legacy); err != nil {
		t.Fatal("ID logout revoked legacy rollback session")
	}
}

func TestIdentitySessionRechecksActivityAndExpiresWithExistingSweep(t *testing.T) {
	pool := pgtest.New(t)
	service := NewService(NewPostgresRepository(pool))
	user := identityFixtureUser(t, pool, "eval-active@example.test", false)
	legacy, _, err := service.StartSession(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := service.StartIdentitySession(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetDisabled(t.Context(), user.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.StartIdentitySession(t.Context(), user); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("stale user record started a disabled ID session")
	}
	if _, _, err := service.IdentitySession(t.Context(), current); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("disabled ID session retained access")
	}
	if _, _, err := service.Session(t.Context(), legacy); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("disabled legacy session retained access")
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE user_id=@id`, pgx.NamedArgs{"id": user.ID}).Scan(&count); err != nil || count != 0 {
		t.Fatal("disable failed to revoke both session modes")
	}
	if err := service.SetDisabled(t.Context(), user.ID, false); err != nil {
		t.Fatal(err)
	}
	current, _, err = service.StartIdentitySession(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE sessions SET expires_at=now()-interval '1 second' WHERE user_id=@id`, pgx.NamedArgs{"id": user.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.IdentitySession(t.Context(), current); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("expired ID session accepted")
	}
}
