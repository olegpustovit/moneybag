package integration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/cmd/api/handlers"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/feedback"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/semantic"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
)

// Opt-in: Identity's owned runner supplies the loopback issuer and private fixture files.
func TestIdentityActualProviderBrowser(t *testing.T) {
	issuer := os.Getenv("MONEYBAG_ID_TEST_ISSUER")
	if issuer == "" {
		t.Skip("requires owned Identity e2e scenario")
	}
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme != "http" || (parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1") {
		t.Fatal("owned loopback issuer required")
	}
	port, err := strconv.Atoi(os.Getenv("MONEYBAG_ID_TEST_APP_PORT"))
	if err != nil || port < 1024 || port > 65535 {
		t.Fatal("reserved app port required")
	}
	origin := "http://localhost:" + strconv.Itoa(port)
	pool := pgtest.New(t)
	ids := identity.NewService(identity.NewPostgresRepository(pool))
	password := providerPrivateFile(t, os.Getenv("MONEYBAG_ID_TEST_PASSWORD_FILE"))
	owner, err := ids.Setup(t.Context(), "eval-owner-local@example.test", "Owner", password)
	if err != nil {
		t.Fatal(err)
	}
	token, err := ids.Invite(t.Context(), identity.Principal(owner), "eval-browser@example.test")
	if err != nil {
		t.Fatal(err)
	}
	existingInvite, err := ids.Invite(t.Context(), identity.Principal(owner), "eval-browser@example.test")
	if err != nil {
		t.Fatal(err)
	}
	newInvite, err := ids.Invite(t.Context(), identity.Principal(owner), "eval-new@example.test")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := ids.Register(t.Context(), token, "Keep local name", password)
	if err != nil {
		t.Fatal(err)
	}
	services, repo := providerServices(t, pool, ids)
	if err := services.Model.Initialize(t.Context(), identity.Principal(legacy)); err != nil {
		t.Fatal(err)
	}
	const entrySQL = `INSERT INTO journal(id,user_id,seq,postings,actor) VALUES('fixture-entry',@user,1,@postings::jsonb,@actor::jsonb)`
	if _, err := pool.Exec(t.Context(), entrySQL, pgx.NamedArgs{"user": legacy.ID, "postings": `[{"account":"assets:bank","amount":{"value":"23.400000000000000000001","commodity":"EUR"}}]`, "actor": `{"kind":"human","id":"keep-actor"}`}); err != nil {
		t.Fatal(err)
	}
	const grantSQL = `INSERT INTO oauth_grants(id,user_id,client_id,client_name,resource,scopes,expires_at) VALUES('fixture-existing-grant',@user,'fixture-agent','Keep agent',@resource,ARRAY['ledger:read'],@expires)`
	if _, err := pool.Exec(t.Context(), grantSQL, pgx.NamedArgs{"user": legacy.ID, "resource": origin + "/mcp", "expires": time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		const sql = `SELECT jsonb_build_object('user',(SELECT to_jsonb(u) FROM users u WHERE id=@user),'credentials',(SELECT to_jsonb(c) FROM credentials c WHERE id=@user),'journal',(SELECT jsonb_agg(to_jsonb(j) ORDER BY id) FROM journal j),'grant',(SELECT to_jsonb(g) FROM oauth_grants g WHERE id='fixture-existing-grant'),'tables',(SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM tables d WHERE user_id=@user),'rows',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM rows r WHERE user_id=@user),'scenarios',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM scenarios s WHERE user_id=@user))::text`
		if err := pool.QueryRow(t.Context(), sql, pgx.NamedArgs{"user": legacy.ID}).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	cfg := handlers.IdentityConfig{Enabled: true, AccessEnabled: true, Issuer: issuer, ClientID: os.Getenv("MONEYBAG_ID_TEST_CLIENT_ID"), ClientSecret: providerPrivateFile(t, os.Getenv("MONEYBAG_ID_TEST_SECRET_FILE"))}
	if _, err := rand.Read(cfg.CookieKey[:]); err != nil {
		t.Fatal(err)
	}
	serve := func(enabled bool) func() {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Fatal(err)
		}
		// A restarted mode gets fresh composition while all persisted finance,
		// credentials and subject bindings remain in the same owned database.
		currentServices := services
		currentIDs := identity.NewService(identity.NewPostgresRepository(pool))
		currentServices.Identity = currentIDs
		web := handlers.New(handlers.Config{PublicURL: origin, Version: "owned-provider"}, currentServices, oauth.NewService(oauth.NewPostgresRepository(pool), currentIDs, origin), repo, pool)
		if enabled {
			if err := web.ConfigureIdentity(t.Context(), cfg); err != nil {
				_ = listener.Close()
				t.Fatal(err)
			}
		}
		compress, err := handlers.Compression()
		if err != nil {
			_ = listener.Close()
			t.Fatal(err)
		}
		app := fiber.New()
		app.All("/mcp", compress(web.RemoteMCP(false)))
		app.All("/mcp/readonly", compress(web.RemoteMCP(true)))
		handler := web.Handler()
		app.All("/*", compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if failure := recover(); failure != nil {
					t.Errorf("owned handler panic at %s: %v\n%s", r.URL.Path, failure, debug.Stack())
					http.Error(w, "owned handler failed", 500)
				}
			}()
			handler.ServeHTTP(w, r)
		})))
		done := make(chan error, 1)
		go func() { done <- app.Listener(listener, fiber.ListenConfig{DisableStartupMessage: true}) }()
		return func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := app.ShutdownWithContext(ctx); err != nil {
				t.Error(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-ctx.Done():
				_ = listener.Close()
				t.Error("owned app failed to stop")
			}
		}
	}
	run := func(stage, email, invitation, userID string) {
		t.Helper()
		fixture := filepath.Join(t.TempDir(), "browser.json")
		data, err := json.Marshal(map[string]string{"origin": origin, "issuer": issuer, "passwordFile": os.Getenv("MONEYBAG_ID_TEST_PASSWORD_FILE"), "stage": stage, "email": email, "invitation": invitation, "userID": userID, "oauthCallback": os.Getenv("MONEYBAG_ID_TEST_OAUTH_CALLBACK"), "policyURL": os.Getenv("MONEYBAG_ID_TEST_POLICY_URL"), "policyKeyFile": os.Getenv("MONEYBAG_ID_TEST_POLICY_KEY_FILE")})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture, data, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "e2e", "identity-provider-browser.mjs"))
		command.Env = append(os.Environ(), "MONEYBAG_ID_BROWSER_FIXTURE="+fixture)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("owned %s browser: %v\n%s", stage, err, output)
		}
		t.Log(strings.TrimSpace(string(output)))
	}

	runSmoke := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "e2e", "production-smoke.mjs"))
		command.Env = append(os.Environ(), "MONEYBAG_SMOKE_ORIGIN="+origin, "MONEYBAG_SMOKE_ID_ORIGIN="+issuer, "MONEYBAG_SMOKE_EMAIL="+legacy.Email, "MONEYBAG_SMOKE_PASSWORD_FILE="+os.Getenv("MONEYBAG_ID_TEST_PASSWORD_FILE"), "MONEYBAG_SMOKE_VERSION=owned-provider")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("persistent eval smoke: %v\n%s", err, output)
		}
		t.Log(strings.TrimSpace(string(output)))
		if snapshot() != before {
			t.Fatal("persistent smoke changed existing account/finance/model/agent data")
		}
		var count int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions`).Scan(&count); err != nil || count != 0 {
			t.Fatal("persistent smoke left app sessions")
		}
	}
	func() {
		stop := serve(true)
		defer stop()
		run("existing", legacy.Email, "", legacy.ID)
		run("oauth", legacy.Email, "", legacy.ID)
		run("existing-invite", legacy.Email, existingInvite, legacy.ID)
		run("uninvited", "eval-new@example.test", "", "")
		var count int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&count); err != nil || count != 2 {
			t.Fatal("unknown ID created local account")
		}
		run("new-invite", "eval-new@example.test", newInvite, "")
		created, err := ids.ByEmail(t.Context(), "eval-new@example.test")
		if err != nil || created.Admin {
			t.Fatal("invited account missing or privileged")
		}
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM credentials WHERE id=@user`, pgx.NamedArgs{"user": created.ID}).Scan(&count); err != nil || count != 0 {
			t.Fatal("ID-only account received password")
		}
		var pending bool
		if err := pool.QueryRow(t.Context(), `SELECT needs_initialization FROM user_identities WHERE user_id=@user`, pgx.NamedArgs{"user": created.ID}).Scan(&pending); err != nil || pending {
			t.Fatal("new account initialization incomplete")
		}
		run("new-existing", created.Email, "", created.ID)
		runSmoke()
		if snapshot() != before {
			t.Fatal("ID flow changed legacy profile, credentials, finance, model or existing agent grant")
		}
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions`).Scan(&count); err != nil || count != 0 {
			t.Fatal("browser left app sessions")
		}
	}()
	func() {
		stop := serve(false)
		defer stop()
		run("rollback", legacy.Email, "", legacy.ID)
		runSmoke()
	}()
	if snapshot() != before {
		t.Fatal("rollback changed existing data")
	}
	if u, err := ids.Login(t.Context(), legacy.Email, password); err != nil || u.ID != legacy.ID {
		t.Fatal("legacy password rollback broken")
	}
}

func providerPrivateFile(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		t.Fatal("private owned fixture file required")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		t.Fatal("private fixture unavailable")
	}
	return strings.TrimSuffix(string(data), "\n")
}

func providerServices(t *testing.T, pool *pgxpool.Pool, ids *identity.Service) (mcpserver.Services, *ledger.PostgresRepository) {
	t.Helper()
	a := audit.NewService(audit.NewPostgresRepository(pool))
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	repo := ledger.NewPostgresRepository(pool)
	book := ledger.NewService(repo, a)
	book.Expr = expr
	prices := rates.NewService(rates.NewPostgresRepository(pool), a)
	tab := tables.NewService(tables.NewPostgresRepository(pool), a, expr)
	models := model.NewService(model.NewPostgresRepository(pool), tab, a, expr)
	sources := &dashboard.Sources{Ledger: book, Tables: tab, Valuer: prices, Rates: prices, Model: models, Expr: expr}
	dash := dashboard.NewService(dashboard.NewPostgresRepository(pool), &dashboard.Engine{Expr: expr, Reader: sources}, a)
	rec := reconcile.NewService(reconcile.NewPostgresRepository(pool), book, a)
	services := mcpserver.Services{Identity: ids, Ledger: book, Audit: a, Rates: prices, Tables: tab, Model: models, Dashboard: dash, Expr: expr, Sources: sources, Reconcile: rec, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	services.Imports = imports.NewService(imports.NewPostgresRepository(pool), book, nil, rec, a)
	services.Semantic = semantic.NewService(semantic.NewPostgresRepository(pool), book, prices, a, expr)
	services.Feedback = feedback.NewService(feedback.NewPostgresRepository(pool), a)
	return services, repo
}
