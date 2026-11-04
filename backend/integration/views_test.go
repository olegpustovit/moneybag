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

	t.Run("exact decimal cells and undo", func(t *testing.T) {
		const id = "decimal-cells"
		_, err := tab.CreateNamed(ctx, pa, id, tables.CreateInput{Name: "Exact amounts", Columns: []tables.Column{
			{ID: "qty", Type: "decimal", Required: true}, {ID: "price", Type: "decimal", Required: true},
			{ID: "total", Type: "formula", Formula: "row.qty*row.price"},
			{ID: "twice", Type: "formula", Formula: "row.total+row.total"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		updated, err := tab.Update(ctx, pa, tables.UpdateInput{DB: id, Updates: []tables.CellUpdate{{RowID: "one", Column: "qty", Value: "2.5"}, {RowID: "one", Column: "price", Value: "3.12"}}})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := tab.Query(ctx, pa, tables.QueryInput{DB: id, Where: "row.qty*row.price==row.total && row.twice==15.6"})
		if err != nil || len(rows.Rows) != 1 {
			t.Fatalf("exact query: %v %v", rows, err)
		}
		encoded, err := json.Marshal(rows.Rows[0].Cells)
		if err != nil || !strings.Contains(string(encoded), `"total":"7.8"`) || !strings.Contains(string(encoded), `"twice":"15.6"`) {
			t.Fatalf("decimal JSON: %s %v", encoded, err)
		}
		result, err := engine.Evaluate(ctx, pa, "sum(data.rows.map(r,r.total))", map[string]dashboard.Query{"rows": {From: "table:" + id}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err = json.Marshal(result.Value)
		if err != nil || string(encoded) != `"7.8"` {
			t.Fatalf("exact dashboard expression: %s %v", encoded, err)
		}
		changed, err := tab.Update(ctx, pa, tables.UpdateInput{DB: id, Updates: []tables.CellUpdate{{RowID: "one", Column: "qty", Value: "3"}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = hist.Undo(ctx, pa, history.UndoInput{EventID: changed.EventID, Reason: "test"}); err != nil {
			t.Fatal(err)
		}
		rows, err = tab.Query(ctx, pa, tables.QueryInput{DB: id, Where: "row.qty*row.price==row.total && row.total==7.8"})
		if err != nil || len(rows.Rows) != 1 {
			t.Fatalf("decimal undo: %v %v", rows, err)
		}
		if _, err = hist.Undo(ctx, pa, history.UndoInput{EventID: updated.EventID, Reason: "test"}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("column constraints and existing rows", func(t *testing.T) {
		const id = "constrained-types"
		if _, err := tab.CreateNamed(ctx, pa, id, tables.CreateInput{Name: "Constraints", Columns: []tables.Column{{ID: "code", Type: "string"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := tab.Update(ctx, pa, tables.UpdateInput{DB: id, Updates: []tables.CellUpdate{{RowID: "one", Column: "code", Value: "A"}, {RowID: "two", Column: "code", Value: "B"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := tab.Alter(ctx, pa, tables.AlterInput{DB: id, RemoveColumns: []string{"code"}, AddColumns: []tables.Column{{ID: "code", Type: "string", Required: true, Enum: []any{"A"}}}}); err == nil {
			t.Fatal("constraint invalidating existing row accepted")
		}
		current, err := tab.Get(ctx, pa, id)
		if err != nil || current.Columns[0].Required {
			t.Fatalf("refused schema changed table: %v %v", current, err)
		}
		if _, err = tab.Alter(ctx, pa, tables.AlterInput{DB: id, RemoveColumns: []string{"code"}, AddColumns: []tables.Column{{ID: "code", Type: "string", Required: true, Pattern: "^[A-Z]$"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err = tab.Update(ctx, pa, tables.UpdateInput{DB: id, Updates: []tables.CellUpdate{{RowID: "one", Column: "code", Value: "C"}, {RowID: "new", Column: "code", Value: ""}}}); err == nil {
			t.Fatal("invalid multi-row update accepted")
		}
		rows, err := tab.Query(ctx, pa, tables.QueryInput{DB: id})
		if err != nil || len(rows.Rows) != 2 || rows.Rows[0].Cells["code"] != "A" || rows.Rows[0].Version != 1 {
			t.Fatalf("validation partially wrote cells: %v %v", rows, err)
		}
		updated, err := tab.Update(ctx, pa, tables.UpdateInput{DB: id, Updates: []tables.CellUpdate{{RowID: "one", Column: "code", Value: "C"}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tab.Alter(ctx, pa, tables.AlterInput{DB: id, RemoveColumns: []string{"code"}, AddColumns: []tables.Column{{ID: "code", Type: "string", Required: true, Enum: []any{"B", "C"}}}}); err != nil {
			t.Fatal(err)
		}
		if _, err = hist.Undo(ctx, pa, history.UndoInput{EventID: updated.EventID, Reason: "test"}); err == nil {
			t.Fatal("undo bypassed current constraints")
		}
		rows, err = tab.Query(ctx, pa, tables.QueryInput{DB: id})
		if err != nil || rows.Rows[0].Cells["code"] != "C" {
			t.Fatalf("refused undo changed cells: %v %v", rows, err)
		}
	})

	t.Run("nested table types", func(t *testing.T) {
		created, err := tab.CreateNamed(ctx, pa, "nested-types", tables.CreateInput{
			Name: "Nested types", Description: "Receipt source metadata",
			Columns: []tables.Column{
				{ID: "attrs", Type: "json", Description: "Country-specific facts"},
				{ID: "payees", Type: "list"},
				{ID: "issued_at", Type: "datetime"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if created.Description != "Receipt source metadata" {
			t.Fatal(created)
		}
		updated, err := tab.Update(ctx, pa, tables.UpdateInput{DB: created.ID, Updates: []tables.CellUpdate{
			{RowID: "one", Column: "attrs", Value: map[string]any{"vat": map[string]any{"rate": float64(25)}}},
			{RowID: "one", Column: "payees", Value: []any{"Konzum"}},
			{RowID: "one", Column: "issued_at", Value: "2026-11-04T18:00:00+01:00"},
		}})
		if err != nil || len(updated.Rows) != 1 {
			t.Fatalf("update: %v %v", updated, err)
		}
		queried, err := tab.Query(ctx, pa, tables.QueryInput{DB: created.ID, Where: `row.attrs.vat.rate == 25 && row.payees[0] == "Konzum" && row.issued_at == "2026-11-04T18:00:00+01:00"`})
		if err != nil || len(queried.Rows) != 1 {
			t.Fatalf("query: %v %v", queried, err)
		}
		if queried.Columns[0].Description != "Country-specific facts" {
			t.Fatal(queried.Columns)
		}
		result, err := engine.Evaluate(ctx, pa, "data.rows[0].attrs.vat.rate", map[string]dashboard.Query{"rows": {From: "table:" + created.ID}}, nil)
		if err != nil || result.Value != float64(25) {
			t.Fatalf("dashboard: %v %v", result, err)
		}
		if _, err = tab.Query(ctx, pb, tables.QueryInput{DB: created.ID}); err == nil {
			t.Fatal("cross-user table query succeeded")
		}
		if _, err = hist.Undo(ctx, pa, history.UndoInput{EventID: updated.EventID, Reason: "test"}); err != nil {
			t.Fatal(err)
		}
		queried, err = tab.Query(ctx, pa, tables.QueryInput{DB: created.ID})
		if err != nil || len(queried.Rows) != 0 {
			t.Fatalf("undo: %v %v", queried, err)
		}
	})

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
