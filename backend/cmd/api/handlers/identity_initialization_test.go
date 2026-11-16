package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	idclient "github.com/olegpustovit/identity/idclient-go"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
)

func TestIdentityInitializationRetriesOnlyNewAccountsAndBlocksPrematureSessions(t *testing.T) {
	pool := pgtest.New(t)
	ids := identity.NewService(identity.NewPostgresRepository(pool))
	owner, err := ids.Setup(t.Context(), "eval-existing@example.test", "Keep local name", "private-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	token, err := ids.Invite(t.Context(), identity.Principal(owner), "eval-new@example.test")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{cfg: Config{IdentityMode: true, PublicURL: "https://moneybag.example.test"}, services: mcpserver.Services{Identity: ids}, ledgerRepo: ledger.NewPostgresRepository(pool)}
	claims := idclient.Claims{Subject: "new-subject", Email: "eval-new@example.test", Name: "New member", EmailVerified: true}
	invalidCtx := context.WithValue(t.Context(), invitationContextKey{}, identityFlowContext{Invitation: token, Invalid: true})
	if _, err := h.resolveIdentity(invalidCtx, claims); err == nil {
		t.Fatal("invalid invitation context reached account resolution")
	}
	if _, err := ids.ByEmail(t.Context(), claims.Email); !errors.Is(err, principal.ErrNotFound) {
		t.Fatal("invalid invitation context created an account")
	}
	inviteCtx := context.WithValue(t.Context(), invitationContextKey{}, identityFlowContext{Invitation: token})
	if _, err := h.resolveIdentity(inviteCtx, claims); err == nil {
		t.Fatal("missing initializer ignored")
	}
	created, err := ids.ByEmail(t.Context(), claims.Email)
	if err != nil || created.Admin {
		t.Fatal("invited user missing or made administrator")
	}
	if pending, err := ids.IdentityNeedsInitialization(t.Context(), created.ID); err != nil || !pending {
		t.Fatal("failed initialization lost pending marker")
	}
	if _, _, err := ids.StartIdentitySession(t.Context(), created); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("pending account received an ID session")
	}
	a := audit.NewService(audit.NewPostgresRepository(pool))
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	tab := tables.NewService(tables.NewPostgresRepository(pool), a, expr)
	h.services.Model = model.NewService(model.NewPostgresRepository(pool), tab, a, expr)
	// Force a failure after assumptions were written but before the base scenario exists.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE scenarios ADD CONSTRAINT fixture_reject_base CHECK(id <> 'base')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.resolveIdentity(t.Context(), claims); err == nil {
		t.Fatal("partial model failure ignored")
	}
	if pending, err := ids.IdentityNeedsInitialization(t.Context(), created.ID); err != nil || !pending {
		t.Fatal("partial initialization marked complete")
	}
	var rows int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM rows WHERE user_id=@user`, pgx.NamedArgs{"user": created.ID}).Scan(&rows); err != nil || rows == 0 {
		t.Fatal("fixture did not reach partial initialization")
	}
	p := identity.Principal(created)
	if _, err := tab.Update(t.Context(), p, tables.UpdateInput{DB: "assumptions", Updates: []tables.CellUpdate{{RowID: "horizon", Column: "unit", Value: "keep-unit"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE scenarios DROP CONSTRAINT fixture_reject_base`); err != nil {
		t.Fatal(err)
	}
	var retries sync.WaitGroup
	for range 8 {
		retries.Go(func() {
			resolved, err := h.resolveIdentity(t.Context(), claims)
			if err != nil || resolved != created.ID {
				t.Error("normal sign-in could not retry partial account", err)
			}
		})
	}
	retries.Wait()
	if pending, err := ids.IdentityNeedsInitialization(t.Context(), created.ID); err != nil || pending {
		t.Fatal("successful initialization not completed")
	}
	data, err := tab.Query(t.Context(), p, tables.QueryInput{DB: "assumptions"})
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, row := range data.Rows {
		if row.ID == "horizon" && row.Cells["unit"] == "keep-unit" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("retry reset an existing assumption")
	}
	// Already existing users never get fresh defaults, even if their model is empty.
	existingClaims := idclient.Claims{Subject: "existing-subject", Email: owner.Email, Name: "Do not replace", EmailVerified: true}
	if resolved, err := h.resolveIdentity(t.Context(), existingClaims); err != nil || resolved != owner.ID {
		t.Fatal("existing account linking failed", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM tables WHERE user_id=@user`, pgx.NamedArgs{"user": owner.ID}).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("existing user's model was initialized during sign-in")
	}
	if current, err := ids.Login(t.Context(), owner.Email, "private-fixture-password"); err != nil || current.ID != owner.ID || current.Name != owner.Name || !current.Admin {
		t.Fatal("existing credentials/profile changed")
	}
	var credentials int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM credentials WHERE id=@user`, pgx.NamedArgs{"user": created.ID}).Scan(&credentials); err != nil || credentials != 0 {
		t.Fatal("new ID account received local credentials")
	}
	sessions := identitySessions{h}
	request := httptest.NewRequest("GET", h.cfg.PublicURL+"/api/auth/id/callback", nil)
	w := httptest.NewRecorder()
	if err := sessions.Create(w, request, created.ID); err != nil {
		t.Fatal("initialized account did not receive session", err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-moneybag_session" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Domain != "" {
		t.Fatal("SDK adapter emitted an unsafe session cookie")
	}
	if p, _, err := ids.IdentitySession(t.Context(), cookies[0].Value); err != nil || p.UserID != created.ID || p.Admin {
		t.Fatal("SDK adapter used the wrong local principal")
	}
	invalid := httptest.NewRecorder()
	badRequest := request.WithContext(context.WithValue(t.Context(), invitationContextKey{}, identityFlowContext{Invalid: true}))
	if err := sessions.Create(invalid, badRequest, created.ID); err == nil || len(invalid.Result().Cookies()) != 0 {
		t.Fatal("invalid invitation context created a session")
	}
	clear := httptest.NewRecorder()
	request.AddCookie(&http.Cookie{Name: "__Host-moneybag_session", Value: cookies[0].Value})
	if err := sessions.Clear(clear, request); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ids.IdentitySession(t.Context(), cookies[0].Value); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("SDK logout did not revoke its session row")
	}
	if err := ids.SetDisabled(t.Context(), created.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Create(httptest.NewRecorder(), request, created.ID); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("disabled user received SDK session")
	}
}
