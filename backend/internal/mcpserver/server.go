package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/classify"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/history"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/preview"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"log/slog"
	"reflect"
	"strconv"
)

type Services struct {
	Previews  *preview.Service
	History   *history.Service
	Classify  *classify.Service
	Imports   *imports.Service
	Reconcile *reconcile.Service
	Sharing   *sharing.Service
	Identity  *identity.Service
	Ledger    *ledger.Service
	Audit     *audit.Service
	Rates     *rates.Service
	Budget    *budget.Service
	Tables    *tables.Service
	Model     *model.Service
	Dashboard *dashboard.Service
	Expr      *expressions.Engine
	Sources   dashboard.Reader
	Logger    *slog.Logger
}
type Options struct {
	ReadOnly bool
	Toolsets map[string]bool
	Version  string
}
type registration struct {
	server  *mcp.Server
	options Options
	logger  *slog.Logger
}
type Empty struct{}
type IDInput struct {
	ID string `json:"id"`
}
type DashboardInput struct {
	DashboardID string `json:"dashboard_id"`
	Version     any    `json:"version,omitempty"`
}
type GetDashboardInput struct {
	ID      string `json:"id"`
	Version any    `json:"version,omitempty"`
}
type PreviewInput struct {
	DashboardID string         `json:"dashboard_id"`
	Version     any            `json:"version,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
}

func versionRef(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	switch n := v.(type) {
	case string:
		if n == "draft" || n == "published" {
			return n, nil
		}
		if i, e := strconv.Atoi(n); e == nil && i > 0 {
			return n, nil
		}
	case float64:
		if n > 0 && n == float64(int(n)) {
			return strconv.Itoa(int(n)), nil
		}
	case int:
		if n > 0 {
			return strconv.Itoa(n), nil
		}
	}
	return "", fmt.Errorf("version must be draft, published or a positive integer")
}
func add[In, Out any](r registration, name, set, scope, description string, readOnly, destructive bool, fn func(context.Context, principal.Principal, In) (Out, error)) {
	if r.options.ReadOnly && !readOnly {
		return
	}
	if len(r.options.Toolsets) > 0 && !r.options.Toolsets[set] {
		return
	}
	open := name == "rates_refresh" || name == "import_start" || name == "feeds_sync"
	inputSchema, err := schema[In]()
	if err != nil {
		panic(err)
	}
	outputSchema, err := schema[Out]()
	if err != nil {
		panic(err)
	}
	mcp.AddTool(r.server, &mcp.Tool{Name: name, Description: description, InputSchema: inputSchema, OutputSchema: outputSchema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: &destructive, OpenWorldHint: &open}}, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		p, e := principal.From(ctx)
		if e == nil {
			e = p.Require(scope)
		}
		if e != nil {
			return nil, zero, e
		}
		out, e := fn(ctx, p, in)
		if e != nil {
			var command mongo.CommandError
			var write mongo.WriteException
			if errors.As(e, &command) || errors.As(e, &write) || mongo.IsNetworkError(e) {
				r.logger.Error("MCP workflow failed", "tool", name, "error", e)
				e = fmt.Errorf("storage operation failed; inspect audit history before retrying a write")
			}
			if errors.Is(e, context.DeadlineExceeded) || errors.Is(e, context.Canceled) {
				e = fmt.Errorf("operation interrupted; inspect history before retrying a write")
			}
			return nil, zero, e
		}
		return nil, out, nil
	})
}
func schema[T any]() (*jsonschema.Schema, error) {
	return jsonschema.ForType(reflect.TypeFor[T](), &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[ledger.Decimal](): {Type: "string", Pattern: `^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`}, reflect.TypeFor[json.RawMessage](): {}}})
}
func New(s Services, opts Options) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "moneybag", Version: opts.Version}, &mcp.ServerOptions{Logger: s.Logger, Instructions: "Moneybag is your personal double-entry ledger. Use ledger tools to read and post money movements, import_* for statements, model tools for forecasts, and dashboard_* to build views. Ledger writes create drafts unless commit=true. dashboard_publish goes live immediately when validation passes. Posted entries are immutable; use reversals to correct them. One shared database scopes every record to the authenticated user. Amounts are decimal strings. Discover account IDs before posting. Review tool errors and audit history before retrying ambiguous writes."})
	r := registration{server, opts, s.Logger}
	add(r, "whoami", "account", "ledger:read", "Use this when you need to know whose ledger you are acting on and your scopes and actor identity.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		User   identity.User   `json:"user"`
		Scopes []string        `json:"scopes"`
		Actor  principal.Actor `json:"actor"`
	}, error) {
		u, e := s.Identity.User(ctx, p.UserID)
		return struct {
			User   identity.User   `json:"user"`
			Scopes []string        `json:"scopes"`
			Actor  principal.Actor `json:"actor"`
		}{u, p.Scopes, p.Actor}, e
	})
	add(r, "accounts_list", "ledger", "ledger:read", "Use this when you need the chart of accounts or pots. Page with next_cursor; limit is 1..500.", true, false, s.Ledger.Accounts)
	add(r, "account_create", "ledger", "ledger:write", "Use this when a new bank account, cash pot or expense category is needed. IDs use assets:, liabilities:, expenses:, income: or equity: prefixes. Money amounts use declared commodities.", false, false, s.Ledger.CreateAccount)
	add(r, "account_update", "ledger", "ledger:write", "Use this when renaming, regrouping or closing an account. Omitted fields remain unchanged; empty pot or location clears them. History is retained.", false, false, s.Ledger.UpdateAccount)
	add(r, "entries_list", "ledger", "ledger:read", "Use this when you need transactions matching filters. Results are scoped to your user, newest sequence first, with next_cursor; limit 1..500.", true, false, s.Ledger.Entries)
	add(r, "entry_get", "ledger", "ledger:read", "Use this when you need one entry with provenance links and audit history. Obtain its id from entries_list.", true, false, func(ctx context.Context, p principal.Principal, in IDInput) (struct {
		Entry ledger.Entry  `json:"entry"`
		Links []ledger.Link `json:"links"`
		Audit []audit.Event `json:"audit"`
	}, error) {
		v, e := s.Ledger.Get(ctx, p, in.ID)
		if e != nil {
			return struct {
				Entry ledger.Entry  `json:"entry"`
				Links []ledger.Link `json:"links"`
				Audit []audit.Event `json:"audit"`
			}{}, e
		}
		a, e := s.Audit.List(ctx, p, audit.ListInput{Target: in.ID})
		return struct {
			Entry ledger.Entry  `json:"entry"`
			Links []ledger.Link `json:"links"`
			Audit []audit.Event `json:"audit"`
		}{v, v.Links, a.Events}, e
	})
	add(r, "entry_post", "ledger", "ledger:write", "Use this when recording a money movement. Amounts are {v:decimal string,c:commodity}; postings must balance separately per commodity. Defaults to draft; commit=true posts it. To edit a saved draft, include id and its complete entry body with commit=false. To commit it, supply only id and commit=true.", false, false, s.Ledger.Post)
	add(r, "entry_reverse", "ledger", "ledger:write", "Use this when a posted entry is wrong. Requires a reason; appends a reversal and optional replacement. Never changes the original entry.", false, false, s.Ledger.Reverse)
	add(r, "transfer_record", "ledger", "ledger:write", "Use this when moving money between your own accounts or exchanging currency. Exchanges use equity:conversion; fees use expenses:fees. Creates a posted entry.", false, false, s.Ledger.Transfer)
	add(r, "balances_get", "ledger", "ledger:read", "Use this when you need balances or net worth as of YYYY-MM-DD. Group by account, pot, commodity or type; value_in optionally USD or EUR uses historical quotes. include_lots adds asset holdings grouped by explicit per-unit cost, cost basis and unrealized_pl. Annotate both acquisitions and disposals with cost; missing costs remain unknown.", true, false, func(ctx context.Context, p principal.Principal, in ledger.BalancesInput) (ledger.BalancesResult, error) {
		return s.Ledger.Balances(ctx, p, in, s.Rates)
	})
	add(r, "rates_get", "rates", "ledger:read", "Use this when valuing a currency, gold or security. Supply at most 50 BASE/QUOTE pairs and optional RFC3339 date; returns latest quotes at or before that date.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		Pairs []string `json:"pairs"`
		At    string   `json:"at,omitempty"`
	}) (struct {
		Quotes []rates.Quote `json:"quotes"`
	}, error) {
		q, e := s.Rates.Get(ctx, p, in.Pairs, in.At)
		return struct {
			Quotes []rates.Quote `json:"quotes"`
		}{q}, e
	})
	add(r, "rate_set", "rates", "ledger:write", "Use this when recording a manual FX, metal or security quote. Positive decimal value, BASE/QUOTE pair, RFC3339 at and source are required.", false, false, s.Rates.Set)
	type refreshed struct {
		Written int           `json:"written"`
		Latest  []rates.Quote `json:"latest"`
	}
	add(r, "rates_refresh", "rates", "ledger:write", "Use this when FX quotes are stale. Backfills ECB daily reference rates for the last 90 days; returns how many quotes were new or changed and the newest day among them. Manual metal/security prices use rate_set.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		Sources []string `json:"sources,omitempty"`
	}) (refreshed, error) {
		for _, source := range in.Sources {
			if source != "ecb" {
				return refreshed{}, fmt.Errorf("feed %q is not configured; use rate_set for manual quotes", source)
			}
		}
		q, e := s.Rates.RefreshECB(ctx, p)
		out := refreshed{Written: len(q), Latest: []rates.Quote{}}
		newest := ""
		for _, x := range q {
			newest = max(newest, x.At)
		}
		for _, x := range q {
			if x.At == newest {
				out.Latest = append(out.Latest, x)
			}
		}
		return out, e
	})
	add(r, "budget_get", "budget", "ledger:read", "Use this when comparing EUR spending with your budget for YYYY-MM. Includes current actuals and the previous six complete calendar months' average.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		Month string `json:"month,omitempty"`
	}) (budget.Status, error) {
		return s.Budget.Get(ctx, p, in.Month)
	})
	add(r, "budget_set_line", "budget", "ledger:write", "Use this when changing an EUR budget limit, label or expense-account mapping. Omitted fields remain unchanged; empty accounts clears mapping.", false, false, s.Budget.Set)
	add(r, "db_list", "tables", "ledger:read", "Use this when you need your user databases and column schemas. These logical tables all live in the single shared database.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Items []tables.Table `json:"items"`
	}, error) {
		v, e := s.Tables.List(ctx, p)
		return struct {
			Items []tables.Table `json:"items"`
		}{v}, e
	})
	add(r, "db_query", "tables", "ledger:read", "Use this when reading your database rows. db is a logical table ID from db_list, where is CEL over row, sort uses +/-column names, limit 1..5000.", true, false, s.Tables.Query)
	add(r, "db_create", "tables", "model:write", "Use this when you need a new user database. Columns have unique id and type number/string/bool/date/formula. Formula columns require CEL and are read-only cells.", false, false, s.Tables.Create)
	add(r, "db_update_cells", "tables", "model:write", "Use this when editing assumptions or cells. Row IDs may create rows; updates are type-checked and formulas recomputed. Optional version rejects stale edits.", false, false, s.Tables.Update)
	add(r, "model_get", "model", "ledger:read", "Use this when you need assumptions, income streams and scenarios before forecasting.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (model.State, error) {
		return s.Model.Get(ctx, p)
	})
	add(r, "scenario_upsert", "model", "model:write", "Use this when defining a what-if scenario. Overrides each name an assumption with either numeric value or CEL expr over params. Events change cash in a specified future month.", false, false, s.Model.Upsert)
	add(r, "forecast_run", "model", "ledger:read", "Use this when projecting scenarios. Supply 1..20 scenario IDs and horizon_months 1..600. value_in is USD or EUR; the original tracker projection engine is preserved for migrated scenarios.", true, false, func(ctx context.Context, p principal.Principal, in model.ForecastInput) (struct {
		Series []model.Projection `json:"series"`
	}, error) {
		v, e := s.Model.Forecast(ctx, p, in)
		return struct {
			Series []model.Projection `json:"series"`
		}{v}, e
	})
	add(r, "income_model_set", "model", "model:write", "Use this when salary or side income changes. Replaces streams; amount is per declared frequency, currency is USD/EUR, start/end are optional YYYY-MM-DD.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		Streams []model.Income `json:"streams"`
	}) (struct {
		Streams []model.Income `json:"streams"`
	}, error) {
		v, e := s.Model.SetIncome(ctx, p, in.Streams)
		return struct {
			Streams []model.Income `json:"streams"`
		}{v}, e
	})
	add(r, "dashboard_list", "dashboard", "ledger:read", "Use this when choosing a dashboard. Returns published/draft head versions.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Items []dashboard.Head `json:"items"`
	}, error) {
		v, e := s.Dashboard.List(ctx, p)
		return struct {
			Items []dashboard.Head `json:"items"`
		}{v}, e
	})
	add(r, "dashboard_get", "dashboard", "ledger:read", "Use this when reading a dashboard spec. version is a number, draft or published; omitted selects the current draft, otherwise published.", true, false, func(ctx context.Context, p principal.Principal, in GetDashboardInput) (dashboard.GetResult, error) {
		v, e := versionRef(in.Version)
		if e != nil {
			return dashboard.GetResult{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.ID, false)
		if e != nil {
			return dashboard.GetResult{}, e
		}
		return access.Dashboard.Get(ctx, access.Principal, access.ID, v)
	})
	add(r, "dashboard_create", "dashboard", "dash:write", "Use this when creating a dashboard draft. Optional from_template is budget-vs-actual or forecast. It belongs to your authenticated user.", false, false, s.Dashboard.Create)
	add(r, "dashboard_add_component", "dashboard", "dash:write", "Use this when adding a KPI, table, chart, budget_bars, scenario_compare or text note. New components append a layout row by default; a missing named page is added. Spans are 1..12.", false, false, func(ctx context.Context, p principal.Principal, in dashboard.AddInput) (dashboard.EditResult, error) {
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, true)
		if e != nil {
			return dashboard.EditResult{}, e
		}
		in.DashboardID = access.ID
		return access.Dashboard.Add(ctx, access.Principal, in)
	})
	add(r, "dashboard_update_component", "dashboard", "dash:write", "Use this when changing fields on one draft component. set is a partial component; its id cannot change. Creates a new immutable draft version.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string         `json:"dashboard_id"`
		ComponentID string         `json:"component_id"`
		Set         map[string]any `json:"set"`
	}) (dashboard.EditResult, error) {
		b, e := json.Marshal(in.Set)
		if e != nil {
			return dashboard.EditResult{}, e
		}
		var set map[string]json.RawMessage
		if e = json.Unmarshal(b, &set); e != nil {
			return dashboard.EditResult{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, true)
		if e != nil {
			return dashboard.EditResult{}, e
		}
		return access.Dashboard.UpdateComponent(ctx, access.Principal, access.ID, in.ComponentID, set)
	})
	add(r, "dashboard_remove_component", "dashboard", "dash:write", "Use this when removing a component and its layout placements from a new draft version. Published output is unchanged until publish.", false, true, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string `json:"dashboard_id"`
		ComponentID string `json:"component_id"`
	}) (dashboard.EditResult, error) {
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, true)
		if e != nil {
			return dashboard.EditResult{}, e
		}
		return access.Dashboard.Remove(ctx, access.Principal, access.ID, in.ComponentID)
	})
	add(r, "dashboard_compose", "dashboard", "dash:write", "Use this when rearranging a page's 12-column layout. A missing named page is added. Each row contains cols with span and component ID.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string          `json:"dashboard_id"`
		Page        string          `json:"page,omitempty"`
		Layout      []dashboard.Row `json:"layout"`
	}) (dashboard.EditResult, error) {
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, true)
		if e != nil {
			return dashboard.EditResult{}, e
		}
		return access.Dashboard.Compose(ctx, access.Principal, access.ID, in.Page, in.Layout)
	})
	add(r, "dashboard_patch", "dashboard", "dash:write", "Use this for RFC6902 JSON Patch edits to a dashboard draft. At most 200 operations and 256 KiB; unknown spec fields are rejected.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string           `json:"dashboard_id"`
		Ops         []map[string]any `json:"ops"`
	}) (dashboard.EditResult, error) {
		b, e := json.Marshal(in.Ops)
		if e != nil {
			return dashboard.EditResult{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, true)
		if e != nil {
			return dashboard.EditResult{}, e
		}
		return access.Dashboard.Patch(ctx, access.Principal, access.ID, b)
	})
	add(r, "dashboard_validate", "dashboard", "ledger:read", "Use this before preview or publish. Compiles every CEL expression, evaluates all rows, checks layout references and renders every component within the server budget.", true, false, func(ctx context.Context, p principal.Principal, in DashboardInput) (dashboard.Validation, error) {
		v, e := versionRef(in.Version)
		if e != nil {
			return dashboard.Validation{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, false)
		if e != nil {
			return dashboard.Validation{}, e
		}
		r, e := access.Dashboard.Preview(ctx, access.Principal, access.ID, v, nil)
		return r.Validation, e
	})
	add(r, "dashboard_preview", "dashboard", "ledger:read", "Use this to inspect the exact server-rendered dashboard and computed values. Does not publish or execute browser code.", true, false, func(ctx context.Context, p principal.Principal, in PreviewInput) (dashboard.Rendered, error) {
		v, e := versionRef(in.Version)
		if e != nil {
			return dashboard.Rendered{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, false)
		if e != nil {
			return dashboard.Rendered{}, e
		}
		rendered, e := access.Dashboard.Preview(ctx, access.Principal, access.ID, v, in.Params)
		if e == nil && rendered.Validation.OK && s.Previews != nil {
			rendered.HTMLURL, rendered.PNGURL, e = s.Previews.Create(p, rendered.HTML, access.Grant)
		}
		return rendered, e
	})
	add(r, "dashboard_diff", "dashboard", "ledger:read", "Use this when comparing two immutable dashboard versions. from/to are numbers, draft or published.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string `json:"dashboard_id"`
		From        any    `json:"from"`
		To          any    `json:"to"`
	}) (struct {
		Changes []dashboard.Change `json:"changes"`
	}, error) {
		a, e := versionRef(in.From)
		if e != nil {
			return struct {
				Changes []dashboard.Change `json:"changes"`
			}{}, e
		}
		b, e := versionRef(in.To)
		if e != nil {
			return struct {
				Changes []dashboard.Change `json:"changes"`
			}{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, false)
		if e != nil {
			return struct {
				Changes []dashboard.Change `json:"changes"`
			}{}, e
		}
		v, e := access.Dashboard.Diff(ctx, access.Principal, access.ID, a, b)
		return struct {
			Changes []dashboard.Change `json:"changes"`
		}{v}, e
	})
	add(r, "dashboard_publish", "dashboard", "dash:write", "Use this to publish a draft immediately. No human approval required. Validation and a successful server render gate the publish; failures keep the live head unchanged.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string `json:"dashboard_id"`
		Version     int    `json:"version"`
		Note        string `json:"note,omitempty"`
	}) (dashboard.PublishResult, error) {
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, true)
		if e != nil {
			return dashboard.PublishResult{}, e
		}
		v, e := access.Dashboard.Publish(ctx, access.Principal, access.ID, in.Version, in.Note)
		if access.Grant != nil {
			v.URL = "/shared/" + access.Grant.ID
		}
		if e == nil {
			if refreshErr := s.Sharing.RefreshDashboard(ctx, access.Principal, access.ID); refreshErr != nil {
				s.Logger.ErrorContext(ctx, "refresh dashboard snapshots after publication", "dashboard_id", access.ID, "error", refreshErr)
				v.Warnings = append(v.Warnings, "Published successfully; some shared snapshots could not refresh. Maintenance will retry.")
			}
		}
		return v, e
	})
	add(r, "dashboard_rollback", "dashboard", "dash:write", "Use this when a publish went wrong. Revalidates the selected prior version and makes it live, retaining every version.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string `json:"dashboard_id"`
		ToVersion   int    `json:"to_version"`
	}) (dashboard.PublishResult, error) {
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, true)
		if e != nil {
			return dashboard.PublishResult{}, e
		}
		v, e := access.Dashboard.Rollback(ctx, access.Principal, access.ID, in.ToVersion)
		if access.Grant != nil {
			v.URL = "/shared/" + access.Grant.ID
		}
		if e == nil {
			if refreshErr := s.Sharing.RefreshDashboard(ctx, access.Principal, access.ID); refreshErr != nil {
				s.Logger.ErrorContext(ctx, "refresh dashboard snapshots after rollback", "dashboard_id", access.ID, "error", refreshErr)
				v.Warnings = append(v.Warnings, "Rolled back successfully; some shared snapshots could not refresh. Maintenance will retry.")
			}
		}
		return v, e
	})
	add(r, "expr_eval", "dashboard", "ledger:read", "Use this to test a bounded CEL expression on your own data. vars.data maps query names to declared source queries; vars.params supplies parameters. Shared-owner data is never accepted.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		Expr string `json:"expr"`
		Vars *struct {
			Data   map[string]dashboard.Query `json:"data,omitempty"`
			Params map[string]any             `json:"params,omitempty"`
		} `json:"vars,omitempty"`
	}) (expressions.Result, error) {
		data := map[string]dashboard.Query{}
		params := map[string]any{}
		if in.Vars != nil {
			data = in.Vars.Data
			params = in.Vars.Params
		}
		engine := dashboard.Engine{Expr: s.Expr, Reader: s.Sources}
		return engine.Evaluate(ctx, p, in.Expr, data, params)
	})
	add(r, "audit_log", "audit", "ledger:read", "Use this when asked who changed what. Events are hash-chained per user and include durable workflow outcomes; missing outcome indicates interruption. Page with next_cursor.", true, false, s.Audit.List)
	add(r, "history_of", "audit", "ledger:read", "Use this when tracing a specific entry, rule, table or dashboard. Returns that target's audit events and outcomes.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		TargetID string `json:"target_id"`
	}) (history.HistoryResult, error) {
		return s.History.Of(ctx, p, in.TargetID)
	})
	add(r, "undo", "audit", "ledger:write", "Use this to append an inverse for a completed mutation. Requires event_id and reason; posted journal entries are reversed and history is retained. Each affected domain scope is required.", false, true, s.History.Undo)
	registerWorkflows(r, s)
	registerReference(server, s)
	return server
}
