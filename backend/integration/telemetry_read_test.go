package integration

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/cmd/api/handlers"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/feedback"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/components/preview"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/semantic"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const tailnetOrigin = "https://moneybag.example.ts.net:8443"

type readHarness struct {
	ctx      context.Context
	db       *mongo.Database
	endpoint string
	services mcpserver.Services
	audit    *audit.Service
	cookie   string
	csrf     string
	owner    principal.Principal
	http     *http.Client
	capture  atomic.Pointer[preview.Capture]
}

func testDatabase(t *testing.T, suffix string, monitor *event.CommandMonitor) (*mongo.Database, context.Context) {
	t.Helper()
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	opts := options.Client().ApplyURI(uri)
	if monitor != nil {
		opts.SetMonitor(monitor)
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + suffix)
	if err = db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = client.Disconnect(context.Background()) })
	return db, ctx
}

// newReadHarness runs the real handlers, snapshots, sharing and ledger behind the real compression wrapper.
func newReadHarness(t *testing.T, suffix string, extra dashboard.Reader) *readHarness {
	t.Helper()
	db, ctx := testDatabase(t, suffix, nil)
	ids := identity.NewService(identity.NewMongoRepository(db))
	if err := ids.Init(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := ids.Setup(ctx, "owner@example.test", "Owner", "synthetic-test-password")
	if err != nil {
		t.Fatal(err)
	}
	owner := identity.Principal(user)
	repo := ledger.NewMongoRepository(db)
	if err = repo.Init(ctx, owner); err != nil {
		t.Fatal(err)
	}
	a := audit.NewService(audit.NewMongoRepository(db))
	cache, err := dashboard.NewSnapshots(a)
	if err != nil {
		t.Fatal(err)
	}
	a.OnChange = cache.Changed
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	book := ledger.NewService(repo, a)
	book.Expr = expr
	prices := rates.NewService(rates.NewMongoRepository(db), a)
	tab := tables.NewService(tables.NewMongoRepository(db), a, expr)
	var reader dashboard.Reader = &dashboard.Sources{Ledger: book, Tables: tab, Valuer: prices, Rates: prices, Snapshots: cache, Expr: expr}
	if extra != nil {
		reader = extra
	}
	dash := dashboard.NewService(dashboard.NewMongoRepository(db), &dashboard.Engine{Expr: expr, Reader: reader}, a)
	dash.UseSnapshots(cache)
	shares := sharing.NewService(sharing.NewMongoRepository(db), ids, dash, reader, expr, a)
	rec := reconcile.NewService(reconcile.NewMongoRepository(db), book, a)
	h := &readHarness{ctx: ctx, db: db, audit: a, owner: owner, http: &http.Client{Transport: &http.Transport{DisableCompression: true}}}
	h.services = mcpserver.Services{Identity: ids, Ledger: book, Audit: a, Rates: prices, Tables: tab, Dashboard: dash, Expr: expr, Sources: reader, Sharing: shares, Reconcile: rec, Imports: imports.NewService(imports.NewMongoRepository(db), book, nil, rec, a), Logger: slog.Default()}
	h.services.Semantic = semantic.NewService(semantic.NewMongoRepository(db), book, prices, a, expr)
	h.services.Feedback = feedback.NewService(feedback.NewMongoRepository(db), a)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.endpoint = "http://" + listener.Addr().String()
	h.services.Previews = preview.New(h.endpoint, func(ctx context.Context, target string, width, height int) ([]byte, error) {
		return (*h.capture.Load())(ctx, target, width, height)
	})
	web := handlers.New(handlers.Config{PublicURL: "https://public.example.test", TailnetURL: tailnetOrigin, Version: "integration"}, h.services, oauth.NewService(oauth.NewMongoRepository(db), ids, "https://public.example.test"), repo, db.Client())
	compress, err := handlers.Compression()
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.All("/*", compress(web.Handler()))
	done := make(chan error, 1)
	go func() { done <- app.Listener(listener, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = app.ShutdownWithContext(shutdown)
		<-done
	})
	raw, session, err := ids.StartSession(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	h.cookie, h.csrf = raw, session.CSRF
	return h
}

type readResponse struct {
	status int
	header http.Header
	wire   []byte
	body   map[string]any
}

// get reads what the wire carries, so gzip applied twice or not at all fails the JSON decode.
func (h *readHarness) get(t *testing.T, path, cookie, etag string, gzipped bool) readResponse {
	t.Helper()
	req, err := http.NewRequestWithContext(h.ctx, "GET", h.endpoint+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "moneybag_session", Value: cookie})
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if gzipped {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	res, err := h.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	wire, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := readResponse{status: res.StatusCode, header: res.Header, wire: wire}
	raw := wire
	if res.Header.Get("Content-Encoding") == "gzip" {
		r, err := gzip.NewReader(bytes.NewReader(wire))
		if err != nil {
			t.Fatal(err)
		}
		if raw, err = io.ReadAll(r); err != nil {
			t.Fatal(err)
		}
	}
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") && len(raw) > 0 {
		if err = json.Unmarshal(raw, &out.body); err != nil {
			t.Fatalf("%s: %v in %.200q", path, err, raw)
		}
	}
	return out
}

func kpiValue(t *testing.T, r readResponse, id string) any {
	t.Helper()
	values, _ := r.body["values"].(map[string]any)
	component, ok := values[id].(map[string]any)
	if !ok {
		t.Fatalf("component %s missing from %v", id, r.body["values"])
	}
	return component["value"]
}

func (h *readHarness) publish(t *testing.T, spec dashboard.Spec) {
	t.Helper()
	if _, err := h.services.Dashboard.Create(h.ctx, h.owner, dashboard.CreateInput{ID: spec.ID, Title: spec.Title}); err != nil {
		t.Fatal(err)
	}
	ops, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "", "value": spec}})
	draft, err := h.services.Dashboard.Patch(h.ctx, h.owner, spec.ID, ops)
	if err != nil || !draft.Validation.OK {
		t.Fatal(draft, err)
	}
	if _, err = h.services.Dashboard.Publish(h.ctx, h.owner, spec.ID, draft.DraftVersion, "Synthetic"); err != nil {
		t.Fatal(err)
	}
}

