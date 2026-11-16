package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/pressly/goose/v3"
)

func identityFixtureUser(t *testing.T, pool *pgxpool.Pool, email string, admin bool) User {
	t.Helper()
	user := User{ID: ulid.Make().String(), Email: email, Name: "Keep local name", Admin: admin, CreatedAt: time.UnixMilli(1234567890123).UTC()}
	if _, err := pool.Exec(t.Context(), insertUserSQL, pgx.NamedArgs{"id": user.ID, "email": user.Email, "name": user.Name, "admin": user.Admin, "at": user.CreatedAt, "disabled": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), insertCredentialSQL, pgx.NamedArgs{"id": user.ID, "hash": []byte("keep-private-hash"), "salt": []byte("keep-private-salt")}); err != nil {
		t.Fatal(err)
	}
	stored, err := NewService(NewPostgresRepository(pool)).User(t.Context(), user.ID)
	if err != nil || !stored.CreatedAt.Equal(user.CreatedAt) {
		t.Fatal("fixture user did not persist exactly")
	}
	return stored
}

func identityFixtureInvite(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	token := Random()
	const sql = `INSERT INTO invites(id,email,created_by,expires_at) VALUES(@id,@email,'fixture-admin',@expires)`
	if _, err := pool.Exec(t.Context(), sql, pgx.NamedArgs{"id": Hash(token), "email": email, "expires": time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	return token
}

func identitySnapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var snapshot string
	const sql = `SELECT jsonb_build_object(
 'users',(SELECT jsonb_agg(to_jsonb(u) ORDER BY id) FROM users u),
 'credentials',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM credentials c),
 'journal',(SELECT jsonb_agg(to_jsonb(j) ORDER BY id) FROM journal j),
 'grants',(SELECT jsonb_agg(to_jsonb(g) ORDER BY id) FROM oauth_grants g))::text`
	if err := pool.QueryRow(t.Context(), sql).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestIdentityLinkPreservesLocalAccountsFinanceAndAgents(t *testing.T) {
	pool := pgtest.New(t)
	service := NewService(NewPostgresRepository(pool))
	original := identityFixtureUser(t, pool, "eval-existing@example.test", true)
	other := identityFixtureUser(t, pool, "eval-other@example.test", false)
	const entrySQL = `INSERT INTO journal(id,user_id,seq,postings,actor) VALUES('fixture-entry',@user,1,@postings::jsonb,@actor::jsonb)`
	if _, err := pool.Exec(t.Context(), entrySQL, pgx.NamedArgs{"user": original.ID, "postings": `[{"account":"assets:bank","amount":{"value":"23.400000000000000000001","commodity":"EUR"}}]`, "actor": `{"kind":"human","id":"keep-actor"}`}); err != nil {
		t.Fatal(err)
	}
	const grantSQL = `INSERT INTO oauth_grants(id,user_id,client_id,client_name,resource,scopes,expires_at) VALUES('fixture-grant',@user,'fixture-agent','Keep agent','fixture-resource',ARRAY['ledger:read'],@expires)`
	if _, err := pool.Exec(t.Context(), grantSQL, pgx.NamedArgs{"user": other.ID, "expires": time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	before := identitySnapshot(t, pool)
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			user, err := service.LinkIdentity(t.Context(), "id-existing", " EVAL-existing@example.test ")
			if err != nil {
				t.Error(err)
				return
			}
			if !reflect.DeepEqual(user, original) {
				t.Error("link changed existing account")
			}
		})
	}
	group.Wait()
	if identitySnapshot(t, pool) != before {
		t.Fatal("link changed credentials, profile, finance ownership/precision or agent grants")
	}
	user, err := service.LinkIdentity(t.Context(), "id-existing", other.Email)
	if err != nil || !reflect.DeepEqual(user, original) {
		t.Fatal("changed verified email moved a linked subject")
	}
	if _, err := service.LinkIdentity(t.Context(), "another-subject", original.Email); !errors.Is(err, principal.ErrConflict) {
		t.Fatal("same local user linked to another subject")
	}
	if _, err := service.LinkIdentity(t.Context(), "uninvited-subject", "eval-uninvited@example.test"); !errors.Is(err, ErrInvitationRequired) {
		t.Fatal("uninvited account accepted")
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM user_identities`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate/unknown identity persisted")
	}
	users, err := service.Users(t.Context())
	if err != nil || len(users) != 2 {
		t.Fatal("legacy SELECT * users decoder broke after migration")
	}
	if p := Principal(user); p.Actor.Kind != "human" || p.UserID != original.ID || !p.Admin {
		t.Fatal("local principal changed")
	}
}

func TestIdentityLinkKeepsLegacyPasswordSessionAndDisabledRules(t *testing.T) {
	pool := pgtest.New(t)
	service := NewService(NewPostgresRepository(pool))
	const password = "private-test-password"
	owner, err := service.Setup(t.Context(), "eval-owner@example.test", "Owner", password)
	if err != nil {
		t.Fatal(err)
	}
	var beforeHash, beforeSalt []byte
	if err := pool.QueryRow(t.Context(), `SELECT hash,salt FROM credentials WHERE id=@id`, pgx.NamedArgs{"id": owner.ID}).Scan(&beforeHash, &beforeSalt); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LinkIdentity(t.Context(), "owner-subject", owner.Email); err != nil {
		t.Fatal(err)
	}
	logged, err := service.Login(t.Context(), owner.Email, password)
	if err != nil || logged.ID != owner.ID {
		t.Fatal("linked account lost legacy password rollback")
	}
	raw, _, err := service.StartSession(t.Context(), logged)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetDisabled(t.Context(), owner.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LinkIdentity(t.Context(), "owner-subject", owner.Email); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("linked disabled account accepted")
	}
	if _, _, err := service.Session(t.Context(), raw); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("disabled account retained session")
	}
	if err := service.SetDisabled(t.Context(), owner.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LinkIdentity(t.Context(), "owner-subject", owner.Email); err != nil {
		t.Fatal(err)
	}
	var afterHash, afterSalt []byte
	if err := pool.QueryRow(t.Context(), `SELECT hash,salt FROM credentials WHERE id=@id`, pgx.NamedArgs{"id": owner.ID}).Scan(&afterHash, &afterSalt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeHash, afterHash) || !bytes.Equal(beforeSalt, afterSalt) {
		t.Fatal("Identity changed legacy password material")
	}
	disabled := identityFixtureUser(t, pool, "eval-disabled@example.test", false)
	if err := service.SetDisabled(t.Context(), disabled.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LinkIdentity(t.Context(), "disabled-subject", disabled.Email); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("unlinked disabled account accepted")
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM user_identities WHERE user_id=@id`, pgx.NamedArgs{"id": disabled.ID}).Scan(&count); err != nil || count != 0 {
		t.Fatal("disabled account acquired a subject")
	}
}

func TestIdentityRegistrationIsInvitedNonAdminAndHasNoPassword(t *testing.T) {
	pool := pgtest.New(t)
	service := NewService(NewPostgresRepository(pool))
	email := "eval-new@example.test"
	if _, _, err := service.RegisterIdentity(t.Context(), "new-subject", email, "New", ""); !errors.Is(err, ErrInvitationRequired) {
		t.Fatal("ID created first owner without invitation")
	}
	token := identityFixtureInvite(t, pool, email)
	user, created, err := service.RegisterIdentity(t.Context(), "new-subject", email, "New", token)
	if err != nil || !created || user.Admin || user.Disabled || user.Email != email {
		t.Fatalf("invited ID registration: created=%t err=%v", created, err)
	}
	var credentials int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM credentials WHERE id=@id`, pgx.NamedArgs{"id": user.ID}).Scan(&credentials); err != nil || credentials != 0 {
		t.Fatal("ID account manufactured a local password")
	}
	linked, err := service.LinkIdentity(t.Context(), "new-subject", email)
	if err != nil || linked.ID != user.ID {
		t.Fatal("new ID user not bound")
	}
	if _, _, err := service.RegisterIdentity(t.Context(), "new-subject", email, "Replacement", token); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("consumed invite reused")
	}
	if needed, err := service.NeedsSetup(t.Context()); err != nil || needed {
		t.Fatal("first invited member allowed owner bootstrap")
	}
}

func TestIdentityRegistrationPreservesExistingUserAndRollsBackFailures(t *testing.T) {
	pool := pgtest.New(t)
	service := NewService(NewPostgresRepository(pool))
	original := identityFixtureUser(t, pool, "eval-existing@example.test", false)
	token := identityFixtureInvite(t, pool, original.Email)
	before := identitySnapshot(t, pool)
	user, created, err := service.RegisterIdentity(t.Context(), "existing-subject", original.Email, "Do not replace", token)
	if err != nil || created || !reflect.DeepEqual(user, original) || identitySnapshot(t, pool) != before {
		t.Fatal("invited existing account was changed")
	}
	// Fail after account insertion; the invitation and new account must both roll back.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE user_identities ADD CONSTRAINT fixture_reject_subject CHECK (subject <> 'rejected-subject')`); err != nil {
		t.Fatal(err)
	}
	rejectedEmail := "eval-rejected@example.test"
	rejectedToken := identityFixtureInvite(t, pool, rejectedEmail)
	if _, _, err := service.RegisterIdentity(t.Context(), "rejected-subject", rejectedEmail, "Rejected", rejectedToken); err == nil {
		t.Fatal("injected persistence failure ignored")
	}
	var count int
	var used *time.Time
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE email=@email`, pgx.NamedArgs{"email": rejectedEmail}).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial registration left an account")
	}
	if err := pool.QueryRow(t.Context(), `SELECT used_at FROM invites WHERE id=@id`, pgx.NamedArgs{"id": Hash(rejectedToken)}).Scan(&used); err != nil || used != nil {
		t.Fatal("failed registration consumed invitation")
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE user_identities DROP CONSTRAINT fixture_reject_subject`); err != nil {
		t.Fatal(err)
	}
	if _, created, err := service.RegisterIdentity(t.Context(), "accepted-subject", rejectedEmail, "Accepted", rejectedToken); err != nil || !created {
		t.Fatal("rolled-back invitation could not be retried")
	}
}

