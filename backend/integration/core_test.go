package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSharedDatabaseLedgerIdentityOAuth(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI to a dedicated moneybag_test_ database on standalone MongoDB")
	}
	parsed, e := url.Parse(uri)
	if e != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("integration tests require the dedicated moneybag_test_ database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client, e := mongodb.Connect(ctx, uri)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	db := client.Database(strings.TrimPrefix(parsed.Path, "/"))
	if e = db.Drop(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := db.Drop(context.Background()); e != nil {
			t.Error(e)
		}
	})
	ids := identity.NewService(identity.NewMongoRepository(db))
	if e = ids.Init(ctx); e != nil {
		t.Fatal(e)
	}
	a, e := ids.Setup(ctx, "alice@example.test", "Alice", "synthetic-test-password")
	if e != nil {
		t.Fatal(e)
	}
	pa := identity.Principal(a)
	if _, e = ids.Setup(ctx, "hijack@example.test", "Hijack", "synthetic-test-password"); !errors.Is(e, principal.ErrForbidden) {
		t.Fatalf("second setup: %v", e)
	}
	invite, e := ids.Invite(ctx, pa, "bob@example.test")
	if e != nil {
		t.Fatal(e)
	}
	b, e := ids.Register(ctx, invite, "Bob", "synthetic-test-password")
	if e != nil {
		t.Fatal(e)
	}
	pb := identity.Principal(b)
	if _, e = ids.Register(ctx, invite, "Bob", "synthetic-test-password"); e == nil {
		t.Fatal("invite reused")
	}
	if _, e = ids.Invite(ctx, pb, "evil@example.test"); !errors.Is(e, principal.ErrForbidden) {
		t.Fatal("non-admin invite accepted")
	}
	if _, e = ids.Login(ctx, a.Email, "wrong-password"); !errors.Is(e, identity.ErrCredentials) {
		t.Fatal(e)
	}
	raw, session, e := ids.StartSession(ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	got, _, e := ids.Session(ctx, raw)
	if e != nil || got.UserID != a.ID {
		t.Fatal("session failed", e)
	}
	encoded, _ := json.Marshal(session)
	if strings.Contains(string(encoded), raw) || strings.Contains(string(encoded), session.CSRF) {
		t.Fatal("session secret leaked")
	}
	auditSvc := audit.NewService(audit.NewMongoRepository(db))
	repo := ledger.NewMongoRepository(db)
	if e = repo.Init(ctx, pa); e != nil {
		t.Fatal(e)
	}
	book := ledger.NewService(repo, auditSvc)
	for _, p := range []principal.Principal{pa, pb} {
		for _, a := range []ledger.Account{{ID: "assets:bank", Type: "asset", Name: "Bank", Commodities: []string{"EUR"}}, {ID: "expenses:food", Type: "expense", Name: "Food", Commodities: []string{"EUR"}}} {
			if _, e = book.CreateAccount(ctx, p, a); e != nil {
				t.Fatal("colliding logical accounts should work across users", e)
			}
		}
	}
	in := ledger.EntryInput{Date: "2026-10-01", Payee: "Synthetic groceries", Commit: true, Postings: []ledger.Posting{{Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal("23.40"), Commodity: "EUR"}}, {Account: "assets:bank", Amount: ledger.Amount{Value: ledger.MustDecimal("-23.40"), Commodity: "EUR"}}}}
	ar, e := book.Post(ctx, pa, in)
	if e != nil {
		t.Fatal(e)
	}
	br, e := book.Post(ctx, pb, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = book.Get(ctx, pa, br.Entry.ID); !errors.Is(e, principal.ErrNotFound) {
		t.Fatal("cross-user entry accessible", e)
	}
	if _, e = book.Reverse(ctx, pa, ledger.ReverseInput{ID: br.Entry.ID, Reason: "cross-user attempt"}); !errors.Is(e, principal.ErrNotFound) {
		t.Fatal("cross-user mutation accepted", e)
	}
	page, e := book.Entries(ctx, pa, ledger.EntriesInput{})
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != ar.Entry.ID {
		t.Fatal("scoped entry list", page, e)
	}
	invalid := in
	invalid.Postings = append([]ledger.Posting{}, in.Postings...)
	invalid.Postings[0].Amount.Value = ledger.MustDecimal("23.41")
	if _, e = book.Post(ctx, pa, invalid); e == nil {
		t.Fatal("unbalanced entry accepted")
	}
	if _, e = book.Reverse(ctx, pa, ledger.ReverseInput{ID: ar.Entry.ID, Reason: "Test reversal"}); e != nil {
		t.Fatal(e)
	}
	if _, e = book.Reverse(ctx, pa, ledger.ReverseInput{ID: ar.Entry.ID, Reason: "Repeat"}); e == nil {
		t.Fatal("double reversal accepted")
	}
	balances, e := book.Balances(ctx, pa, ledger.BalancesInput{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range balances.Rows {
		for _, v := range r.Amounts {
			if v.Value.Rat().Sign() != 0 {
				t.Fatal("reversal did not zero balances", balances)
			}
		}
	}
	if e = book.Verify(ctx, pa); e != nil {
		t.Fatal(e)
	}
	if _, e = book.CreateAccount(ctx, pb, ledger.Account{ID: "assets:usd", Type: "asset", Name: "USD", Commodities: []string{"USD"}}); e != nil {
		t.Fatal(e)
	}
	fee := ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}
	if _, e = book.Transfer(ctx, pb, ledger.TransferInput{Date: "2026-10-02", FromAccount: "assets:bank", ToAccount: "assets:usd", Amount: ledger.Amount{Value: ledger.MustDecimal("10"), Commodity: "EUR"}, Received: &ledger.Amount{Value: ledger.MustDecimal("11"), Commodity: "USD"}, Fee: &fee}); e != nil {
		t.Fatal("exchange should provision system accounts", e)
	}
	if conv, e := book.Account(ctx, pb, "equity:conversion"); e != nil || len(conv.Commodities) != 2 {
		t.Fatal("conversion account", conv, e)
	}
	if e = book.Verify(ctx, pb); e != nil {
		t.Fatal(e)
	}
	var stored ledger.Entry
	if e = db.Collection("journal").FindOne(ctx, bson.M{"_id": ar.Entry.ID}).Decode(&stored); e != nil || stored.Status != "posted" || stored.Postings[0].Amount.Value.String() != "23.40" {
		t.Fatal("original entry mutated", e)
	}
	readonly := pa
	readonly.Scopes = []string{"ledger:read"}
	if _, e = book.Post(ctx, readonly, in); !errors.Is(e, principal.ErrForbidden) {
		t.Fatal("read-only write accepted", e)
	}
	oa := oauth.NewService(oauth.NewMongoRepository(db), ids, "http://localhost:6459")
	if e = oa.Init(ctx); e != nil {
		t.Fatal(e)
	}
	reg, e := oa.Register(ctx, oauth.RegisterInput{Name: "Synthetic agent", RedirectURIs: []string{"http://127.0.0.1:8484/callback"}, AuthMethod: "none"})
	if e != nil {
		t.Fatal(e)
	}
	verifier := identity.Random()
	auth := oauth.Authorization{ClientID: reg.ID, RedirectURI: reg.RedirectURIs[0], ResponseType: "code", Scope: "ledger:read ledger:write", State: "opaque-state", Challenge: oauth.Challenge(verifier), ChallengeMethod: "S256", Resource: "http://localhost:6459/mcp"}
	code, e := oa.Authorize(ctx, auth, pa)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = oa.Exchange(ctx, reg.Client, code, auth.RedirectURI, identity.Random(), auth.Resource); !errors.Is(e, oauth.ErrInvalidGrant) {
		t.Fatal("bad verifier accepted", e)
	}
	tokens, e := oa.Exchange(ctx, reg.Client, code, auth.RedirectURI, verifier, auth.Resource)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = oa.Exchange(ctx, reg.Client, code, auth.RedirectURI, verifier, auth.Resource); !errors.Is(e, oauth.ErrInvalidGrant) {
		t.Fatal("code reused", e)
	}
	ap, e := oa.Bearer(ctx, tokens.AccessToken, auth.Resource)
	if e != nil || ap.UserID != a.ID || ap.Actor.Kind != "agent" {
		t.Fatal("agent principal", e)
	}
	if _, e = oa.Bearer(ctx, tokens.AccessToken, auth.Resource+"/readonly"); e == nil {
		t.Fatal("wrong audience accepted")
	}
	refreshed, e := oa.Refresh(ctx, reg.Client, tokens.RefreshToken, auth.Resource, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = oa.Refresh(ctx, reg.Client, tokens.RefreshToken, auth.Resource, ""); !errors.Is(e, oauth.ErrInvalidGrant) {
		t.Fatal("refresh reuse accepted", e)
	}
	if _, e = oa.Bearer(ctx, refreshed.AccessToken, auth.Resource); e == nil {
		t.Fatal("reuse failed to revoke token family")
	}
	users := 0
	if e = ids.EachUser(ctx, func(p principal.Principal) error { users++; return p.Require("ledger:read") }); e != nil || users != 2 {
		t.Fatal("maintenance principals", users, e)
	}
	canceled, cancelWrite := context.WithCancel(ctx)
	eventID, e := auditSvc.Write(canceled, pa, "cancellation_fixture", audit.Target{Collection: "test", ID: "cancellation"}, "", nil, nil, func(context.Context) error { cancelWrite(); return nil })
	if e != nil {
		t.Fatal("completion record after cancellation", e)
	}
	recorded, e := auditSvc.Get(ctx, pa, eventID)
	if e != nil || recorded.Outcome == nil || recorded.Outcome.Status != "completed" {
		t.Fatal("durable outcome after cancellation", recorded, e)
	}
	testProductWorkflows(t, ctx, db, pa, pb, book, auditSvc, ids)
}
