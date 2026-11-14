package main

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

type blockedWarmReader struct {
	reader    dashboard.Reader
	block     atomic.Bool
	calls     atomic.Int64
	entered   chan struct{}
	cancelled chan struct{}
}

func (r *blockedWarmReader) Rows(ctx context.Context, p principal.Principal, source string) ([]map[string]any, error) {
	r.calls.Add(1)
	if r.block.CompareAndSwap(true, false) {
		close(r.entered)
		<-ctx.Done()
		close(r.cancelled)
		return nil, ctx.Err()
	}
	return r.reader.Rows(ctx, p, source)
}

type observedWarmDashboard struct {
	*dashboard.Service
	done chan struct{}
}

func (s observedWarmDashboard) Preview(ctx context.Context, p principal.Principal, id, version string, params map[string]any) (dashboard.Rendered, error) {
	result, err := s.Service.Preview(ctx, p, id, version, params)
	if id == "dash-6" && err == nil && result.Validation.OK {
		select {
		case s.done <- struct{}{}:
		default:
		}
	}
	return result, err
}

func TestPrewarmAfterPostedEntriesAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var reads atomic.Int64
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read", "ledger:write"}}
	pgdb := pgtest.New(t)
	config := pgdb.Config().Copy()
	config.ConnConfig.Tracer = warmSQLCounter{&reads}
	traced, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	a := audit.NewService(audit.NewPostgresRepository(traced))
	repo := ledger.NewPostgresRepository(traced)
	if err = repo.Init(ctx, p); err != nil {
		t.Fatal(err)
	}
	book := ledger.NewService(repo, a)
	for _, account := range []ledger.Account{
		{ID: "assets:test", Name: "Test asset", Type: "asset", Commodities: []string{"EUR"}},
		{ID: "expenses:test", Name: "Test expense", Type: "expense", Commodities: []string{"EUR"}},
	} {
		if _, err = book.CreateAccount(ctx, p, account); err != nil {
			t.Fatal(err)
		}
	}
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	book.Expr = expr
	cache, err := dashboard.NewSnapshots(a)
	if err != nil {
		t.Fatal(err)
	}
	sources := &dashboard.Sources{Ledger: book, Expr: expr, Snapshots: cache}
	reader := &blockedWarmReader{reader: sources, entered: make(chan struct{}), cancelled: make(chan struct{})}
	service := dashboard.NewService(dashboard.NewPostgresRepository(traced), &dashboard.Engine{Expr: expr, Reader: reader}, a)
	service.UseSnapshots(cache)
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("dash-%d", i)
		spec := dashboard.Spec{ID: id, Title: id, Version: 1, Data: map[string]dashboard.Query{"rows": {From: "journal", Where: "row.payee == 'Second'"}}, Pages: []dashboard.Page{{ID: "main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 12, Component: "count"}}}}}}, Components: []dashboard.Component{{ID: "count", Type: "kpi", Value: "size(data.rows)"}}}
		head := dashboard.Head{Key: p.UserID + ":" + id, UserID: p.UserID, ID: id, Title: id, PublishedVersion: 1, Revision: 1}
		version := dashboard.Version{Key: p.UserID + ":" + id + ":1", UserID: p.UserID, DashboardID: id, Version: 1, Spec: spec}
		if _, err = pgdb.Exec(ctx, `INSERT INTO dashboards(storage_key,user_id,id,title,published_version,revision) VALUES (@key,@user,@id,@title,@published,@revision)`, pgx.NamedArgs{"key": head.Key, "user": head.UserID, "id": head.ID, "title": head.Title, "published": head.PublishedVersion, "revision": head.Revision}); err != nil {
			t.Fatal(err)
		}
		if _, err = pgdb.Exec(ctx, `INSERT INTO dashboard_versions(storage_key,user_id,dashboard_id,version,spec) VALUES (@key,@user,@id,@version,@spec)`, pgx.NamedArgs{"key": version.Key, "user": version.UserID, "id": version.DashboardID, "version": version.Version, "spec": version.Spec}); err != nil {
			t.Fatal(err)
		}
		before, err := service.Preview(ctx, p, id, "published", nil)
		if err != nil || !before.Validation.OK || before.Values["count"].(map[string]any)["value"] != float64(0) {
			t.Fatalf("initial preview: %v, %v", before.Validation, err)
		}
	}
	done := make(chan struct{}, 1)
	change, stop := prewarmer(ctx, cache, observedWarmDashboard{Service: service, done: done}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.OnChange = change
	defer stop()
	post := func(payee string) {
		t.Helper()
		_, err := book.Post(ctx, p, ledger.EntryInput{Date: "2026-01-01", Payee: payee, Commit: true, Postings: []ledger.Posting{
			{Account: "assets:test", Amount: ledger.Amount{Value: ledger.MustDecimal("-1"), Commodity: "EUR"}},
			{Account: "expenses:test", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	reader.block.Store(true)
	post("First")
	select {
	case <-reader.entered:
	case <-ctx.Done():
		t.Fatal("first prewarm did not start")
	}
	post("Second")
	select {
	case <-reader.cancelled:
	case <-ctx.Done():
		t.Fatal("new post did not cancel the earlier prewarm")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("latest prewarm did not finish")
	}
	calls := reader.calls.Load()
	for i := 0; i < 7; i++ {
		reads.Store(0)
		result, err := service.Preview(ctx, p, fmt.Sprintf("dash-%d", i), "published", nil)
		if err != nil || !result.Validation.OK || result.Values["count"].(map[string]any)["value"] != float64(1) || reads.Load() != 1 || reader.calls.Load() != calls {
			t.Fatalf("warmed dashboard %d: valid=%t, reads=%d, builds=%d, %v", i, result.Validation.OK, reads.Load(), reader.calls.Load()-calls, err)
		}
	}
}

type warmSQLCounter struct{ reads *atomic.Int64 }

func (c warmSQLCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(strings.TrimSpace(data.SQL), "SELECT") {
		c.reads.Add(1)
	}
	return ctx
}
func (c warmSQLCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
