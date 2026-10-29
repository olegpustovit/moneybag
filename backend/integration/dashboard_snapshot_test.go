package integration

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type snapshotReader struct {
	calls atomic.Int64
	value atomic.Int64
}

func (r *snapshotReader) Rows(context.Context, principal.Principal, string) ([]map[string]any, error) {
	r.calls.Add(1)
	return []map[string]any{{"value": float64(r.value.Load())}}, nil
}
func TestDashboardSnapshotsRespectRevisionAndReader(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	var reads atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "find" || e.CommandName == "aggregate" || e.CommandName == "getMore" {
			reads.Add(1)
		}
	}}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_snapshots")
	if err := db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(ctx); _ = client.Disconnect(ctx) })
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read", "ledger:write", "dash:write"}}
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
	reader := &snapshotReader{}
	reader.value.Store(1)
	service := dashboard.NewService(dashboard.NewMongoRepository(db), &dashboard.Engine{Expr: expr, Reader: reader}, a)
	service.UseSnapshots(cache)
	spec := dashboard.Spec{ID: "sample", Title: "Synthetic", Version: 1, Data: map[string]dashboard.Query{"rows": {From: "journal"}}, Pages: []dashboard.Page{{ID: "main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 3, Component: "value"}, {Span: 3, Component: "table"}, {Span: 3, Component: "chart"}, {Span: 3, Component: "bars"}}}}}}, Components: []dashboard.Component{
		{ID: "value", Type: "kpi", Value: "data.rows[0].value"},
		{ID: "table", Type: "table", Rows: "rows", Columns: []dashboard.Column{{Label: "Value", Value: "row.value"}}},
		{ID: "chart", Type: "chart", Mark: "line", Rows: "rows", Encoding: &dashboard.Encoding{X: dashboard.Axis{Field: "value"}, Y: dashboard.Axis{Field: "value"}}},
		{ID: "bars", Type: "budget_bars", Rows: "rows", Label: "'sample'", Actual: "row.value", Limit: "5.0"},
	}}
	head := dashboard.Head{Key: p.UserID + ":sample", UserID: p.UserID, ID: "sample", Title: "Synthetic", PublishedVersion: 1, Revision: 1}
	version := dashboard.Version{Key: p.UserID + ":sample:1", UserID: p.UserID, DashboardID: "sample", Version: 1, Spec: spec}
	if _, err := db.Collection("dashboards").InsertOne(ctx, head); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("dashboard_versions").InsertOne(ctx, version); err != nil {
		t.Fatal(err)
	}
	first, err := service.Preview(ctx, p, "sample", "published", nil)
	if err != nil || !first.Validation.OK || first.Values["value"] != float64(1) {
		t.Fatalf("first preview: %+v %v", first.Validation, err)
	}
	first.Values["value"] = float64(999)
	first.Values["table"].([]map[string]any)[0]["Value"] = float64(999)
	reads.Store(0)
	second, err := service.Preview(ctx, p, "sample", "published", nil)
	if err != nil || second.Values["value"] != float64(1) || second.Values["table"].([]map[string]any)[0]["Value"] != float64(1) || reader.calls.Load() != 1 || reads.Load() > 1 {
		t.Fatalf("warm preview: %v, %v, builds=%d reads=%d", second.Values, err, reader.calls.Load(), reads.Load())
	}
	filtered := &snapshotReader{}
	filtered.value.Store(2)
	shared, err := service.WithReader(filtered).Preview(ctx, p, "sample", "published", nil)
	if err != nil || shared.Values["value"] != float64(2) {
		t.Fatalf("reader isolation: %v, %v", shared.Values, err)
	}
	if err := a.Mutation(ctx, p, func(context.Context) error { reader.value.Store(3); return nil }); err != nil {
		t.Fatal(err)
	}
	fresh, err := service.Preview(ctx, p, "sample", "published", nil)
	if err != nil || fresh.Values["value"] != float64(3) || reader.calls.Load() != 2 {
		t.Fatalf("post-write preview: %v, %v, builds=%d", fresh.Values, err, reader.calls.Load())
	}
	bookRepo := ledger.NewMongoRepository(db)
	if err := bookRepo.Init(ctx, p); err != nil {
		t.Fatal(err)
	}
	book := ledger.NewService(bookRepo, a)
	for _, account := range []ledger.Account{
		{ID: "assets:test", Type: "asset", Name: "Test asset", Commodities: []string{"EUR"}},
		{ID: "expenses:test", Type: "expense", Name: "Test expense", Commodities: []string{"EUR"}},
	} {
		if _, err := book.CreateAccount(ctx, p, account); err != nil {
			t.Fatal(err)
		}
	}
	journalSpec := dashboard.Spec{ID: "journal-count", Title: "Synthetic journal", Version: 1, Data: map[string]dashboard.Query{"entries": {From: "journal"}}, Pages: []dashboard.Page{{ID: "main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 12, Component: "count"}}}}}}, Components: []dashboard.Component{{ID: "count", Type: "kpi", Value: "size(data.entries)"}}}
	if _, err := db.Collection("dashboards").InsertOne(ctx, dashboard.Head{Key: p.UserID + ":journal-count", UserID: p.UserID, ID: "journal-count", PublishedVersion: 1, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("dashboard_versions").InsertOne(ctx, dashboard.Version{Key: p.UserID + ":journal-count:1", UserID: p.UserID, DashboardID: "journal-count", Version: 1, Spec: journalSpec}); err != nil {
		t.Fatal(err)
	}
	sources := &dashboard.Sources{Ledger: book, Snapshots: cache}
	journal := dashboard.NewService(dashboard.NewMongoRepository(db), &dashboard.Engine{Expr: expr, Reader: sources}, a)
	journal.UseSnapshots(cache)
	beforePost, err := journal.Preview(ctx, p, "journal-count", "published", nil)
	if err != nil || beforePost.Values["count"] != float64(0) {
		t.Fatalf("initial journal: %v, %v", beforePost.Values, err)
	}
	_, err = book.Post(ctx, p, ledger.EntryInput{Date: "2026-10-01", Payee: "Synthetic", Commit: true, Postings: []ledger.Posting{
		{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("-1"), Commodity: "EUR"}},
		{Account: "expenses:test", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	afterPost, err := journal.Preview(ctx, p, "journal-count", "published", nil)
	if err != nil || afterPost.Values["count"] != float64(1) {
		t.Fatalf("journal after post: %v, %v", afterPost.Values, err)
	}
}