func ledgerRateSpec() dashboard.Spec {
	return dashboard.Spec{ID: "worth", Title: "Worth", Version: 1, Data: map[string]dashboard.Query{"entries": {From: "journal"}, "fx": {From: "rates:EUR/USD"}},
		Pages:      []dashboard.Page{{ID: "main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 6, Component: "count"}, {Span: 6, Component: "rate"}}}}}},
		Components: []dashboard.Component{{ID: "count", Type: "kpi", Title: "Entries", Value: "size(data.entries)", Format: "int"}, {ID: "rate", Type: "kpi", Title: "Rate", Value: "data.fx[0].value", Format: "number"}}}
}

func TestViewETagStaleAndFreshness(t *testing.T) {
	h := newReadHarness(t, "_view", nil)
	ctx, p := h.ctx, h.owner
	prices := h.services.Rates
	ecb := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><Envelope xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref"><Cube><Cube time="2026-01-05"><Cube currency="USD" rate="1.25"/></Cube></Cube></Envelope>`))
	}))
	defer ecb.Close()
	client := ecb.Client()
	client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, ecb.Listener.Addr().String())
	}
	prices.SetHTTPClient(client)
	prices.SetEndpoints("https://example.com/ecb", "https://example.com/gold", "https://example.com/swiss")
	if res, err := prices.Sync(ctx, p, true, false); err != nil || len(res.Written) != 1 {
		t.Fatal(res, err)
	}
	for _, account := range []ledger.Account{{ID: "assets:bank", Type: "asset", Name: "Bank", Commodities: []string{"EUR"}}, {ID: "expenses:food", Type: "expense", Name: "Food", Commodities: []string{"EUR"}}} {
		if _, err := h.services.Ledger.CreateAccount(ctx, p, account); err != nil {
			t.Fatal(err)
		}
	}
	post := func() {
		t.Helper()
		_, err := h.services.Ledger.Post(ctx, p, ledger.EntryInput{Date: "2026-01-06", Payee: "Synthetic", Commit: true, Postings: []ledger.Posting{{Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal("5"), Commodity: "EUR"}}, {Account: "assets:bank", Amount: ledger.Amount{Value: ledger.MustDecimal("-5"), Commodity: "EUR"}}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	post()
	h.publish(t, ledgerRateSpec())
	const path = "/api/dashboards/worth/view?version=published"

	if r := h.get(t, path, "", "", true); r.status != 401 {
		t.Fatal("anonymous view", r.status)
	}
	if r := h.get(t, "/api/dashboards/worth/view?from=yesterday", h.cookie, "", true); r.status != 400 {
		t.Fatal("bad date", r.status)
	}
	if r := h.get(t, "/api/dashboards/nope/view", h.cookie, "", true); r.status != 404 {
		t.Fatal("missing dashboard", r.status)
	}
	first := h.get(t, path, h.cookie, "", true)
	if first.status != 200 || first.header.Get("Content-Encoding") != "gzip" || first.header.Get("ETag") == "" || first.header.Get("Cache-Control") != "private, no-cache" {
		t.Fatal("first view", first.status, first.header)
	}
	if first.body["stale"] != false || first.body["as_of"] == "" || kpiValue(t, first, "count") != float64(1) || kpiValue(t, first, "rate") != 1.25 {
		t.Fatal("first body", first.body)
	}
	for _, field := range []string{"head", "spec", "version", "status", "pages", "values", "validation"} {
		if _, ok := first.body[field]; !ok {
			t.Fatal("view lacks", field)
		}
	}
	if strings.Contains(string(mustJSON(first.body)), "<svg") {
		t.Fatal("view carries rendered markup")
	}
	etag := first.header.Get("ETag")
	started := time.Now()
	hit := h.get(t, path, h.cookie, etag, true)
	t.Logf("304 round trip: %v", time.Since(started))
	if hit.status != 304 || len(hit.wire) != 0 || hit.header.Get("ETag") != etag {
		t.Fatal("conditional view", hit.status, len(hit.wire))
	}
	if r := h.get(t, path, "", etag, true); r.status != 401 {
		t.Fatal("ETag bypassed the session", r.status)
	}
	if plain := h.get(t, path, h.cookie, "", false); plain.status != 200 || plain.header.Get("Content-Encoding") != "" || plain.body["stale"] != false || plain.header.Get("ETag") != etag {
		t.Fatal("identity view", plain.status, plain.header)
	}

	// A sync that changes nothing bumps nothing, so the same bytes keep answering.
	before, err := h.audit.Revisions(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := prices.Sync(ctx, p, true, false); err != nil || len(res.Written) != 0 {
		t.Fatal(res, err)
	}
	after, err := h.audit.Revisions(ctx, p)
	if err != nil || after != before {
		t.Fatal("no-op sync moved the revision", before, after, err)
	}
	if r := h.get(t, path, h.cookie, etag, true); r.status != 304 {
		t.Fatal("no-op sync lost the cached view", r.status)
	}

	// A rate write keeps the last render, flagged, and refreshes behind it.
	at := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if _, err = prices.Set(ctx, p, rates.SetInput{Pair: "EUR/USD", Value: ledger.MustDecimal("2"), At: at, Source: "synthetic", Reason: "Synthetic correction"}); err != nil {
		t.Fatal(err)
	}
	rateOnly, err := h.audit.Revisions(ctx, p)
	if err != nil || rateOnly.Data != after.Data+1 || rateOnly.Ledger() != after.Ledger() {
		t.Fatal("rate write counters", after, rateOnly, err)
	}
	started = time.Now()
	stale := h.get(t, path, h.cookie, etag, true)
	t.Logf("stale read after a rate write: %v", time.Since(started))
	if stale.status != 200 || stale.body["stale"] != true || stale.body["as_of"] != first.body["as_of"] || kpiValue(t, stale, "rate") != 1.25 || stale.header.Get("ETag") == etag {
		t.Fatal("stale view", stale.status, stale.body["stale"], stale.body["as_of"])
	}
	var fresh readResponse
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if fresh = h.get(t, path, h.cookie, "", true); fresh.body["stale"] == false {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if fresh.body["stale"] != false || kpiValue(t, fresh, "rate") != float64(2) {
		t.Fatal("background refresh did not arrive", fresh.body["stale"])
	}

	// A person's ledger write is never answered with an old render, even straight after a rate write.
	if _, err = prices.Set(ctx, p, rates.SetInput{Pair: "EUR/USD", Value: ledger.MustDecimal("3"), At: at, Source: "synthetic", Reason: "Synthetic correction"}); err != nil {
		t.Fatal(err)
	}
	post()
	own := h.get(t, path, h.cookie, "", true)
	if own.status != 200 || own.body["stale"] != false || kpiValue(t, own, "count") != float64(2) || kpiValue(t, own, "rate") != float64(3) {
		t.Fatal("own write served stale", own.body["stale"], own.body["values"])
	}
	// A spec edit is not rate-only either.
	if _, err = prices.Set(ctx, p, rates.SetInput{Pair: "EUR/USD", Value: ledger.MustDecimal("4"), At: at, Source: "synthetic", Reason: "Synthetic correction"}); err != nil {
		t.Fatal(err)
	}
	if _, err = h.services.Dashboard.UpdateComponent(ctx, p, "worth", "count", map[string]json.RawMessage{"title": json.RawMessage(`"Renamed"`)}); err != nil {
		t.Fatal(err)
	}
	if draft := h.get(t, "/api/dashboards/worth/view", h.cookie, "", true); draft.body["stale"] != false || draft.body["status"] != "draft" {
		t.Fatal("draft view", draft.body["stale"], draft.body["status"])
	}
}

func TestBootstrapContract(t *testing.T) {
	h := newReadHarness(t, "_bootstrap", nil)
	for i := range 60 {
		if _, err := h.services.Dashboard.Create(h.ctx, h.owner, dashboard.CreateInput{ID: fmt.Sprintf("d%02d", i), Title: fmt.Sprintf("Synthetic dashboard %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if r := h.get(t, "/api/bootstrap", "", "", true); r.status != 401 {
		t.Fatal("anonymous bootstrap", r.status)
	}
	first := h.get(t, "/api/bootstrap", h.cookie, "", true)
	if first.status != 200 || first.header.Get("Content-Encoding") != "gzip" {
		t.Fatal(first.status, first.header)
	}
	for _, field := range []string{"session", "nav", "dashboards", "tables", "counts", "labels"} {
		if _, ok := first.body[field]; !ok {
			t.Fatal("bootstrap lacks", field)
		}
	}
	session := first.body["session"].(map[string]any)
	counts := first.body["counts"].(map[string]any)
	if session["csrf"] != h.csrf || len(first.body["dashboards"].([]any)) != 60 || len(first.body["nav"].([]any)) != 8 || counts["reconcile"] != float64(0) || counts["imports"] != float64(0) || counts["feedback"] != float64(0) {
		t.Fatal("bootstrap body", first.body)
	}
	if len(first.wire) >= 100*1024 {
		t.Fatalf("bootstrap is %d bytes gzipped", len(first.wire))
	}
	if r := h.get(t, "/api/bootstrap", h.cookie, first.header.Get("ETag"), true); r.status != 304 {
		t.Fatal("bootstrap 304", r.status)
	}
	if _, err := h.services.Dashboard.Create(h.ctx, h.owner, dashboard.CreateInput{ID: "new", Title: "New"}); err != nil {
		t.Fatal(err)
	}
	if r := h.get(t, "/api/bootstrap", h.cookie, first.header.Get("ETag"), true); r.status != 200 || len(r.body["dashboards"].([]any)) != 61 {
		t.Fatal("bootstrap after a write", r.status)
	}
	t.Logf("bootstrap with 60 dashboards: %d bytes gzipped", len(first.wire))
}

// syntheticRows backs a page as heavy as lint allows, so the budget measures a realistic worst case.
type syntheticRows struct{}

func (syntheticRows) Rows(context.Context, principal.Principal, string) ([]map[string]any, error) {
	rows := make([]map[string]any, 1500)
	for i := range rows {
		rows[i] = map[string]any{"month": fmt.Sprintf("20%02d-%02d", 10+i/12, i%12+1), "name": fmt.Sprintf("Synthetic payee %d", i%37), "category": fmt.Sprintf("Category %d", i%11), "amount": float64(i*37%9973) / 7, "other": float64(i*91%7919) / 3}
	}
	return rows, nil
}

func TestPageViewPayloadBudget(t *testing.T) {
	h := newReadHarness(t, "_budget", syntheticRows{})
	spec := dashboard.Spec{ID: "large", Title: "Large", Version: 1, Data: map[string]dashboard.Query{"rows": {From: "journal"}, "recent": {From: "journal", Limit: 49}, "spark": {From: "journal", Limit: 240}}, Computed: map[string]string{"total": "double(size(data.rows)) * 1.5"}}
	row := dashboard.Row{}
	layout := []dashboard.Row{}
	add := func(c dashboard.Component) {
		spec.Components = append(spec.Components, c)
		row.Cols = append(row.Cols, dashboard.Placement{Span: 4, Component: c.ID})
		if len(row.Cols) == 3 {
			layout = append(layout, row)
			row = dashboard.Row{}
		}
	}
	for i := range 4 {
		add(dashboard.Component{ID: fmt.Sprintf("k%d", i), Type: "kpi", Title: "Total", Value: "computed.total", Format: "eur0", Sparkline: "data.spark.map(r, r.amount)"})
	}
	for i := range 4 {
		add(dashboard.Component{ID: fmt.Sprintf("c%d", i), Type: "chart", Title: "Monthly", Mark: "stack", Rows: "rows", Format: "eur0", Encoding: &dashboard.Encoding{X: dashboard.Axis{Field: "month"}, Y: dashboard.Axis{Field: "amount"}, Color: &dashboard.Axis{Field: "category"}}})
	}
	for i := range 4 {
		columns := []dashboard.Column{{Label: "Payee", Value: "row.name"}, {Label: "Category", Value: "row.category"}, {Label: "Month", Value: "row.month"}, {Label: "Amount", Value: "row.amount", Format: "eur"}, {Label: "Other", Value: "row.other", Format: "eur"}}
		add(dashboard.Component{ID: fmt.Sprintf("t%d", i), Type: "table", Title: "Latest", Rows: "recent", Columns: columns})
	}
	spec.Pages = []dashboard.Page{{ID: "main", Layout: layout}}
	h.publish(t, spec)
	r := h.get(t, "/api/dashboards/large/view?version=published", h.cookie, "", true)
	if r.status != 200 || r.body["validation"].(map[string]any)["ok"] != true {
		t.Fatal(r.status, r.body["validation"])
	}
	raw := len(mustJSON(r.body))
	t.Logf("12-panel view: %d bytes JSON, %d bytes gzipped", raw, len(r.wire))
	if raw < 500*1024 {
		t.Fatalf("synthetic page is too small to prove anything: %d bytes", raw)
	}
	if len(r.wire) >= 100*1024 {
		t.Fatalf("view is %d bytes gzipped, budget is 100 KB", len(r.wire))
	}
	started := time.Now()
	if hit := h.get(t, "/api/dashboards/large/view?version=published", h.cookie, "", true); hit.status != 200 || !bytes.Equal(hit.wire, r.wire) {
		t.Fatal("repeat read differs")
	}
	t.Logf("cached 200 round trip: %v", time.Since(started))
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestSharedGrantsEnforceRestrictedReads(t *testing.T) {
	reader := grantRows{}
	h := newReadHarness(t, "_grant", reader)
	ctx, p := h.ctx, h.owner
	spec := dashboard.Spec{ID: "family", Title: "Family", Version: 1, Data: map[string]dashboard.Query{"rows": {From: "journal"}}, Pages: []dashboard.Page{{ID: "main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 6, Component: "count"}, {Span: 6, Component: "list"}}}}}},
		Components: []dashboard.Component{{ID: "count", Type: "kpi", Title: "Rows", Value: "size(data.rows)", Format: "int"}, {ID: "list", Type: "table", Title: "Rows", Rows: "rows", Columns: []dashboard.Column{{Label: "Name", Value: "row.name"}}}}}
	h.publish(t, spec)
	session := func(email string) (principal.Principal, string) {
		t.Helper()
		invite, err := h.services.Identity.Invite(ctx, p, email)
		if err != nil {
			t.Fatal(err)
		}
		user, err := h.services.Identity.Register(ctx, invite, "Synthetic", "synthetic-test-password")
		if err != nil {
			t.Fatal(err)
		}
		raw, _, err := h.services.Identity.StartSession(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		return identity.Principal(user), raw
	}
	editor, editorCookie := session("editor@example.test")
	viewer, viewerCookie := session("viewer@example.test")
	_, strangerCookie := session("stranger@example.test")
	edit, err := h.services.Sharing.Share(ctx, p, sharing.ShareInput{DashboardID: "family", Grantee: editor.UserID, Role: "editor", Sources: []string{"journal"}, RowFilter: `row.name == "public"`})
	if err != nil {
		t.Fatal(err)
	}
	view, err := h.services.Sharing.Share(ctx, p, sharing.ShareInput{DashboardID: "family", Grantee: viewer.UserID, Role: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	// A snapshot frozen before page layouts still shows its stored output.
	frozen, err := h.services.Sharing.Share(ctx, p, sharing.ShareInput{DashboardID: "family", Grantee: viewer.UserID, Role: "viewer", Mode: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Collection("dashboard_snapshots").UpdateOne(ctx, bson.M{"_id": frozen.ShareID}, bson.M{"$set": bson.M{"rendered": bson.M{"html": "<p>frozen output</p>", "values": bson.M{}}}}); err != nil {
		t.Fatal(err)
	}
	if r := h.get(t, "/api/shared/"+frozen.ShareID, viewerCookie, "", true); r.status != 200 || r.body["legacy_html"] != "<p>frozen output</p>" {
		t.Fatal("legacy snapshot", r.status, r.body)
	}
	owned := h.get(t, "/api/dashboards/family/view", h.cookie, "", true)
	if kpiValue(t, owned, "count") != float64(2) || !strings.Contains(string(mustJSON(owned.body)), "private-note") {
		t.Fatal("owner view", owned.body["values"])
	}
	path := "/api/dashboards/" + edit.ShareID + "/view?version=published"
	revision, err := h.audit.Revision(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	restricted := h.get(t, path, editorCookie, "", true)
	if restricted.status != 200 || kpiValue(t, restricted, "count") != float64(1) {
		t.Fatal("editor view", restricted.status, restricted.body)
	}
	if strings.Contains(string(mustJSON(restricted.body["values"])), "private-note") {
		t.Fatal("row filter leaked through the view")
	}
	// The owner's cached bytes must not answer for the grant, or the reverse.
	if again := h.get(t, "/api/dashboards/family/view", h.cookie, "", true); kpiValue(t, again, "count") != float64(2) {
		t.Fatal("grant view reached the owner cache")
	}
	if r := h.get(t, path, editorCookie, owned.header.Get("ETag"), true); r.status != 200 {
		t.Fatal("owner ETag validated a restricted read", r.status)
	}
	if r := h.get(t, path, editorCookie, restricted.header.Get("ETag"), true); r.status != 304 {
		t.Fatal("grant conditional read", r.status)
	}
	if now, err := h.audit.Revision(ctx, p); err != nil || now != revision {
		t.Fatal("shared reads invalidated the owner's views", revision, now, err)
	}
	events, err := h.services.Audit.List(ctx, p, audit.ListInput{Tool: "dashboard_shared_read"})
	if err != nil || len(events.Events) < 3 {
		t.Fatal("shared reads must stay audited, 304 included", len(events.Events), err)
	}
	if r := h.get(t, "/api/dashboards/"+view.ShareID+"/view", viewerCookie, "", true); r.status != 403 || !strings.Contains(fmt.Sprint(r.body["error"]), "/shared/"+view.ShareID) {
		t.Fatal("viewer grants expose rendered output only", r.status, r.body)
	}
	if r := h.get(t, path, strangerCookie, "", true); r.status != 404 {
		t.Fatal("stranger read a grant", r.status)
	}
	if r := h.get(t, "/api/dashboards/family/view", editorCookie, "", true); r.status != 404 {
		t.Fatal("grantee reached the owner id", r.status)
	}

	// A render token is bound to the grant, and revocation stops it immediately.
	var tokenPath string
	reported := false
	capture := preview.Capture(func(ctx context.Context, target string, width, height int) ([]byte, error) {
		tokenPath = "/api/render/" + strings.TrimPrefix(target, h.endpoint+"/render/")
		tokenView := h.get(t, tokenPath+"/view", "", "", true)
		if tokenView.status != 200 || kpiValue(t, tokenView, "count") != float64(1) {
			t.Error("token view must carry the grant's limits", tokenView.status, tokenView.body["values"])
		}
		if r := h.get(t, tokenPath+"/cube", "", "", true); r.status != 404 {
			t.Error("a grant token reached the cube", r.status)
		}
		reported = true
		return nil, h.postReport(t, tokenPath, `{"layout":[],"checks":{"overflow":[],"label_collisions":[],"empty_panels":[],"time_scope_applies":null},"timing_ms":1}`)
	})
	h.capture.Store(&capture)
	access, err := h.services.Sharing.Access(ctx, editor, edit.ShareID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.services.Previews.Render(ctx, editor, sharing.PermissionHash(*access.Grant), preview.Target{Dashboard: edit.ShareID, Version: "published"}); err != nil || !reported {
		t.Fatal(err, reported)
	}
	token, err := h.services.Previews.Issue(editor, sharing.PermissionHash(*access.Grant), preview.Target{Dashboard: edit.ShareID, Version: "published"})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.services.Sharing.Unshare(ctx, p, edit.ShareID); err != nil {
		t.Fatal(err)
	}
	if r := h.get(t, "/api/render/"+token+"/view", "", "", true); r.status != 404 {
		t.Fatal("revoked grant token still reads", r.status)
	}
	if r := h.get(t, path, editorCookie, restricted.header.Get("ETag"), true); r.status != 404 {
		t.Fatal("revoked grant still validates its ETag", r.status)
	}
}

type grantRows struct{}

func (grantRows) Rows(context.Context, principal.Principal, string) ([]map[string]any, error) {
	return []map[string]any{{"name": "public"}, {"name": "private-note"}}, nil
}

func (h *readHarness) postReport(t *testing.T, tokenPath, body string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(h.ctx, "POST", h.endpoint+tokenPath+"/report", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := h.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 204 {
		return fmt.Errorf("report status %d", res.StatusCode)
	}
	return nil
}

func TestDashboardRenderTool(t *testing.T) {
	h := newReadHarness(t, "_render", grantRows{})
	h.publish(t, dashboard.Spec{ID: "plain", Title: "Plain", Version: 1, Data: map[string]dashboard.Query{"rows": {From: "journal"}}, Pages: []dashboard.Page{{ID: "main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 12, Component: "count"}}}}}}, Components: []dashboard.Component{{ID: "count", Type: "kpi", Title: "Rows", Value: "size(data.rows)"}}})
	server := mcpserver.New(h.services, mcpserver.Options{Version: "integration"})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(principal.With(h.ctx, h.owner), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "render-test", Version: "1"}, nil).Connect(h.ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	var widths []int
	var shellStatus int
	capture := preview.Capture(func(ctx context.Context, target string, width, height int) ([]byte, error) {
		widths = append(widths, width)
		tokenPath := "/api/render/" + strings.TrimPrefix(target, h.endpoint+"/render/")
		req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
		res, err := h.http.Do(req)
		if err != nil {
			return nil, err
		}
		res.Body.Close()
		shellStatus = res.StatusCode
		if view := h.get(t, tokenPath+"/view", "", "", true); view.status != 200 || view.body["version"] != float64(2) {
			t.Error("token view", view.status, view.body["version"])
		}
		if err = h.postReport(t, tokenPath, `{"unknown":1}`); err == nil {
			t.Error("report accepted unknown fields")
		}
		return []byte("\x89PNG synthetic"), h.postReport(t, tokenPath, `{"layout":[{"component":"count","x":0,"y":0,"w":800,"h":90}],"checks":{"overflow":["count"],"label_collisions":[],"empty_panels":[],"time_scope_applies":true},"timing_ms":42.5}`)
	})
	h.capture.Store(&capture)
	result, err := cs.CallTool(h.ctx, &mcp.CallToolParams{Name: "dashboard_render", Arguments: map[string]any{"id": "plain", "version": "published", "width": 800, "params": map[string]any{"month": "2026-01"}}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content: %d", len(result.Content))
	}
	image, ok := result.Content[1].(*mcp.ImageContent)
	if !ok || image.MIMEType != "image/png" || !bytes.HasPrefix(image.Data, []byte("\x89PNG")) {
		t.Fatal("inline image", result.Content[1])
	}
	var out map[string]any
	if err = json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	checks := out["checks"].(map[string]any)
	if out["timing_ms"] != 42.5 || len(out["layout"].([]any)) != 1 || checks["overflow"].([]any)[0] != "count" || checks["time_scope_applies"] != true || out["render_ms"] == nil {
		t.Fatal("render findings", out)
	}
	if _, ok := out["lint"]; !ok || out["validation"].(map[string]any)["ok"] != true {
		t.Fatal("render lint and validation", out)
	}
	if len(widths) != 1 || widths[0] != 800 {
		t.Fatal("width", widths)
	}
	if shellStatus == 404 {
		t.Fatal("live token shell", shellStatus)
	}
	if r, err := http.Get(h.endpoint + "/render/unknown-token"); err != nil || r.StatusCode != 404 {
		t.Fatal("unknown token shell", r, err)
	}
	if r, err := http.Get(h.endpoint + "/api/render/unknown-token/view"); err != nil || r.StatusCode != 404 {
		t.Fatal("unknown token view", r, err)
	}
	if r := h.get(t, "/previews/any.png", h.cookie, "", false); r.header.Get("Content-Type") == "image/png" {
		t.Fatal("old preview route survived")
	}

	silent := preview.Capture(func(context.Context, string, int, int) ([]byte, error) { return []byte("\x89PNG blank"), nil })
	h.capture.Store(&silent)
	result, err = cs.CallTool(h.ctx, &mcp.CallToolParams{Name: "dashboard_render", Arguments: map[string]any{"id": "plain", "version": "published"}})
	if err != nil || !result.IsError || !strings.Contains(fmt.Sprint(result.Content[0].(*mcp.TextContent).Text), "never reported") {
		t.Fatal("silent page must fail clearly", result, err)
	}
	if result, err = cs.CallTool(h.ctx, &mcp.CallToolParams{Name: "dashboard_render", Arguments: map[string]any{"id": "plain", "width": 9000}}); err != nil || !result.IsError {
		t.Fatal("width bound", result, err)
	}
	preview, err := cs.CallTool(h.ctx, &mcp.CallToolParams{Name: "dashboard_preview", Arguments: map[string]any{"dashboard_id": "plain", "version": "published"}})
	if err != nil || preview.IsError {
		t.Fatal(preview, err)
	}
	text := preview.Content[0].(*mcp.TextContent).Text
	for _, gone := range []string{"html_url", "png_url", `"html"`} {
		if strings.Contains(text, gone) {
			t.Fatal("dashboard_preview still returns", gone)
		}
	}
}

func TestTailnetOriginAndPublicIdentity(t *testing.T) {
	h := newReadHarness(t, "_tailnet", grantRows{})
	post := func(origin string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(h.ctx, "POST", h.endpoint+"/api/expr/check", strings.NewReader(`{"expr":"1 + 1","role":"value"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "moneybag_session", Value: h.cookie})
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", h.csrf)
		res, err := h.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if got := post(tailnetOrigin); got != 200 {
		t.Fatal("tailnet origin", got)
	}
	if got := post("https://public.example.test"); got != 200 {
		t.Fatal("public origin", got)
	}
	if got := post("https://evil.example.test"); got != 403 {
		t.Fatal("other origin", got)
	}
	meta := h.get(t, "/.well-known/oauth-authorization-server", "", "", false)
	if meta.body["issuer"] != "https://public.example.test" {
		t.Fatal("OAuth must keep the public issuer", meta.body["issuer"])
	}
}

// Plans and read counts are the evidence for the audit, journal and reconcile fixes.
func TestTelemetryQueryPlans(t *testing.T) {
	var reads, writes atomic.Int64
	db, ctx := testDatabase(t, "_plans", &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		switch e.CommandName {
		case "find", "aggregate", "getMore":
			reads.Add(1)
		case "insert", "update", "delete", "findAndModify":
			writes.Add(1)
		}
	}})
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read", "ledger:write"}, Actor: principal.Actor{Kind: "user", ID: "owner"}}
	if err := ledger.NewMongoRepository(db).Init(ctx, p); err != nil {
		t.Fatal(err)
	}
	const entries = 4000
	journal := make([]any, entries)
	for i := range journal {
		account := fmt.Sprintf("assets:a%02d", i%40)
		journal[i] = bson.M{"_id": fmt.Sprintf("j%d", i), "user_id": p.UserID, "seq": int64(i + 1), "status": "posted", "date": fmt.Sprintf("2026-%02d-%02d", i%12+1, i%28+1), "postings": bson.A{bson.M{"account": account, "amount": bson.M{"v": bson.Decimal128{}, "c": "EUR"}}, bson.M{"account": "expenses:x", "amount": bson.M{"v": bson.Decimal128{}, "c": "EUR"}}}}
	}
	if _, err := db.Collection("journal").InsertMany(ctx, journal); err != nil {
		t.Fatal(err)
	}
	a := audit.NewService(audit.NewMongoRepository(db))
	for i := range 210 {
		tool := "tool_a"
		if i%3 == 0 {
			tool = "tool_b"
		}
		who := p
		who.Actor.ID = fmt.Sprintf("actor-%d", i%5)
		payload := map[string]int{"i": i}
		if _, err := a.Write(ctx, who, tool, audit.Target{Collection: "x", ID: fmt.Sprint(i)}, "", payload, payload, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	reads.Store(0)
	page, err := a.List(ctx, p, audit.ListInput{Limit: 200})
	if err != nil || len(page.Events) != 200 {
		t.Fatal(len(page.Events), err)
	}
	if reads.Load() != 2 {
		t.Fatalf("audit page read %d times, want 2", reads.Load())
	}
	if page.Events[0].Outcome == nil || page.Events[0].Outcome.Status != "completed" || page.Events[0].Outcome.Before != nil || page.Events[0].Outcome.After != nil || len(page.Events[0].Before) == 0 {
		t.Fatal("outcome payloads", page.Events[0].Outcome)
	}
	before, after := page.Events[0].Result()
	if len(before) == 0 || len(after) == 0 {
		t.Fatal("Result lost the payloads")
	}
	var stored bson.M
	if err = db.Collection("audit_outcomes").FindOne(ctx, bson.M{"_id": page.Events[0].ID}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if _, dup := stored["before"]; dup {
		t.Fatal("outcome still stores before")
	}
	if err = a.Verify(ctx, p); err != nil {
		t.Fatal("chain with slim outcomes", err)
	}
	// An outcome written before this change keeps its payloads and hash, and still verifies.
	legacy := audit.Outcome{ID: page.Events[0].ID, UserID: p.UserID, Status: "completed", Before: page.Events[0].Before, After: page.Events[0].After, At: page.Events[0].Outcome.At}
	legacy.Hash = audit.OutcomeDigest(page.Events[0].Hash, legacy)
	if _, err = db.Collection("audit_outcomes").ReplaceOne(ctx, bson.M{"_id": legacy.ID}, legacy); err != nil {
		t.Fatal(err)
	}
	if err = a.Verify(ctx, p); err != nil {
		t.Fatal("legacy outcome", err)
	}
	if page, err = a.List(ctx, p, audit.ListInput{Limit: 1}); err != nil || page.Events[0].Outcome.Before != nil {
		t.Fatal("legacy outcome duplicates its event", err)
	}

	type plan struct {
		find     string
		filter   bson.M
		sort     bson.D
		index    string
		examined int64
	}
	for _, c := range []plan{
		{"events", bson.M{"user_id": p.UserID, "tool": "tool_b"}, bson.D{{Key: "seq", Value: -1}}, "user_id_1_tool_1_seq_-1", 70},
		{"events", bson.M{"user_id": p.UserID, "actor.id": "actor-1"}, bson.D{{Key: "seq", Value: -1}}, "user_id_1_actor.id_1_seq_-1", 42},
		{"journal", bson.M{"user_id": p.UserID, "status": "posted", "date": bson.M{"$lte": "2026-12-31"}, "postings.account": bson.M{"$in": bson.A{"assets:a07"}}}, nil, "user_id_1_postings.account_1_date_1", entries / 40},
	} {
		command := bson.D{{Key: "find", Value: c.find}, {Key: "filter", Value: c.filter}}
		if c.sort != nil {
			command = append(command, bson.E{Key: "sort", Value: c.sort})
		}
		var result bson.M
		if err = db.RunCommand(ctx, bson.D{{Key: "explain", Value: command}, {Key: "verbosity", Value: "executionStats"}}).Decode(&result); err != nil {
			t.Fatal(err)
		}
		encoded, _ := bson.MarshalExtJSON(result["queryPlanner"], false, false)
		winning, _ := json.Marshal(mustDecode(encoded)["winningPlan"])
		if !strings.Contains(string(winning), `"indexName":"`+c.index+`"`) || strings.Contains(string(winning), `"stage":"SORT"`) || strings.Contains(string(winning), `"stage":"COLLSCAN"`) {
			t.Fatalf("%s plan: %s", c.index, winning)
		}
		stats, _ := bson.MarshalExtJSON(result["executionStats"], false, false)
		values := mustDecode(stats)
		t.Logf("%s: %v docs examined, %v returned, %v ms", c.index, values["totalDocsExamined"], values["nReturned"], values["executionTimeMillis"])
		if examined := values["totalDocsExamined"].(float64); examined > float64(c.examined) {
			t.Fatalf("%s examined %v documents, want at most %d", c.index, examined, c.examined)
		}
	}

	// reconcile_status writes nothing even for a principal that may write, however stale the assertion is.
	if _, err = db.Collection("accounts").InsertOne(ctx, bson.M{"_id": p.UserID + ":assets:a01", "user_id": p.UserID, "id": "assets:a01", "type": "asset", "name": "Synthetic", "commodities": bson.A{"EUR"}}); err != nil {
		t.Fatal(err)
	}
	assertion := reconcile.Assertion{ID: p.UserID + ":assets:a01:2026-12-31:EUR", UserID: p.UserID, Account: "assets:a01", Date: "2026-12-31", Amount: ledger.Amount{Value: ledger.MustDecimal("999"), Commodity: "EUR"}, Source: "synthetic", OK: true, Diff: ledger.MustDecimal("0")}
	if _, err = db.Collection("balance_assertions").InsertOne(ctx, assertion); err != nil {
		t.Fatal(err)
	}
	book := ledger.NewService(ledger.NewMongoRepository(db), a)
	rec := reconcile.NewService(reconcile.NewMongoRepository(db), book, a)
	writes.Store(0)
	status, err := rec.Status(ctx, p, "")
	if err != nil || len(status) != 1 || status[0].LastOK != "2026-12-31" || writes.Load() != 0 {
		t.Fatal("status", status, writes.Load(), err)
	}
	checked, err := rec.Recheck(ctx, p, "assets:a01")
	if err != nil || len(checked) != 1 || checked[0].LastOK != "" || len(checked[0].Discrepancies) != 1 || writes.Load() == 0 {
		t.Fatal("recheck", checked, writes.Load(), err)
	}
	if _, err = rec.Status(ctx, principal.Principal{UserID: p.UserID, Scopes: []string{"ledger:write"}}, ""); !errors.Is(err, principal.ErrForbidden) {
		t.Fatal("scope", err)
	}
}

func mustDecode(raw []byte) map[string]any {
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}
