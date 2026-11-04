package mcpserver

import (
	"context"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func registerViewsAndTables(r registration, s Services) {
	add(r, "db_templates", "tables", "ledger:read", "List ready-made ordinary table schemas and agent playbooks. Create all receipts tables with db_create {template:receipts}; reuse natural ids and write a complete receipt in one db_batch, dry_run first.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Templates []tables.Template `json:"templates"`
	}, error) {
		return struct {
			Templates []tables.Template `json:"templates"`
		}{tables.Templates()}, p.Require("ledger:read")
	})
	add(r, "view_upsert", "dashboard", "dash:write", "Use this to save a named query (the same query object dashboards use: from, where, derive, group, agg, sort, limit, hide_zero) as a per-user view. Dashboards read it as the source from: \"view:<id>\". agg also supports mean, median, count_distinct, percentile(expr, p) with p 0..100, and linear_slope(x, y) (least-squares slope of y per unit of x). A view that references itself through view: sources is rejected.", false, false, s.Dashboard.UpsertView)
	add(r, "view_get", "dashboard", "ledger:read", "Use this to read one saved view by id.", true, false, func(ctx context.Context, p principal.Principal, in IDInput) (dashboard.View, error) {
		return s.Dashboard.View(ctx, p, in.ID)
	})
	add(r, "view_list", "dashboard", "ledger:read", "Use this to list your saved views and their queries.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Items []dashboard.View `json:"items"`
	}, error) {
		v, e := s.Dashboard.Views(ctx, p)
		return struct {
			Items []dashboard.View `json:"items"`
		}{v}, e
	})
	add(r, "dashboard_unpublish", "dashboard", "dash:write", "Use this to take a published dashboard offline: shared viewers lose access and the published pointer is cleared, while every draft and version is kept. Publish again, or undo, to restore it.", false, true, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string `json:"dashboard_id"`
		Reason      string `json:"reason,omitempty"`
	}) (struct {
		Unpublished string `json:"unpublished"`
	}, error) {
		e := s.Dashboard.Unpublish(ctx, p, in.DashboardID, in.Reason)
		if e == nil {
			e = s.Sharing.RefreshDashboard(ctx, p, in.DashboardID)
		}
		return struct {
			Unpublished string `json:"unpublished"`
		}{in.DashboardID}, e
	})
	add(r, "db_drop", "tables", "model:write", "Use this to drop (archive) a user database; it disappears from db_list and cannot be queried. Rows are retained and undo restores it.", false, true, func(ctx context.Context, p principal.Principal, in struct {
		DB     string `json:"db"`
		Reason string `json:"reason,omitempty"`
	}) (struct {
		Dropped string `json:"dropped"`
	}, error) {
		e := s.Tables.Archive(ctx, p, in.DB, true, in.Reason)
		return struct {
			Dropped string `json:"dropped"`
		}{in.DB}, e
	})
	add(r, "db_alter", "tables", "model:write", "Use this to change a database schema: remove_columns by id, then add_columns (same shape as db_create). Optional keys, checks, ledger and description replace those schema fields; [] clears keys/checks and {} clears ledger. Existing active rows must satisfy all proposed cell, key, reference, check and ledger constraints before the schema changes. Key/constraint migrations rewrite derived values in order on standalone MongoDB; storage failures can leave earlier row changes saved, so run consistency_check and retry the migration after resolving the error. Type changes are not supported. Cells of removed columns stay stored but are no longer listed; plain formula additions fill in on the next cell update, while constraint migrations recompute them immediately. Undo restores the previous schema.", false, true, s.Tables.Alter)
}