func TestIdentityRegistrationRejectsInvalidOrForeignInvitations(t *testing.T) {
	for _, kind := range []string{"missing", "expired", "used", "foreign", "disabled", "conflict"} {
		t.Run(kind, func(t *testing.T) {
			pool := pgtest.New(t)
			service := NewService(NewPostgresRepository(pool))
			email := "eval-invited@example.test"
			token := identityFixtureInvite(t, pool, email)
			switch kind {
			case "missing":
				token = "not-a-real-token"
			case "expired":
				_, err := pool.Exec(t.Context(), `UPDATE invites SET expires_at=now()-interval '1 second'`)
				if err != nil {
					t.Fatal(err)
				}
			case "used":
				_, err := pool.Exec(t.Context(), `UPDATE invites SET used_at=now()`)
				if err != nil {
					t.Fatal(err)
				}
			case "foreign":
				email = "eval-other@example.test"
			case "disabled":
				user := identityFixtureUser(t, pool, email, false)
				if err := service.SetDisabled(t.Context(), user.ID, true); err != nil {
					t.Fatal(err)
				}
			case "conflict":
				user := identityFixtureUser(t, pool, email, false)
				if _, err := service.LinkIdentity(t.Context(), "different-subject", user.Email); err != nil {
					t.Fatal(err)
				}
			}
			before := identitySnapshot(t, pool)
			if _, _, err := service.RegisterIdentity(t.Context(), "new-subject", email, "New", token); err == nil {
				t.Fatal("invalid invitation accepted")
			}
			if identitySnapshot(t, pool) != before {
				t.Fatal("rejected invitation changed account data")
			}
			if kind != "used" {
				var count int
				if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM invites WHERE used_at IS NOT NULL`).Scan(&count); err != nil || count != 0 {
					t.Fatal("rejected invitation consumed")
				}
			}
		})
	}
}

func TestConcurrentIdentityRegistrationKeepsOneOwner(t *testing.T) {
	for _, kind := range []string{"same-invite", "same-email", "same-subject"} {
		t.Run(kind, func(t *testing.T) {
			pool := pgtest.New(t)
			service := NewService(NewPostgresRepository(pool))
			email1, email2 := "eval-first@example.test", "eval-first@example.test"
			subject1, subject2 := "subject-first", "subject-second"
			token1 := identityFixtureInvite(t, pool, email1)
			token2 := token1
			if kind == "same-invite" {
				subject2 = subject1
			} else {
				if kind == "same-subject" {
					email2 = "eval-second@example.test"
					subject2 = subject1
				}
				token2 = identityFixtureInvite(t, pool, email2)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			created := make(chan bool, 2)
			for _, input := range []struct{ subject, email, token string }{{subject1, email1, token1}, {subject2, email2, token2}} {
				go func() {
					<-start
					_, newUser, err := service.RegisterIdentity(t.Context(), input.subject, input.email, "New", input.token)
					created <- newUser
					results <- err
				}()
			}
			close(start)
			successes, newUsers := 0, 0
			for range 2 {
				if err := <-results; err == nil {
					successes++
				} else if !errors.Is(err, principal.ErrConflict) && !errors.Is(err, principal.ErrForbidden) {
					t.Error(err)
				}
				if <-created {
					newUsers++
				}
			}
			if successes != 1 || newUsers != 1 {
				t.Fatalf("competing registration: successes=%d created=%d", successes, newUsers)
			}
			for _, query := range []string{`SELECT count(*) FROM users`, `SELECT count(*) FROM user_identities`, `SELECT count(*) FROM invites WHERE used_at IS NOT NULL`} {
				var count int
				if err := pool.QueryRow(t.Context(), query).Scan(&count); err != nil || count != 1 {
					t.Fatal("concurrent registration left a partial or duplicate owner")
				}
			}
		})
	}
}

func TestIdentityInputsValidateBeforePersistence(t *testing.T) {
	service := NewService(nil)
	for _, input := range []struct{ subject, email string }{{"", "a@example.test"}, {strings.Repeat("s", 256), "a@example.test"}, {"nul\x00subject", "a@example.test"}, {"subject", "bad email"}} {
		if _, err := service.LinkIdentity(context.Background(), input.subject, input.email); !errors.Is(err, ErrIdentityInvalid) {
			t.Fatal("invalid verified identity reached persistence")
		}
	}
	if _, _, err := service.RegisterIdentity(context.Background(), "subject", "a@example.test", "Name", ""); !errors.Is(err, ErrInvitationRequired) {
		t.Fatal("missing invite reached persistence")
	}
}

func TestIdentityMigrationAllowsLegacyStartupAndStrictUserQueries(t *testing.T) {
	pool := pgtest.New(t)
	service := NewService(NewPostgresRepository(pool))
	user := identityFixtureUser(t, pool, "eval-legacy@example.test", true)
	initial, err := os.ReadFile("../../infrastructure/postgres/migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	legacy, err := goose.NewProvider(goose.DialectPostgres, db, fstest.MapFS{"0001_init.sql": &fstest.MapFile{Data: initial}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Up(t.Context()); err != nil {
		t.Fatal("legacy migration provider rejected additive migration", err)
	}
	got, err := service.User(t.Context(), user.ID)
	if err != nil || !reflect.DeepEqual(got, user) {
		t.Fatal("legacy strict user decoder rejected new schema")
	}
}

func TestIdentityMappingConstraintsStayPrivateAndFollowUserDeletion(t *testing.T) {
	pool := pgtest.New(t)
	service := NewService(NewPostgresRepository(pool))
	first := identityFixtureUser(t, pool, "eval-first@example.test", false)
	second := identityFixtureUser(t, pool, "eval-second@example.test", false)
	const subject = "private-verified-subject"
	linked, err := service.LinkIdentity(t.Context(), subject, first.Email)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(linked)
	if err != nil || strings.Contains(string(encoded), subject) {
		t.Fatal("private subject leaked through User JSON")
	}
	for _, value := range []struct{ subject, user string }{{subject, second.ID}, {"another-subject", first.ID}, {"foreign-subject", ulid.Make().String()}, {"", second.ID}, {strings.Repeat("s", 256), second.ID}} {
		if _, err := pool.Exec(t.Context(), insertIdentitySQL, pgx.NamedArgs{"subject": value.subject, "user": value.user}); err == nil {
			t.Fatal("identity uniqueness, foreign-key or subject constraint missing")
		}
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM users WHERE id=@id`, pgx.NamedArgs{"id": first.ID}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM user_identities`).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted user left a reusable subject mapping")
	}
}
