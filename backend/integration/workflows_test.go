package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/classify"
	"github.com/olegpustovit/moneybag/backend/internal/components/consistency"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/history"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	previewcache "github.com/olegpustovit/moneybag/backend/internal/components/preview"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testProductWorkflows(t *testing.T, ctx context.Context, db *mongo.Database, pa, pb principal.Principal, book *ledger.Service, a *audit.Service, ids *identity.Service) {
	t.Helper()
	expr, e := expressions.New()
	if e != nil {
		t.Fatal(e)
	}
	prices := rates.NewService(rates.NewMongoRepository(db), a)
	tab := tables.NewService(tables.NewMongoRepository(db), a, expr)
	budgets := budget.NewService(budget.NewMongoRepository(db), a, prices)
	models := model.NewService(model.NewMongoRepository(db), tab, a, expr)
	for _, p := range []principal.Principal{pa, pb} {
		if e = models.Initialize(ctx, p); e != nil {
			t.Fatal("default model", e)
		}
	}
	classes := classify.NewService(classify.NewMongoRepository(db), book, a, expr)
	rec := reconcile.NewService(reconcile.NewMongoRepository(db), book, a)
	imp := imports.NewService(imports.NewMongoRepository(db), book, classes, rec, a)
	sources := &dashboard.Sources{Ledger: book, Budget: budgets, Model: models, Tables: tab, Valuer: prices, Rates: prices}
	dash := dashboard.NewService(dashboard.NewMongoRepository(db), &dashboard.Engine{Expr: expr, Reader: sources}, a)
	shares := sharing.NewService(sharing.NewMongoRepository(db), ids, dash, sources, expr, a)
	engine := &dashboard.Engine{Expr: expr, Reader: sources}
	// The initialized model has zero spending, so every scenario's runway is null.
	if r, e := engine.Evaluate(ctx, pa, "data.sc.map(s, s.runway == null)", map[string]dashboard.Query{"sc": {From: "scenarios"}}, nil); e != nil || fmt.Sprint(r.Value) != "[true]" {
		t.Fatal("scenario source with null runway", r.Value, e)
	}
	columns := []tables.Column{{ID: "qty", Type: "number"}, {ID: "price", Type: "number"}, {ID: "total", Type: "formula", Formula: "row.qty * row.price"}}
	for _, p := range []principal.Principal{pa, pb} {
		if _, e = tab.CreateNamed(ctx, p, "margin", tables.CreateInput{Name: "Margin", Columns: columns}); e != nil {
			t.Fatal(e)
		}
	}
	update, e := tab.Update(ctx, pa, tables.UpdateInput{DB: "margin", Updates: []tables.CellUpdate{{RowID: "one", Column: "qty", Value: float64(2)}, {RowID: "one", Column: "price", Value: float64(5)}}})
	if e != nil || len(update.Rows) != 1 || update.Rows[0].Cells["total"] != float64(10) {
		t.Fatal("formula did not recompute", update, e)
	}
	if _, e = tab.Update(ctx, pb, tables.UpdateInput{DB: "margin", Updates: []tables.CellUpdate{{RowID: "one", Column: "qty", Value: float64(99)}, {RowID: "one", Column: "price", Value: float64(10)}}}); e != nil {
		t.Fatal(e)
	}
	q, e := tab.Query(ctx, pa, tables.QueryInput{DB: "margin"})
	if e != nil || q.Rows[0].Cells["qty"] != float64(2) {
		t.Fatal("table users mixed", e)
	}
	stale := 0
	if _, e = tab.Update(ctx, pa, tables.UpdateInput{DB: "margin", Updates: []tables.CellUpdate{{RowID: "one", Column: "qty", Value: float64(3), Version: &stale}}}); !errors.Is(e, principal.ErrConflict) {
		t.Fatal("stale edit accepted", e)
	}
	amount := ledger.MustDecimal("100")
	name := "Food"
	accounts := []string{"expenses:food"}
	if _, e = budgets.Set(ctx, pa, budget.SetInput{ID: "food", Name: &name, Limit: &amount, Accounts: &accounts}); e != nil {
		t.Fatal(e)
	}
	csv := "Date,Amount,Currency,Description,ID,Balance\n2026-10-01,-20.00,EUR,Coffee,bank-1,-20.00\n2026-10-02,-30.00,EUR,Store,bank-2,-50.00\n"
	start, e := imp.Start(ctx, pa, imports.StartInput{Account: "assets:bank", Format: "generic_csv", ContentB64: base64.StdEncoding.EncodeToString([]byte(csv))})
	if e != nil {
		t.Fatal("stage", e)
	}
	preview, e := imp.Preview(ctx, pa, start.BatchID, 0)
	if e != nil || len(preview.Proposed) != 2 {
		t.Fatal("preview", preview, e)
	}
	if _, e = imp.Preview(ctx, pb, start.BatchID, 0); !errors.Is(e, principal.ErrNotFound) {
		t.Fatal("cross-user import access", e)
	}
	edit := imports.EditInput{BatchID: start.BatchID, RowID: preview.Proposed[0].ID}
	account := "expenses:food"
	edit.Set.Account = &account
	if _, e = imp.Edit(ctx, pa, edit); e != nil {
		t.Fatal(e)
	}
	posted, e := imp.Commit(ctx, pa, start.BatchID, true)
	if e != nil || posted.Posted != 2 {
		t.Fatal("commit", posted, e)
	}
	before, e := book.Entries(ctx, pa, ledger.EntriesInput{Limit: 500})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = imp.Commit(ctx, pa, start.BatchID, true); e != nil {
		t.Fatal("retry", e)
	}
	after, e := book.Entries(ctx, pa, ledger.EntriesInput{Limit: 500})
	if e != nil || len(before.Items) != len(after.Items) {
		t.Fatal("import retry duplicated journal entries", e)
	}
	feedServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-feed-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("Date,Payee,Amount,Currency\n30/09/2026,Old feed row,-1.00,EUR\n05/10/2026,Feed cafe,-4.20,EUR\n06/10/2026,Feed market,-8.00,EUR\n"))
	}))
	t.Cleanup(feedServer.Close)
	feedClient := feedServer.Client()
	// The certificate covers example.com; dial the local listener so the public-URL checks still apply.
	feedClient.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, feedServer.Listener.Addr().String())
	}
	imp.SetHTTPClient(feedClient)
	imp.SetSecretKey([]byte("0123456789abcdef0123456789abcdef"))
	feed := imports.FeedInput{ID: "finance_connector", URL: "https://example.com/statement.csv", Account: "assets:bank", Format: "generic_csv", DateFormat: "dmy", Token: "synthetic-feed-token"}
	if _, e = imp.ConfigureFeed(ctx, pa, feed); e != nil {
		t.Fatal(e)
	}
	synced, e := imp.Sync(ctx, pa, "finance_connector", "2026-10-01")
	if e != nil || synced.RowsParsed != 2 || synced.Period.From != "2026-10-05" {
		t.Fatal("feed sync", synced, e)
	}
	if _, e = imp.Sync(ctx, pb, "finance_connector", ""); e == nil {
		t.Fatal("feed configured for another user synced")
	}
	feed.Token = "wrong-token"
	if _, e = imp.ConfigureFeed(ctx, pa, feed); e != nil {
		t.Fatal(e)
	}
	if _, e = imp.Sync(ctx, pa, "finance_connector", ""); e == nil {
		t.Fatal("rejected credential synced")
	}
	rule, e := classes.Upsert(ctx, pa, classify.RuleInput{Priority: 1, Match: "row.payee == 'Store'", Set: classify.RuleSet{Account: "expenses:food", Tags: []string{"food"}}, Note: "Synthetic category correction"})
	if e != nil {
		t.Fatal(e)
	}
	changes, e := classes.Preview(ctx, pa, classify.PreviewInput{RuleID: rule.ID})
	if e != nil || changes.Count != 1 {
		t.Fatal("rule preview", changes, e)
	}
	applied, e := classes.Apply(ctx, pa, classify.PreviewInput{RuleID: rule.ID}, true)
	if e != nil || len(applied.Entries) != 1 {
		t.Fatal("rule apply", applied, e)
	}
	again, e := classes.Apply(ctx, pa, classify.PreviewInput{RuleID: rule.ID}, true)
	if e != nil || len(again.Entries) != 0 {
		t.Fatal("rule applied twice", again, e)
	}
	tagRule, e := classes.Upsert(ctx, pa, classify.RuleInput{Priority: 2, Match: "row.payee == 'Store'", Set: classify.RuleSet{Account: "expenses:food", Tags: []string{"reviewed"}}})
	if e != nil {
		t.Fatal(e)
	}
	tagPreview, e := classes.Preview(ctx, pa, classify.PreviewInput{RuleID: tagRule.ID})
	if e != nil || tagPreview.Count != 1 {
		t.Fatal("same-category tag preview", tagPreview, e)
	}
	tagApply, e := classes.Apply(ctx, pa, classify.PreviewInput{RuleID: tagRule.ID}, true)
	if e != nil || len(tagApply.Entries) != 0 || len(tagApply.EventIDs) != 1 {
		t.Fatal("tags-only rule", tagApply, e)
	}
	tagPreview, e = classes.Preview(ctx, pa, classify.PreviewInput{RuleID: tagRule.ID})
	if e != nil || tagPreview.Count != 0 {
		t.Fatal("tag rule repeated", tagPreview, e)
	}
	tagged, e := book.Entries(ctx, pa, ledger.EntriesInput{Tag: "food", Status: "posted"})
	if e != nil || len(tagged.Items) != 1 {
		t.Fatal("tag overlay missing", tagged, e)
	}
	var original ledger.Entry
	if e = db.Collection("journal").FindOne(ctx, bson.M{"_id": tagged.Items[0].ID, "user_id": pa.UserID}).Decode(&original); e != nil {
		t.Fatal(e)
	}
	if len(original.Tags) != 0 {
		t.Fatal("tag mutation changed immutable journal")
	}
	status, e := budgets.Get(ctx, pa, "2026-10")
	if e != nil || status.Total.Actual != 50 {
		t.Fatal("budget reclass double counted or omitted spend", status, e)
	}
	assertion, e := rec.Assert(ctx, pa, reconcile.AssertInput{Account: "assets:bank", Date: "2026-10-31", Amount: ledger.Amount{Value: ledger.MustDecimal("-55"), Commodity: "EUR"}, Source: "Synthetic statement"})
	if e != nil || assertion.OK || assertion.Diff.String() != "-5" {
		t.Fatal("assertion", assertion, e)
	}
	resolved, e := rec.Resolve(ctx, pa, reconcile.ResolveInput{DiscrepancyID: assertion.Assertion.ID, Action: "adjust", Reason: "Synthetic cash difference"})
	if e != nil || !resolved.Resolved || resolved.Adjustment == nil {
		t.Fatal("adjustment", resolved, e)
	}
	createShared := func(id, exprValue string, data map[string]dashboard.Query) {
		t.Helper()
		if _, e = dash.Create(ctx, pa, dashboard.CreateInput{ID: id, Title: id}); e != nil {
			t.Fatal(e)
		}
		spec := dashboard.Spec{ID: id, Title: id, Data: data, Pages: []dashboard.Page{{ID: "main", Title: "Overview", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 12, Component: "value"}}}}}}, Components: []dashboard.Component{{ID: "value", Type: "kpi", Value: exprValue}}}
		ops, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "", "value": spec}})
		draft, e := dash.Patch(ctx, pa, id, ops)
		if e != nil || !draft.Validation.OK {
			t.Fatal("draft", draft, e)
		}
		if _, e = dash.Publish(ctx, pa, id, draft.DraftVersion, "Synthetic publish"); e != nil {
			t.Fatal(e)
		}
	}
	createShared("fixed", "42", map[string]dashboard.Query{})
	if _, e = dash.Add(ctx, pa, dashboard.AddInput{DashboardID: "fixed", Page: "details", Component: dashboard.Component{ID: "note", Type: "text", MD: "extra"}}); e != nil {
		t.Fatal("add to a new page", e)
	}
	if added, e := dash.Get(ctx, pa, "fixed", "draft"); e != nil || len(added.Spec.Pages) != 2 || added.Spec.Pages[1].Layout[0].Cols[0].Component != "note" {
		t.Fatal("new page layout", added, e)
	}
	if _, e = shares.Share(ctx, pa, sharing.ShareInput{DashboardID: "fixed", Grantee: "missing@example.test", Role: "viewer"}); !errors.Is(e, principal.ErrNotFound) || !strings.Contains(e.Error(), "grantee") {
		t.Fatal("missing grantee error", e)
	}
	snap, e := shares.Share(ctx, pa, sharing.ShareInput{DashboardID: "fixed", Grantee: pb.UserID, Role: "viewer"})
	if e != nil {
		t.Fatal(e)
	}
	view, e := shares.View(ctx, pb, snap.ShareID)
	if e != nil || view.Snapshot.Rendered.Values["value"] != float64(42) {
		t.Fatal("snapshot share", view, e)
	}
	if _, e = shares.Access(ctx, pb, snap.ShareID, true); !errors.Is(e, principal.ErrForbidden) {
		t.Fatal("viewer can edit", e)
	}
	if _, e = dash.Get(ctx, pb, "fixed", ""); !errors.Is(e, principal.ErrNotFound) {
		t.Fatal("grantee directly accessed owner's spec", e)
	}
	createShared("live", "data.items[0].total", map[string]dashboard.Query{"items": {From: "table:margin"}})
	live, e := shares.Share(ctx, pa, sharing.ShareInput{DashboardID: "live", Grantee: pb.UserID, Role: "editor", Mode: "live", Sources: []string{"table:margin"}, RowFilter: "row.qty >= 2.0"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tab.Update(ctx, pa, tables.UpdateInput{DB: "margin", Updates: []tables.CellUpdate{{RowID: "one", Column: "price", Value: float64(10)}}}); e != nil {
		t.Fatal(e)
	}
	view, e = shares.View(ctx, pb, live.ShareID)
	if e != nil || view.Snapshot.Rendered.Values["value"] != float64(20) {
		t.Fatal("live share used wrong owner or stale values", view, e)
	}
	access, e := shares.Access(ctx, pb, live.ShareID, true)
	if e != nil {
		t.Fatal(e)
	}
	ops := json.RawMessage(`[{"op":"add","path":"/data/raw","value":{"from":"journal"}}]`)
	bad, e := access.Dashboard.Patch(ctx, access.Principal, access.ID, ops)
	if e != nil {
		t.Fatal(e)
	}
	if bad.Validation.OK {
		t.Fatal("editor read outside granted sources")
	}
	if _, e = access.Dashboard.Publish(ctx, access.Principal, access.ID, bad.DraftVersion, "Forbidden source"); e == nil || !strings.Contains(e.Error(), "/data/raw") {
		t.Fatal("invalid editor draft published or gate hid the reason", e)
	}
	fixed, e := access.Dashboard.Patch(ctx, access.Principal, access.ID, json.RawMessage(`[{"op":"remove","path":"/data/raw"}]`))
	if e != nil || !fixed.Validation.OK {
		t.Fatal("removed query still executes", fixed, e)
	}
	if _, e = shares.Share(ctx, pb, sharing.ShareInput{DashboardID: "live", Grantee: pa.UserID, Role: "viewer"}); e == nil {
		t.Fatal("editor reshared owner data")
	}
	if e = shares.Unshare(ctx, pa, live.ShareID); e != nil {
		t.Fatal(e)
	}
	if _, e = shares.View(ctx, pb, live.ShareID); !errors.Is(e, principal.ErrNotFound) {
		t.Fatal("revoked grant still works", e)
	}
	hist := &history.Service{Audit: a, Ledger: book, Budget: budgets, Classify: classes, Dashboard: dash, Imports: imp, Model: models, Rates: prices, Sharing: shares, Tables: tab, Reconcile: rec}
	createShared("undo-publish", "1", map[string]dashboard.Query{})
	edited, e := dash.Add(ctx, pa, dashboard.AddInput{DashboardID: "undo-publish", Component: dashboard.Component{ID: "note", Type: "text", MD: "v2"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = dash.Publish(ctx, pa, "undo-publish", edited.DraftVersion, "Publish edit"); e != nil {
		t.Fatal(e)
	}
	published, e := a.List(ctx, pa, audit.ListInput{Tool: "dashboard_publish", Target: "undo-publish", Limit: 1})
	if e != nil || len(published.Events) != 1 {
		t.Fatal("publish event", published, e)
	}
	if _, e = hist.Undo(ctx, pa, history.UndoInput{EventID: published.Events[0].ID, Reason: "Undo publish"}); e != nil {
		t.Fatal(e)
	}
	if draft, e := dash.Get(ctx, pa, "undo-publish", "draft"); e != nil || draft.Version != edited.DraftVersion || draft.Status != "draft" {
		t.Fatal("undoing a publish should restore its draft", draft.Version, draft.Status, e)
	}
	ro := pa
	ro.Scopes = []string{"ledger:read"}
	if _, e = hist.Undo(ctx, ro, history.UndoInput{EventID: update.EventID, Reason: "Scope test"}); !errors.Is(e, principal.ErrForbidden) {
		t.Fatal("read-only undo accepted", e)
	}
	if _, e = hist.Undo(ctx, pb, history.UndoInput{EventID: update.EventID, Reason: "Isolation test"}); !errors.Is(e, principal.ErrNotFound) {
		t.Fatal("cross-user undo accepted", e)
	}
	edit2, e := tab.Update(ctx, pa, tables.UpdateInput{DB: "margin", Updates: []tables.CellUpdate{{RowID: "one", Column: "price", Value: float64(30)}}})
	if e != nil {
		t.Fatal(e)
	}
	inverse, e := hist.Undo(ctx, pa, history.UndoInput{EventID: edit2.EventID, Reason: "Synthetic correction"})
	if e != nil || len(inverse.InverseEventIDs) != 1 {
		t.Fatal("cell undo", inverse, e)
	}
	q, e = tab.Query(ctx, pa, tables.QueryInput{DB: "margin"})
	if e != nil || q.Rows[0].Cells["total"] != float64(20) {
		t.Fatal("undo did not restore/recompute", q, e)
	}
	redo, e := hist.Undo(ctx, pa, history.UndoInput{EventID: inverse.InverseEventIDs[0], Reason: "Restore edit"})
	if e != nil || len(redo.InverseEventIDs) != 1 {
		t.Fatal("inverse undo", redo, e)
	}
	q, e = tab.Query(ctx, pa, tables.QueryInput{DB: "margin"})
	if e != nil || q.Rows[0].Cells["total"] != float64(60) {
		t.Fatal("redo cell", q, e)
	}
	createRow, e := tab.Update(ctx, pa, tables.UpdateInput{DB: "margin", Updates: []tables.CellUpdate{{RowID: "temporary", Column: "qty", Value: float64(1)}, {RowID: "temporary", Column: "price", Value: float64(5)}}})
	if e != nil {
		t.Fatal(e)
	}
	inverse, e = hist.Undo(ctx, pa, history.UndoInput{EventID: createRow.EventID, Reason: "Remove created row"})
	if e != nil {
		t.Fatal(e)
	}
	q, e = tab.Query(ctx, pa, tables.QueryInput{DB: "margin"})
	if e != nil || len(q.Rows) != 1 {
		t.Fatal("created row undo", q, e)
	}
	if _, e = hist.Undo(ctx, pa, history.UndoInput{EventID: inverse.InverseEventIDs[0], Reason: "Restore created row"}); e != nil {
		t.Fatal(e)
	}
	q, e = tab.Query(ctx, pa, tables.QueryInput{DB: "margin"})
	if e != nil || len(q.Rows) != 2 {
		t.Fatal("restore created row", q, e)
	}
	rawCount := db.Collection("import_rows")
	reversals, e := imp.Undo(ctx, pa, start.BatchID, "Synthetic import correction")
	if e != nil || len(reversals) != 2 {
		t.Fatal("import undo", reversals, e)
	}
	if _, e = imp.Undo(ctx, pa, start.BatchID, "Retry import undo"); e != nil {
		t.Fatal("import undo retry", e)
	}
	if undone, e := rec.Status(ctx, pa, "assets:bank"); e != nil || len(undone) == 0 || len(undone[0].Discrepancies) == 0 {
		t.Fatal("status after import undo should show the broken closing balance", undone, e)
	}
	if _, e = imp.Redo(ctx, pa, start.BatchID, "Restore import"); e != nil {
		t.Fatal("import redo", e)
	}
	afterRedo, e := classes.Preview(ctx, pa, classify.PreviewInput{RuleID: tagRule.ID})
	if e != nil || afterRedo.Count != 0 {
		t.Fatal("reversal chain changed effective categories", afterRedo, e)
	}
	n, e := rawCount.CountDocuments(ctx, bson.M{"user_id": pa.UserID, "batch_id": start.BatchID})
	if e != nil || n != 2 {
		t.Fatal("import raw provenance lost", n, e)
	}
	state, e := rec.Status(ctx, pa, "assets:bank")
	if e != nil || len(state) == 0 || state[0].LastOK != "2026-10-31" {
		t.Fatal("resolved assertion was stale", state, e)
	}
	testMCPWorkflows(t, ctx, pa, pb, mcpserver.Services{History: hist, Identity: ids, Ledger: book, Audit: a, Rates: prices, Budget: budgets, Tables: tab, Model: models, Dashboard: dash, Expr: expr, Sources: sources, Classify: classes, Imports: imp, Reconcile: rec, Sharing: shares, Logger: slog.Default(), Consistency: &consistency.Service{Ledger: book, Reconcile: rec, Dashboard: dash, Model: models, Tables: tab, Budget: budgets, Rates: prices}}, db)
	draftEntry, e := book.Post(ctx, pa, ledger.EntryInput{Date: "2026-10-28", Payee: "Draft commit test", Postings: []ledger.Posting{{Account: "assets:bank", Amount: ledger.Amount{Value: ledger.MustDecimal("-1"), Commodity: "EUR"}}, {Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal("1"), Commodity: "EUR"}}}})
	if e != nil {
		t.Fatal(e)
	}
	editedDraft, e := book.Post(ctx, pa, ledger.EntryInput{ID: draftEntry.Entry.ID, Date: draftEntry.Entry.Date, Payee: "Edited draft", Postings: draftEntry.Entry.Postings})
	if e != nil || editedDraft.Entry.ID != draftEntry.Entry.ID || editedDraft.Entry.Status != "draft" {
		t.Fatal("draft edit", editedDraft, e)
	}
	postedDraft, e := book.Post(ctx, pa, ledger.EntryInput{ID: draftEntry.Entry.ID, Commit: true})
	if e != nil || postedDraft.Entry.Status != "posted" {
		t.Fatal("saved draft commit", postedDraft, e)
	}
	if _, e = book.Post(ctx, pa, ledger.EntryInput{ID: draftEntry.Entry.ID, Commit: true}); e == nil {
		t.Fatal("posted draft committed twice")
	}
	fixedGrant, e := shares.Resolve(ctx, pb, snap.ShareID)
	if e != nil {
		t.Fatal(e)
	}
	previews := previewcache.New("http://localhost:6459", "", nil, shares)
	previewURL, _, e := previews.Create(pb, "<p>Snapshot</p>", &fixedGrant)
	if e != nil {
		t.Fatal(e)
	}
	previewID := strings.TrimSuffix(strings.TrimPrefix(previewURL, "http://localhost:6459/previews/"), ".html")
	if _, e = previews.Read(ctx, pb, previewID, false); e != nil {
		t.Fatal(e)
	}
	if e = shares.Unshare(ctx, pa, snap.ShareID); e != nil {
		t.Fatal(e)
	}
	if _, e = previews.Read(ctx, pb, previewID, false); !errors.Is(e, principal.ErrNotFound) {
		t.Fatal("revoked share preview still readable", e)
	}
	testCostLots(t, ctx, pa, pb, book, prices)
	if e = shares.RefreshDue(ctx); e != nil {
		t.Fatal("snapshot maintenance", e)
	}
	// Undoing an overwrite must restore an archived record's visibility too.
	if e = budgets.Archive(ctx, pa, "food", true, "Hide line"); e != nil {
		t.Fatal(e)
	}
	changed, e := budgets.Set(ctx, pa, budget.SetInput{ID: "food", Name: &name, Limit: &amount, Accounts: &accounts})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = hist.Undo(ctx, pa, history.UndoInput{EventID: changed.EventID, Reason: "Restore hidden line"}); e != nil {
		t.Fatal(e)
	}
	hidden, e := budgets.Get(ctx, pa, "2026-10")
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range hidden.Lines {
		if line.ID == "food" {
			t.Fatal("undo resurrected archived budget", hidden)
		}
	}
	if e = book.Verify(ctx, pa); e != nil {
		t.Fatal("final invariants", e)
	}
}

func testCostLots(t *testing.T, ctx context.Context, pa, pb principal.Principal, book *ledger.Service, prices *rates.Service) {
	t.Helper()
	for _, p := range []principal.Principal{pa, pb} {
		for _, a := range []ledger.Account{{ID: "assets:gold", Type: "asset", Name: "Gold", Commodities: []string{"XAU_g"}}, {ID: "equity:gold", Type: "equity", Name: "Gold opening", Commodities: []string{"XAU_g"}}} {
			if _, e := book.CreateAccount(ctx, p, a); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e := prices.Set(ctx, pa, rates.SetInput{Pair: "XAU_g/USD", Value: ledger.MustDecimal("100"), At: "2026-10-01T00:00:00Z", Source: "Synthetic quote"}); e != nil {
		t.Fatal(e)
	}
	if inv, e := prices.Get(ctx, pa, []string{"USD/XAU_g"}, "2026-10-02T00:00:00Z"); e != nil || inv[0].Value.String() != "0.01" || inv[0].Pair != "USD/XAU_g" {
		t.Fatal("inverse quote", inv, e)
	}
	cost := ledger.Amount{Value: ledger.MustDecimal("60"), Commodity: "USD"}
	entry, e := book.Post(ctx, pa, ledger.EntryInput{Date: "2026-10-01", Commit: true, Postings: []ledger.Posting{{Account: "assets:gold", Amount: ledger.Amount{Value: ledger.MustDecimal("10"), Commodity: "XAU_g"}, Cost: &cost}, {Account: "equity:gold", Amount: ledger.Amount{Value: ledger.MustDecimal("-10"), Commodity: "XAU_g"}}}})
	if e != nil {
		t.Fatal(e)
	}
	in := ledger.BalancesInput{At: "2026-10-28", Accounts: []string{"assets:gold"}, ValueIn: "USD", IncludeLots: true}
	result, e := book.Balances(ctx, pa, in, prices)
	if e != nil || len(result.Lots) != 1 {
		t.Fatal(result, e)
	}
	lot := result.Lots[0]
	if lot.CostBasis == nil || lot.CostBasis.Value.String() != "600" || lot.UnrealizedPL == nil || lot.UnrealizedPL.Value.String() != "400" {
		t.Fatal("cost valuation", lot)
	}
	other, e := book.Balances(ctx, pb, in, prices)
	if e != nil || len(other.Lots) != 0 {
		t.Fatal("cost lots crossed users", other, e)
	}
	if _, e = book.Reverse(ctx, pa, ledger.ReverseInput{ID: entry.Entry.ID, Reason: "Synthetic correction"}); e != nil {
		t.Fatal(e)
	}
	result, e = book.Balances(ctx, pa, in, prices)
	if e != nil || len(result.Lots) != 0 {
		t.Fatal("reversal changed cost basis", result, e)
	}
}
