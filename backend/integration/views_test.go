package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/history"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"strings"
	"testing"
)

func lastEvent(t *testing.T, ctx context.Context, hist *history.Service, p principal.Principal, target, tool string) audit.Event {
	t.Helper()
	h, e := hist.Of(ctx, p, target, false)
	if e != nil {
		t.Fatal(e)
	}
	for _, ev := range h.Events {
		if ev.Tool == tool {
			if ev.Before != nil || ev.After != nil {
				t.Fatal("history_of leaked payload without include_payload")
			}
			return ev
		}
	}
	t.Fatal("no event", tool)
	return audit.Event{}
}

func testViewsTablesUnpublish(t *testing.T, ctx context.Context, pa, pb principal.Principal, dash *dashboard.Service, tab *tables.Service, hist *history.Service, sources *dashboard.Sources, expr *expressions.Engine) {
	t.Helper()
	sources.Views, sources.Expr = dash, expr
	engine := dashboard.Engine{Expr: expr, Reader: sources}

	// Tables: where-delete, alter and undo.
	if _, e := tab.CreateNamed(ctx, pa, "ws3t", tables.CreateInput{Name: "WS3", Columns: []tables.Column{{ID: "n", Type: "number"}, {ID: "s", Type: "string"}}}); e != nil {
		t.Fatal(e)
	}
	updates := []tables.CellUpdate{}
	for i := 1; i <= 4; i++ {
		updates = append(updates, tables.CellUpdate{RowID: fmt.Sprint("r", i), Column: "n", Value: float64(i)})
	}
	if _, e := tab.Update(ctx, pa, tables.UpdateInput{DB: "ws3t", Updates: updates}); e != nil {
		t.Fatal(e)
	}

	// Views: aggregates, per-user scoping and cycle guard.
	q := dashboard.Query{From: "table:ws3t", Agg: map[string]string{"med": "median(row.n)", "p90": "percentile(row.n, 90)", "slope": "linear_slope(row.n, row.n * 2.0)", "distinct": "count_distinct(row.n)"}}
	if _, e := dash.UpsertView(ctx, pa, dashboard.ViewInput{ID: "v1", Query: q}); e != nil {
		t.Fatal(e)
	}
	r, e := engine.Evaluate(ctx, pa, "[data.d[0].med, data.d[0].p90, data.d[0].slope, data.d[0].distinct]", map[string]dashboard.Query{"d": {From: "view:v1"}}, nil)
	if e != nil || fmt.Sprint(r.Value) != "[2.5 3.7 2 4]" {
		t.Fatal("view aggregates", r.Value, e)
	}
	if _, e = engine.Evaluate(ctx, pb, "1", map[string]dashboard.Query{"d": {From: "view:v1"}}, nil); e == nil {
		t.Fatal("another user read the view")
	}
	if _, e = dash.UpsertView(ctx, pa, dashboard.ViewInput{ID: "v2", Query: dashboard.Query{From: "view:v1"}}); e != nil {
		t.Fatal(e)
	}
	if _, e = dash.UpsertView(ctx, pa, dashboard.ViewInput{ID: "v1", Query: dashboard.Query{From: "view:v2"}}); e == nil || !strings.Contains(e.Error(), "itself") {
		t.Fatal("view cycle accepted", e)
	}

	del, e := tab.DeleteRows(ctx, pa, tables.DeleteInput{DB: "ws3t", Where: "row.n >= 3"})
	if e != nil || len(del.Rows) != 2 {
		t.Fatal("where delete", del, e)
	}
	if _, e = tab.DeleteRows(ctx, pa, tables.DeleteInput{DB: "ws3t", Where: "row.n >= 3", RowIDs: []string{"r1"}}); e == nil {
		t.Fatal("row_ids and where together accepted")
	}
	if _, e = tab.Alter(ctx, pa, tables.AlterInput{DB: "ws3t", AddColumns: []tables.Column{{ID: "extra", Type: "string"}}, RemoveColumns: []string{"s"}}); e != nil {
		t.Fatal(e)
	}
	got, _ := tab.Get(ctx, pa, "ws3t")
	if len(got.Columns) != 2 || got.Columns[1].ID != "extra" {
		t.Fatal("alter", got.Columns)
	}
	if _, e = hist.Undo(ctx, pa, history.UndoInput{EventID: lastEvent(t, ctx, hist, pa, "ws3t", "db_alter").ID, Reason: "test"}); e != nil {
		t.Fatal(e)
	}
	if got, _ = tab.Get(ctx, pa, "ws3t"); len(got.Columns) != 2 || got.Columns[1].ID != "s" {
		t.Fatal("alter undo", got.Columns)
	}

	// Dashboards: string ops, located patch errors, unpublish and undo.
	if _, e = dash.Create(ctx, pa, dashboard.CreateInput{ID: "ws3d", Title: "WS3"}); e != nil {
		t.Fatal(e)
	}
	op, _ := json.Marshal(`[{"op":"remove","path":"/computed/x"}]`)
	if _, e = dash.Patch(ctx, pa, "ws3d", op); e == nil || !strings.Contains(e.Error(), "op 0: path /computed/x not found") {
		t.Fatal("patch error", e)
	}
	op, _ = json.Marshal(`[{"op":"add","path":"/computed","value":{"x":"1"}}]`)
	if _, e = dash.Patch(ctx, pa, "ws3d", op); e != nil {
		t.Fatal(e)
	}
	if _, e = dash.Publish(ctx, pa, "ws3d", 0, ""); e != nil {
		t.Fatal(e)
	}
	if e = dash.Unpublish(ctx, pa, "ws3d", "test"); e != nil {
		t.Fatal(e)
	}
	if ok, _ := dash.Published(ctx, pa, "ws3d"); ok {
		t.Fatal("still published")
	}
	if _, e = dash.Get(ctx, pa, "ws3d", "draft"); e != nil {
		t.Fatal("draft lost", e)
	}
	if _, e = hist.Undo(ctx, pa, history.UndoInput{EventID: lastEvent(t, ctx, hist, pa, "ws3d", "dashboard_unpublish").ID, Reason: "test"}); e != nil {
		t.Fatal(e)
	}
	if ok, _ := dash.Published(ctx, pa, "ws3d"); !ok {
		t.Fatal("undo did not republish")
	}
}
