package handlers

import (
	"encoding/json"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type column struct {
	ID, Label, Type, Formula string
	Editable, Numeric        bool
}
type cell struct {
	Value, Column, Type, Formula string
	Editable, Numeric            bool
}
type gridRow struct {
	ID      string
	Version int
	Cells   []cell
}
type pageData struct {
	IsImports, IsImportPreview, IsReconcile, IsSharedList, IsWorkflow, IsAccounts, IsBudgets, IsAudit, CanEditDashboard, IsShared bool
	Batches                                                                                                                       []imports.Batch
	ImportPreview                                                                                                                 imports.PreviewResult
	ImportBatchID, DashboardPath                                                                                                  string
	Reconciliation                                                                                                                []reconcile.AccountStatus
	ReceivedShares, OwnedShares                                                                                                   []sharing.Grant
	Feeds                                                                                                                         []imports.Feed

	User                                                                                                                                 identity.User
	Title, Description, Active, Error, CSRF, Version, TableID, GridTool, SpecJSON, DashboardID, DashboardStatus, Author, Note, InviteURL string
	Dashboards                                                                                                                           []dashboard.Head
	Tables                                                                                                                               []tables.Table
	GridColumns                                                                                                                          []column
	GridRows                                                                                                                             []gridRow
	Content                                                                                                                              template.HTML
	DraftVersion, PublishedVersion, CurrentVersion                                                                                       int
	IsDashboard, IsSettings, IsJournal, IsModel                                                                                          bool
	Agents                                                                                                                               []oauth.Grant
	Accounts                                                                                                                             []ledger.Account
	Today                                                                                                                                string
	Scenarios                                                                                                                            []model.Scenario
	ToolsJSON                                                                                                                            string
}

func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	p, session, e := h.session(r)
	if e != nil {
		needed, e := h.services.Identity.NeedsSetup(r.Context())
		if e == nil && needed {
			http.Redirect(w, r, "/setup", 303)
		} else {
			http.Redirect(w, r, "/login?next="+r.URL.EscapedPath(), 303)
		}
		return
	}
	u, e := h.services.Identity.User(r.Context(), p.UserID)
	if e != nil {
		http.Error(w, "account unavailable", 503)
		return
	}
	data := pageData{User: u, CSRF: session.CSRF, Version: h.cfg.Version, GridRows: []gridRow{}, Today: time.Now().Format("2006-01-02")}
	data.Dashboards, e = h.services.Dashboard.List(r.Context(), p)
	if e != nil {
		data.Error = "Could not load dashboards."
	}
	data.Tables, e = h.services.Tables.List(r.Context(), p)
	if e != nil {
		data.Error = "Could not load databases."
	}
	path := r.URL.Path
	if path == "/" && len(data.Dashboards) > 0 {
		path = "/dashboards/" + data.Dashboards[0].ID
	}
	if path == "/imports" || path == "/reconcile" || path == "/journal" {
		accounts, err := h.services.Ledger.Accounts(r.Context(), p, ledger.AccountsInput{Limit: 500})
		if err == nil {
			data.Accounts = accounts.Items
		}
	}
	switch {
	case path == "/imports":
		data.Title = "Imports"
		data.Active = "imports"
		data.IsImports = true
		data.IsWorkflow = true
		data.Description = "Stage statements, review duplicates and commit balanced entries."
		data.Batches, e = h.services.Imports.List(r.Context(), p)
		if e != nil {
			data.Error = e.Error()
		}
	case strings.HasPrefix(path, "/imports/"):
		data.Title = "Review import"
		data.Active = "imports"
		data.IsWorkflow = true
		data.IsImportPreview = true
		data.ImportBatchID = strings.TrimPrefix(path, "/imports/")
		cursor, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
		data.ImportPreview, e = h.services.Imports.Preview(r.Context(), p, data.ImportBatchID, cursor)
		if e != nil {
			data.Error = e.Error()
			data.IsImportPreview = false
		}
	case path == "/reconcile":
		data.Title = "Reconciliation"
		data.Active = "reconcile"
		data.IsReconcile = true
		data.IsWorkflow = true
		data.Description = "Compare the journal with known balances and resolve differences."
		data.Reconciliation, e = h.services.Reconcile.Status(r.Context(), p, "")
		if e != nil {
			data.Error = e.Error()
		}
	case path == "/shared":
		data.Title = "Shared with me"
		data.Active = "shared"
		data.IsSharedList = true
		data.IsWorkflow = true
		data.ReceivedShares, e = h.services.Sharing.ListReceived(r.Context(), p)
		if e != nil {
			data.Error = e.Error()
		}
		data.OwnedShares, e = h.services.Sharing.ListOwned(r.Context(), p, "")
		if e != nil {
			data.Error = e.Error()
		}
	case strings.HasPrefix(path, "/shared/"):
		id := strings.TrimPrefix(path, "/shared/")
		data.Active = "shared"
		data.IsShared = true
		data.IsDashboard = true
		data.DashboardID = id
		data.DashboardPath = "/shared/" + id
		view, err := h.services.Sharing.View(r.Context(), p, id)
		if err != nil {
			data.Error = err.Error()
			data.Title = "Shared dashboard unavailable"
			break
		}
		data.Title = view.Snapshot.Title
		data.CurrentVersion = view.Snapshot.Version
		data.PublishedVersion = view.Snapshot.Version
		data.DashboardStatus = "published"
		data.Content = template.HTML(view.Snapshot.Rendered.HTML)
		data.Description = "Shared " + view.Grant.Mode + " · " + view.Grant.Role
		if view.Grant.Role == "editor" {
			access, err := h.services.Sharing.Access(r.Context(), p, id, true)
			if err != nil {
				data.Error = err.Error()
				break
			}
			data.CanEditDashboard = true
			v, err := access.Dashboard.Get(r.Context(), access.Principal, access.ID, r.URL.Query().Get("version"))
			if err != nil {
				data.Error = err.Error()
				break
			}
			data.CurrentVersion = v.Version
			data.DashboardStatus = v.Status
			if v.Status == "draft" {
				data.DraftVersion = v.Version
			}
			data.Author = v.Author.Kind + ":" + v.Author.ID
			b, _ := json.MarshalIndent(v.Spec, "", "  ")
			data.SpecJSON = string(b)
			rendered, err := access.Dashboard.Preview(r.Context(), access.Principal, access.ID, r.URL.Query().Get("version"), nil)
			if err != nil {
				data.Error = err.Error()
			} else if !rendered.Validation.OK {
				data.Error = "This draft needs corrections before publishing."
				data.Content = ""
			} else {
				data.Content = template.HTML(rendered.HTML)
			}
		}

	case strings.HasPrefix(path, "/dashboards/"):
		id := strings.TrimPrefix(path, "/dashboards/")
		data.IsDashboard = true
		data.CanEditDashboard = true
		data.DashboardPath = "/dashboards/" + id
		data.DashboardID = id
		data.Active = "dash:" + id
		v, e := h.services.Dashboard.Get(r.Context(), p, id, r.URL.Query().Get("version"))
		if e != nil {
			data.Error = e.Error()
			data.Title = "Dashboard unavailable"
			break
		}
		data.Title = v.Spec.Title
		data.CurrentVersion = v.Version
		data.DashboardStatus = v.Status
		data.Author = v.Author.Kind + ":" + v.Author.ID
		data.Note = v.Note
		b, _ := json.MarshalIndent(v.Spec, "", "  ")
		data.SpecJSON = string(b)
		for _, head := range data.Dashboards {
			if head.ID == id {
				data.DraftVersion = head.DraftVersion
				data.PublishedVersion = head.PublishedVersion
			}
		}
		rendered, e := h.services.Dashboard.Preview(r.Context(), p, id, r.URL.Query().Get("version"), nil)
		if e != nil {
			data.Error = e.Error()
		} else if !rendered.Validation.OK {
			data.Error = "This version needs corrections before publishing. "
			for _, issue := range rendered.Validation.Errors {
				data.Error += issue.Path + ": " + issue.Msg + ". "
			}
		} else {
			data.Content = template.HTML(rendered.HTML)
		}
	case path == "/accounts":
		data.IsAccounts = true
		data.Title = "Accounts"
		data.Description = "Asset accounts and pots. Balances are derived from posted journal entries."
		data.Active = "accounts"
		rows, e := h.services.Sources.Rows(r.Context(), p, "accounts")
		if e != nil {
			data.Error = e.Error()
			break
		}
		data.GridColumns = []column{{ID: "account", Label: "Account"}, {ID: "pot", Label: "Pot"}, {ID: "commodity", Label: "Unit"}, {ID: "amount", Label: "Amount", Numeric: true}, {ID: "usd", Label: "Value USD", Numeric: true}}
		data.GridRows = mapRows(rows, data.GridColumns)
	case path == "/journal":
		data.Title = "Journal"
		data.Description = "Every money movement, with balanced postings and permanent history."
		data.Active = "journal"
		data.IsJournal = true
		page, e := h.services.Ledger.Entries(r.Context(), p, ledger.EntriesInput{Limit: 100})
		if e != nil {
			data.Error = e.Error()
			break
		}
		data.GridColumns = []column{{ID: "date", Label: "Date"}, {ID: "payee", Label: "Payee"}, {ID: "kind", Label: "Kind"}, {ID: "status", Label: "Status"}, {ID: "postings", Label: "Postings"}, {ID: "id", Label: "Entry ID"}}
		rows := []map[string]any{}
		for _, entry := range page.Items {
			postings := []string{}
			for _, v := range entry.Postings {
				postings = append(postings, v.Account+" "+v.Amount.Value.String()+" "+v.Amount.Commodity)
			}
			rows = append(rows, map[string]any{"id": entry.ID, "date": entry.Date, "payee": entry.Payee, "kind": entry.Kind, "status": entry.Status, "postings": strings.Join(postings, "; ")})
		}
		data.GridRows = mapRows(rows, data.GridColumns)
		accounts, e := h.services.Ledger.Accounts(r.Context(), p, ledger.AccountsInput{Limit: 500})
		if e == nil {
			data.Accounts = accounts.Items
		}
	case path == "/budgets" || path == "/databases/budget_lines":
		data.IsBudgets = true
		data.Title = "Budget lines"
		data.Description = "Edit a limit to recompute your dashboards. EUR amounts; actuals come from the journal."
		data.Active = "budgets"
		data.GridTool = "budget_set_line"
		status, e := h.services.Budget.Get(r.Context(), p, r.URL.Query().Get("month"))
		if e != nil {
			data.Error = e.Error()
			break
		}
		data.GridColumns = []column{{ID: "name", Label: "Line", Editable: true, Type: "string"}, {ID: "limit", Label: "Limit EUR", Editable: true, Numeric: true, Type: "number"}, {ID: "actual", Label: "Actual EUR", Numeric: true}, {ID: "avg6", Label: "6-mo average", Numeric: true}, {ID: "status", Label: "Status"}}
		rows := []map[string]any{}
		for _, l := range status.Lines {
			rows = append(rows, map[string]any{"id": l.ID, "name": l.Name, "limit": l.Limit, "actual": l.Actual, "avg6": l.Avg6, "status": l.Status})
		}
		data.GridRows = mapRows(rows, data.GridColumns)
	case strings.HasPrefix(path, "/databases/"):
		id := strings.TrimPrefix(path, "/databases/")
		data.TableID = id
		data.GridTool = "db_update_cells"
		data.Active = "table:" + id
		t, e := h.services.Tables.Get(r.Context(), p, id)
		if e != nil {
			data.Title = "Database unavailable"
			data.Error = e.Error()
			break
		}
		data.Title = t.Name
		data.Description = "Edit cells to recompute formulas and every dependent dashboard."
		for _, c := range t.Columns {
			data.GridColumns = append(data.GridColumns, column{ID: c.ID, Label: c.ID, Type: c.Type, Editable: c.Formula == "", Formula: c.Formula, Numeric: c.Type == "number"})
		}
		q, e := h.services.Tables.Query(r.Context(), p, tables.QueryInput{DB: id, Limit: 5000})
		if e != nil {
			data.Error = e.Error()
			break
		}
		rows := []map[string]any{}
		versions := map[string]int{}
		for _, row := range q.Rows {
			row.Cells["id"] = row.ID
			rows = append(rows, row.Cells)
			versions[row.ID] = row.Version
		}
		data.GridRows = mapRows(rows, data.GridColumns)
		for i := range data.GridRows {
			data.GridRows[i].Version = versions[data.GridRows[i].ID]
		}
	case path == "/scenarios":
		data.Title = "Scenarios"
		data.Active = "scenarios"
		data.Description = "Assumptions and what-ifs. These projections never change your journal."
		data.IsModel = true
		state, e := h.services.Model.Get(r.Context(), p)
		if e != nil {
			data.Error = e.Error()
			break
		}
		data.Scenarios = state.Scenarios
		ids := []string{}
		for _, v := range state.Scenarios {
			ids = append(ids, v.ID)
		}
		if len(ids) > 0 {
			series, e := h.services.Model.Forecast(r.Context(), p, model.ForecastInput{ScenarioIDs: ids, HorizonMonths: 12})
			if e != nil {
				data.Error = e.Error()
				break
			}
			data.GridColumns = []column{{ID: "name", Label: "Scenario"}, {ID: "spend", Label: "Monthly burn USD", Numeric: true}, {ID: "net", Label: "12-month net USD", Numeric: true}, {ID: "runway", Label: "Runway months", Numeric: true}}
			rows := []map[string]any{}
			for _, v := range series {
				runway := "—"
				if v.Runway != nil {
					runway = strconv.FormatFloat(*v.Runway, 'f', 1, 64)
				}
				rows = append(rows, map[string]any{"id": v.Scenario, "name": v.Name, "spend": v.SpendUSD, "net": v.EndNetUSD, "runway": runway})
			}
			data.GridRows = mapRows(rows, data.GridColumns)
		}
	case path == "/audit":
		data.IsAudit = true
		data.Title = "Activity"
		data.Active = "audit"
		data.Description = "Who changed what, with a permanent hash chain and workflow outcomes."
		events, e := h.services.Audit.List(r.Context(), p, audit.ListInput{})
		if e != nil {
			data.Error = e.Error()
			break
		}
		data.GridColumns = []column{{ID: "id", Label: "Event ID"}, {ID: "at", Label: "When"}, {ID: "actor", Label: "Actor"}, {ID: "tool", Label: "Action"}, {ID: "target", Label: "Target"}, {ID: "outcome", Label: "Outcome"}, {ID: "reason", Label: "Reason"}}
		rows := []map[string]any{}
		for _, v := range events.Events {
			status := "incomplete"
			if v.Outcome != nil {
				status = v.Outcome.Status
			}
			rows = append(rows, map[string]any{"id": v.ID, "at": v.At.Format("Jan 02 15:04"), "actor": v.Actor.Kind + ":" + v.Actor.ID, "tool": v.Tool, "target": v.Target.ID, "outcome": status, "reason": v.Reason})
		}
		data.GridRows = mapRows(rows, data.GridColumns)
	case path == "/settings":
		data.Title = "Settings"
		data.Active = "settings"
		data.IsSettings = true
		data.Description = "Your account and connected agents."
		data.Feeds, e = h.services.Imports.Feeds(r.Context(), p)
		if e != nil {
			data.Error = e.Error()
		}
		data.Agents, e = h.oauth.Grants(r.Context(), p)
		if e != nil {
			data.Error = e.Error()
		}
	case path == "/":
		data.Title = "Your money, in one place"
		data.Description = "Connect an agent to import your ledger, create a database or start with a dashboard template."
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if e = h.templates.ExecuteTemplate(w, "app.html", data); e != nil {
		h.services.Logger.Error("render application", "error", e)
	}
}
func mapRows(rows []map[string]any, columns []column) []gridRow {
	out := []gridRow{}
	for _, r := range rows {
		id, _ := r["id"].(string)
		row := gridRow{ID: id, Cells: []cell{}}
		for _, c := range columns {
			v := ""
			if value, ok := r[c.ID]; ok && value != nil {
				v = fmt.Sprint(value)
				if n, ok := value.(float64); ok {
					v = strconv.FormatFloat(n, 'f', -1, 64)
				}
			}
			row.Cells = append(row.Cells, cell{Value: v, Column: c.ID, Type: c.Type, Formula: c.Formula, Editable: c.Editable, Numeric: c.Numeric})
		}
		out = append(out, row)
	}
	return out
}
