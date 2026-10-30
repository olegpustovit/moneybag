package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/admin"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/classify"
	"github.com/olegpustovit/moneybag/backend/internal/components/consistency"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/feedback"
	"github.com/olegpustovit/moneybag/backend/internal/components/history"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/preview"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/semantic"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"log/slog"
	"reflect"
	"strconv"
)

type Services struct {
	Previews    *preview.Service
	Consistency *consistency.Service
	History     *history.Service
	Classify    *classify.Service
	Imports     *imports.Service
	Reconcile   *reconcile.Service
	Sharing     *sharing.Service
	Identity    *identity.Service
	Ledger      *ledger.Service
	Audit       *audit.Service
	Rates       *rates.Service
	Budget      *budget.Service
	Tables      *tables.Service
	Model       *model.Service
	Dashboard   *dashboard.Service
	Expr        *expressions.Engine
	Sources     dashboard.Reader
	Logger      *slog.Logger
	Semantic    *semantic.Service
	Feedback    *feedback.Service
	Admin       *admin.Service
	PublicURL   string
}

// dashboardWrite keeps grant resolution and the write inside the same serialized workflow.
func dashboardWrite[T any](ctx context.Context, p principal.Principal, s Services, id string, fn func(context.Context, sharing.Access) (T, error)) (T, sharing.Access, error) {
	var value T
	var access sharing.Access
	err := s.Audit.Workflow(ctx, func(tx context.Context) error {
		var err error
		access, err = s.Sharing.Access(tx, p, id, true)
		if err != nil {
			return err
		}
		if access.Grant != nil {
			tx = audit.WithGrant(tx, access.Grant.ID, sharing.PermissionHash(*access.Grant))
		}
		value, err = fn(tx, access)
		return err
	})
	return value, access, err
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
	tools   map[string]toolInfo
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
	ValuesOnly  bool           `json:"values_only,omitempty" jsonschema:"true omits html and preview links and returns only validation, per-component values and computed values"`
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
	r.tools[name] = toolInfo{set, scope}
	open := name == "rates_refresh" || name == "import_start" || name == "feeds_sync"
	inputSchema, err := schema[In]()
	if err != nil {
		panic(err)
	}
	specEnums(inputSchema)
	outputSchema, err := schema[Out]()
	if err != nil {
		panic(err)
	}
	openObjects(outputSchema)
	mcp.AddTool(r.server, &mcp.Tool{Name: name, Description: description, InputSchema: inputSchema, OutputSchema: outputSchema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: &destructive, OpenWorldHint: &open}}, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		p, e := principal.From(ctx)
		if e == nil {
			e = p.Require(scope)
		}
		if e != nil {
			return nil, zero, e
		}
		if readOnly {
			ctx = ledger.WithAccountCache(rates.WithQuoteCache(reqcache.With(ctx))) // reads only: sources, quotes and accounts load once per call
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
		if c, ok := any(out).(contentOutput); ok {
			return &mcp.CallToolResult{Content: c.content()}, out, nil
		}
		return nil, out, nil
	})
}
func anySchema() *jsonschema.Schema {
	return &jsonschema.Schema{Types: []string{"null", "boolean", "number", "integer", "string", "array", "object"}}
}
func schema[T any]() (*jsonschema.Schema, error) {
	return jsonschema.ForType(reflect.TypeFor[T](), &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[ledger.Decimal](): {Type: "string", Pattern: `^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`}, reflect.TypeFor[json.RawMessage](): anySchema(),
		// Some connectors silently drop tools whose properties use the bare `true` schema that `any` produces; spell it out.
		reflect.TypeFor[any](): anySchema()}})
}

// openObjects lets output objects carry fields a client's cached schema predates, so adding a result field never breaks a connected client.
func openObjects(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if s.Properties != nil {
		s.AdditionalProperties = nil
	}
	for _, c := range s.Properties {
		openObjects(c)
	}
	for _, c := range s.Defs {
		openObjects(c)
	}
	for _, list := range [][]*jsonschema.Schema{s.PrefixItems, s.AllOf, s.AnyOf, s.OneOf} {
		for _, c := range list {
			openObjects(c)
		}
	}
	openObjects(s.Items)
	openObjects(s.AdditionalProperties)
}
func New(s Services, opts Options) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "moneybag", Version: opts.Version}, &mcp.ServerOptions{Logger: s.Logger, Instructions: "Moneybag is your personal double-entry ledger. Use ledger tools to read and post money movements, import_* for statements, model tools for forecasts, and dashboard_* to build views. Ledger writes create drafts unless commit=true. dashboard_publish goes live with no human approval once validation, dashboard_lint and dashboard_test pass. Posted entries are immutable; use reversals to correct them. One shared database scopes every record to the authenticated user. Amounts are decimal strings. Discover account IDs before posting. Review tool errors and audit history before retrying ambiguous writes."})
	r := registration{server, opts, s.Logger, map[string]toolInfo{}}
	server.AddReceivingMiddleware(filterToolList(r.tools))
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
	add(r, "account_create", "ledger", "ledger:write", "Use this when a new bank account, cash pot or expense category is needed. Optional class (bank, cash, metal_holding, receivable, liability, equity, income, expense) and groups (dimension to value, e.g. type, category, source). IDs use assets:, liabilities:, expenses:, income: or equity: prefixes. Money amounts use declared commodities.", false, false, s.Ledger.CreateAccount)
	add(r, "account_update", "ledger", "ledger:write", "Use this when renaming, regrouping (class, groups) or closing an account, or setting its human display_name, working note or estimated flag. Keep notes out of the name. Omitted fields remain unchanged; empty pot or location clears them. History is retained.", false, false, s.Ledger.UpdateAccount)
	add(r, "entries_list", "ledger", "ledger:read", "Use this when you need transactions matching filters; effective=true hides reversed originals and their reversals. Results are scoped to your user, newest sequence first by default; sort=date or -date uses next_cursor and next_cursor_date together; amount or -amount uses next_cursor and next_cursor_amount together. Use account or accounts to match postings in one or any of several accounts. Amount is the first matching account posting, or first posting when no account filter is specified, ordered numerically across commodities. Limit 1..500.", true, false, s.Ledger.Entries)
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
	add(r, "rate_set", "rates", "ledger:write", "Use this when recording a manual FX, metal or security quote, or correcting a bad readout: a quote for the same pair and at is overwritten with the new value. Positive decimal value, BASE/QUOTE pair, RFC3339 at and source are required; add reason when correcting. Corrected quotes are never overwritten by the daily sync. Undoable.", false, false, s.Rates.Set)
	add(r, "rate_archive", "rates", "ledger:write", "Use this when a stored quote is wrong and should be ignored: hides the quote by id (from rates_get or rate_set) from valuation. The sync never restores an archived quote. Undoable with undo.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}) (struct {
		Archived string `json:"archived"`
	}, error) {
		return struct {
			Archived string `json:"archived"`
		}{in.ID}, s.Rates.Archive(ctx, p, in.ID, true, in.Reason)
	})
	add(r, "rates_sync_status", "rates", "ledger:read", "Use this when checking whether rates are current. Returns per provider (ecb, gold-api, swissquote) the last attempt and success, status (ok, error, skipped, never), error, consecutive failures and the newest quote written.", true, false, func(ctx context.Context, p principal.Principal, in struct{}) (struct {
		Providers []rates.ProviderState `json:"providers"`
	}, error) {
		st, e := s.Rates.SyncStatus(ctx, p)
		return struct {
			Providers []rates.ProviderState `json:"providers"`
		}{st}, e
	})
	type refreshed struct {
		Updated   []rates.Quote         `json:"updated"`
		Providers []rates.ProviderState `json:"providers"`
	}
	// updated keeps its original shape (newest ECB day); gold quotes written are included in full.
	add(r, "rates_refresh", "rates", "ledger:write", "Use this when FX or gold quotes are stale. Runs the daily sync now: backfills ECB daily reference rates for the last 90 days and fetches spot gold (gold-api.com, else Swissquote) as XAU/USD and XAU_g/USD. Returns the newest ECB day plus gold quotes written, and per-provider status. Corrected or archived quotes are never overwritten. sources may limit it to ecb and/or gold. backfill_from (YYYY-MM-DD, up to 10 years back) instead loads ECB daily history for pairs (default EUR/USD) from that date and returns every quote written; free gold history is not available. Manual security prices use rate_set.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		Sources      []string `json:"sources,omitempty"`
		BackfillFrom string   `json:"backfill_from,omitempty" jsonschema:"YYYY-MM-DD; loads ECB daily history from this date instead of running the sync"`
		Pairs        []string `json:"pairs,omitempty" jsonschema:"EUR/<CCY> pairs for backfill_from; default EUR/USD"`
	}) (refreshed, error) {
		if in.BackfillFrom != "" {
			written, e := s.Rates.BackfillECB(ctx, p, in.BackfillFrom, in.Pairs)
			return refreshed{Updated: append([]rates.Quote{}, written...), Providers: []rates.ProviderState{}}, e
		}
		ecb, gold := len(in.Sources) == 0, len(in.Sources) == 0
		for _, source := range in.Sources {
			switch source {
			case "ecb":
				ecb = true
			case "gold":
				gold = true
			default:
				return refreshed{}, fmt.Errorf("feed %q is not configured; use ecb or gold, or rate_set for manual quotes", source)
			}
		}
		res, e := s.Rates.Sync(ctx, p, ecb, gold)
		out := refreshed{Updated: []rates.Quote{}, Providers: res.Providers}
		newest := ""
		for _, x := range res.Written {
			if x.Source == "ECB daily reference" {
				newest = max(newest, x.At)
			}
		}
		for _, x := range res.Written {
			if x.Source != "ECB daily reference" || x.At == newest {
				out.Updated = append(out.Updated, x)
			}
		}
		return out, e
	})
	add(r, "budget_get", "budget", "ledger:read", "Use this when comparing EUR spending with your budget for YYYY-MM. Includes current actuals and the average over a window (default: the previous six complete months; window {months, include_current} changes it and the window used is returned with a label like May–Oct). Lists unmapped expense accounts.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		Month  string `json:"month,omitempty"`
		Window struct {
			Months         int  `json:"months,omitempty"`
			IncludeCurrent bool `json:"include_current,omitempty"`
		} `json:"window,omitempty"`
	}) (budget.Status, error) {
		return s.Budget.GetWindow(ctx, p, in.Month, budget.Window{Months: in.Window.Months, IncludeCurrent: in.Window.IncludeCurrent})
	})
	add(r, "budget_set_line", "budget", "ledger:write", "Use this when changing an EUR budget limit, label or expense-account mapping, either a literal accounts list or a group_ref {dimension, value} resolved through account groups. Omitted fields remain unchanged; empty accounts clears mapping.", false, false, s.Budget.Set)
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
	add(r, "db_delete_rows", "tables", "model:write", "Use this to delete rows from a data table, either by row_ids (1..500) or by where (CEL over row; matching 1..500 rows). Give exactly one. Rows are archived, so undo restores them. To change a row, use db_update_cells with its row_id.", false, true, s.Tables.DeleteRows)
	add(r, "model_get", "model", "ledger:read", "Use this when you need assumptions, income streams and scenarios before forecasting.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (model.State, error) {
		return s.Model.Get(ctx, p)
	})
	add(r, "scenario_upsert", "model", "model:write", "Use this when defining a what-if scenario. Overrides each name an assumption with either numeric value or CEL expr over params. An override may instead carry ref (budget_line:<id>, budget_lines.total, income_stream:<id> or view:<id>[.<column>], column default value, first row) with optional factor; the assumption must end in _usd or _eur. Budget and stream refs are converted with the live EUR/USD rate; a view value is taken in the assumption's currency. An override named after an income stream id replaces that stream amount (0 disables it); overriding income_usd replaces all streams with that monthly total. kyiv_extra_drain_usd, yield_pct and buffer_usd are assumptions or overrides; a legacy block supplies them otherwise, and drop_legacy: true removes it. income_usd is required only when there are no income streams. Events change cash in a specified future month.", false, false, s.Model.Upsert)
	add(r, "forecast_run", "model", "ledger:read", "Use this when projecting scenarios. Supply 1..20 scenario IDs and horizon_months 1..600. value_in is USD or EUR; the original tracker projection engine is preserved for migrated scenarios. The starting state (net worth, wise and kyiv cash, gold grams, EUR/USD and XAU_g/USD rates) is computed from the live ledger as of as_of (default today, Europe/Zagreb); wise cash is accounts whose pot starts with wise (wise_usd, wise_eur), kyiv cash is pots containing kyiv (cash_kyiv), excluding gold; gold is the XAU_g commodity. Stored assumptions are used only when the ledger cannot be valued, and a scenario override still wins. Each series item reports start_as_of, start_source, start_net_usd, journal_seq, rates_used and resolved_inputs, plus savings_usd_mo, kyivEmptyMonth and runway_ex_gold.", true, false, func(ctx context.Context, p principal.Principal, in model.ForecastInput) (struct {
		Series []model.Projection `json:"series"`
	}, error) {
		v, e := s.Model.Forecast(ctx, p, in)
		return struct {
			Series []model.Projection `json:"series"`
		}{v}, e
	})
	add(r, "income_model_set", "model", "model:write", "Use this when salary or side income changes. Replaces streams; amount is per declared frequency, currency is USD/EUR, start/end are optional YYYY-MM-DD. kind is salary, side or reimbursement (reimbursement is netted against Kyiv cash and never counted as income); payer_match is CEL over row.payee and row.tags, lands_in an account id and gross_or_net gross or net; income_reconcile uses them.", false, false, func(ctx context.Context, p principal.Principal, in struct {
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
	add(r, "catalog_get", "dashboard", "ledger:read", "Use this before writing dashboard CEL. Returns the sources with their fields, functions, aggregates and variables you can reference, plus a revision.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (dashboard.Catalog, error) {
		return s.Dashboard.Catalog(ctx, p, s.Tables)
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
		result, err := access.Dashboard.Get(ctx, access.Principal, access.ID, v)
		if err == nil && access.Grant != nil {
			err = s.Sharing.RecordEditorRead(ctx, p, access)
		}
		if err != nil {
			return dashboard.GetResult{}, err
		}
		return result, nil
	})
	add(r, "dashboard_test", "dashboard", "ledger:read", "Use this before publishing to check spec.tests against the ledger. Each test says the metric must equal the plain ledger accounts over a period; a mismatch shows both numbers.", true, false, func(ctx context.Context, p principal.Principal, in GetDashboardInput) (dashboard.TestReport, error) {
		v, e := versionRef(in.Version)
		if e != nil {
			return dashboard.TestReport{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.ID, false)
		if e != nil {
			return dashboard.TestReport{}, e
		}
		result, e := access.Dashboard.Get(ctx, access.Principal, access.ID, v)
		if e != nil {
			return dashboard.TestReport{}, e
		}
		return access.Dashboard.RunTests(ctx, access.Principal, result.Spec)
	})
	add(r, "dashboard_lint", "dashboard", "ledger:read", "Use this before publishing to judge a dashboard once its templates have run. Each issue has rule, severity, path, component_id, msg and fix; errors block dashboard_publish and warnings do not. Paths are JSON Pointers for dashboard_apply.", true, false, func(ctx context.Context, p principal.Principal, in GetDashboardInput) (lintOutput, error) {
		v, e := versionRef(in.Version)
		if e != nil {
			return lintOutput{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.ID, false)
		if e != nil {
			return lintOutput{}, e
		}
		result, e := access.Dashboard.Get(ctx, access.Principal, access.ID, v)
		if e != nil {
			return lintOutput{}, e
		}
		issues, e := access.Dashboard.LintSpec(ctx, access.Principal, result.Spec)
		if e != nil {
			return lintOutput{}, e
		}
		out := lintOutput{Issues: issues}
		for _, issue := range issues {
			if issue.Severity == "error" {
				out.Errors++
			} else {
				out.Warnings++
			}
		}
		return out, nil
	})
	add(r, "dashboard_summary", "dashboard", "ledger:read", "Use this to see a dashboard's pages, components and data sources without the full spec. Use dashboard_get when you need the CEL.", true, false, func(ctx context.Context, p principal.Principal, in GetDashboardInput) (dashboardSummary, error) {
		v, e := versionRef(in.Version)
		if e != nil {
			return dashboardSummary{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.ID, false)
		if e != nil {
			return dashboardSummary{}, e
		}
		result, err := access.Dashboard.Get(ctx, access.Principal, access.ID, v)
		if err == nil && access.Grant != nil {
			err = s.Sharing.RecordEditorRead(ctx, p, access)
		}
		if err != nil {
			return dashboardSummary{}, err
		}
		return summarizeDashboard(result), nil
	})
	add(r, "dashboard_create", "dashboard", "dash:write", "Use this when creating a dashboard draft. Optional from_template is budget-vs-actual or forecast. It belongs to your authenticated user.", false, false, s.Dashboard.Create)
	add(r, "dashboard_add_component", "dashboard", "dash:write", "Use this when adding a KPI, table, chart, budget_bars, scenario_compare or text note. New components append a layout row by default; a missing named page is added. Spans are 1..12.", false, false, func(ctx context.Context, p principal.Principal, in dashboard.AddInput) (dashboard.EditResult, error) {
		result, access, err := dashboardWrite(ctx, p, s, in.DashboardID, func(tx context.Context, access sharing.Access) (dashboard.EditResult, error) {
			in.DashboardID = access.ID
			return access.Dashboard.Add(tx, access.Principal, in)
		})
		if access.Grant != nil {
			result.EventID = ""
		}
		return result, err
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
		result, access, err := dashboardWrite(ctx, p, s, in.DashboardID, func(tx context.Context, access sharing.Access) (dashboard.EditResult, error) {
			return access.Dashboard.UpdateComponent(tx, access.Principal, access.ID, in.ComponentID, set)
		})
		if access.Grant != nil {
			result.EventID = ""
		}
		return result, err
	})
	add(r, "dashboard_remove_component", "dashboard", "dash:write", "Use this when removing a component and its layout placements from a new draft version. Published output is unchanged until publish.", false, true, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string `json:"dashboard_id"`
		ComponentID string `json:"component_id"`
	}) (dashboard.EditResult, error) {
		result, access, err := dashboardWrite(ctx, p, s, in.DashboardID, func(tx context.Context, access sharing.Access) (dashboard.EditResult, error) {
			return access.Dashboard.Remove(tx, access.Principal, access.ID, in.ComponentID)
		})
		if access.Grant != nil {
			result.EventID = ""
		}
		return result, err
	})
	add(r, "dashboard_compose", "dashboard", "dash:write", "Use this when rearranging a page's 12-column layout. A missing named page is added. Each row contains cols with span and component ID.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string          `json:"dashboard_id"`
		Page        string          `json:"page,omitempty"`
		Layout      []dashboard.Row `json:"layout"`
	}) (dashboard.EditResult, error) {
		result, access, err := dashboardWrite(ctx, p, s, in.DashboardID, func(tx context.Context, access sharing.Access) (dashboard.EditResult, error) {
			return access.Dashboard.Compose(tx, access.Principal, access.ID, in.Page, in.Layout)
		})
		if access.Grant != nil {
			result.EventID = ""
		}
		return result, err
	})
	add(r, "dashboard_patch", "dashboard", "dash:write", "Use this for RFC6902 JSON Patch edits to a dashboard draft. ops is a JSON array of operations, or a string containing that array. To guard against stale edits, start ops with a test operation on path /version whose value is the version from dashboard_get; a stale version is rejected. At most 500 operations and 1 MiB; unknown spec fields are rejected. A failing operation is reported as op <index>: path <path> not found (or its error).", false, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string          `json:"dashboard_id"`
		Ops         json.RawMessage `json:"ops" jsonschema:"RFC6902 operations: a JSON array, or a string containing that array"`
	}) (dashboard.EditResult, error) {
		b := in.Ops
		result, access, err := dashboardWrite(ctx, p, s, in.DashboardID, func(tx context.Context, access sharing.Access) (dashboard.EditResult, error) {
			return access.Dashboard.Patch(tx, access.Principal, access.ID, b)
		})
		if access.Grant != nil {
			result.EventID = ""
		}
		return result, err
	})
	add(r, "dashboard_apply", "dashboard", "dash:write", "Use this to edit a dashboard draft with JSON Patch ops, like dashboard_patch. Set validate_only to check the ops and get the validation without writing. Set if_version to the draft version you read; a stale draft is rejected.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID  string          `json:"dashboard_id"`
		Ops          json.RawMessage `json:"ops" jsonschema:"RFC6902 operations: a JSON array, or a string containing that array"`
		IfVersion    int             `json:"if_version,omitempty"`
		ValidateOnly bool            `json:"validate_only,omitempty"`
	}) (dashboard.EditResult, error) {
		ops := in.Ops
		if in.IfVersion > 0 {
			guarded, e := withVersionGuard(ops, in.IfVersion)
			if e != nil {
				return dashboard.EditResult{}, e
			}
			ops = guarded
		}
		if in.ValidateOnly {
			access, e := s.Sharing.Access(ctx, p, in.DashboardID, false)
			if e != nil {
				return dashboard.EditResult{}, e
			}
			return access.Dashboard.PatchPreview(ctx, access.Principal, access.ID, ops)
		}
		result, access, err := dashboardWrite(ctx, p, s, in.DashboardID, func(tx context.Context, access sharing.Access) (dashboard.EditResult, error) {
			return access.Dashboard.Patch(tx, access.Principal, access.ID, ops)
		})
		if access.Grant != nil {
			result.EventID = ""
		}
		return result, err
	})
	add(r, "dashboard_render", "dashboard", "ledger:read", "Use this to see a published or draft dashboard as an image of the real page, with its validation. Fix validation errors first; the image appears once the renderer is configured.", true, false, func(ctx context.Context, p principal.Principal, in GetDashboardInput) (renderOutput, error) {
		v, e := versionRef(in.Version)
		if e != nil {
			return renderOutput{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.ID, false)
		if e != nil {
			return renderOutput{}, e
		}
		rendered, e := access.Dashboard.Preview(ctx, access.Principal, access.ID, v, nil)
		if e == nil && access.Grant != nil {
			e = s.Sharing.RecordEditorRead(ctx, p, access)
		}
		if e != nil {
			return renderOutput{}, e
		}
		out := renderOutput{Validation: rendered.Validation}
		if !rendered.Validation.OK {
			out.Message = "fix validation errors before rendering"
			return out, nil
		}
		if s.Previews == nil {
			out.Message = "image unavailable: renderer not configured"
			return out, nil
		}
		png, e := s.Previews.PNG(ctx, rendered.HTML)
		if e != nil {
			out.Message = "image unavailable: " + e.Error()
			return out, nil
		}
		out.PNG = png
		return out, nil
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
		if e == nil && access.Grant != nil {
			e = s.Sharing.RecordEditorRead(ctx, p, access)
		}
		if e != nil {
			return dashboard.Validation{}, e
		}
		return r.Validation, nil
	})
	add(r, "dashboard_preview", "dashboard", "ledger:read", "Use this to read back a dashboard as rendered: values (per component id), computed (every computed value) and validation, plus html and preview links unless values_only is true. version is a number, draft or published. Does not publish or execute browser code.", true, false, func(ctx context.Context, p principal.Principal, in PreviewInput) (dashboard.Rendered, error) {
		v, e := versionRef(in.Version)
		if e != nil {
			return dashboard.Rendered{}, e
		}
		access, e := s.Sharing.Access(ctx, p, in.DashboardID, false)
		if e != nil {
			return dashboard.Rendered{}, e
		}
		rendered, e := access.Dashboard.Preview(ctx, access.Principal, access.ID, v, in.Params)
		if e == nil && access.Grant != nil {
			e = s.Sharing.RecordEditorRead(ctx, p, access)
		}
		if e != nil {
			return dashboard.Rendered{}, e
		}
		capTableRows(rendered.Values, maxAgentTableRows)
		if in.ValuesOnly {
			rendered.HTML = ""
			return rendered, e
		}
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
		if e == nil && access.Grant != nil {
			e = s.Sharing.RecordEditorRead(ctx, p, access)
		}
		if e != nil {
			return struct {
				Changes []dashboard.Change `json:"changes"`
			}{}, e
		}
		return struct {
			Changes []dashboard.Change `json:"changes"`
		}{v}, nil
	})
	add(r, "dashboard_publish", "dashboard", "dash:write", "Use this to publish a draft immediately. No human approval required. Validation, dashboard_lint errors, dashboard_test and the render check gate the publish; a failure names each fix and keeps the live head unchanged. Returns the link and the changed paths; dashboard_rollback undoes it.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string `json:"dashboard_id"`
		Version     int    `json:"version,omitempty" jsonschema:"defaults to the current draft"`
		Note        string `json:"note,omitempty"`
	}) (dashboard.PublishResult, error) {
		return gatedPublish(ctx, p, s, in.DashboardID, in.Version, in.Note)
	})
	add(r, "dashboard_rollback", "dashboard", "dash:write", "Use this when a publish went wrong. Revalidates the selected prior version and makes it live, retaining every version.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string `json:"dashboard_id"`
		ToVersion   int    `json:"to_version"`
	}) (dashboard.PublishResult, error) {
		v, access, e := dashboardWrite(ctx, p, s, in.DashboardID, func(tx context.Context, access sharing.Access) (dashboard.PublishResult, error) {
			return access.Dashboard.Rollback(tx, access.Principal, access.ID, in.ToVersion)
		})
		if access.Grant != nil {
			v.URL = "/shared/" + access.Grant.ID
			v.EventID = ""
		}
		if e == nil {
			if refreshErr := s.Sharing.RefreshDashboard(ctx, access.Principal, access.ID); refreshErr != nil {
				s.Logger.ErrorContext(ctx, "refresh dashboard snapshots after rollback", "dashboard_id", access.ID, "error", refreshErr)
				v.Warnings = append(v.Warnings, "Rolled back successfully; some shared snapshots could not refresh. Maintenance will retry.")
			}
		}
		return v, e
	})
	add(r, "dashboard_delete", "dashboard", "dash:write", "Use this to delete (archive) a dashboard you own; it disappears from lists and its public link. Owner only; shared editors cannot delete. Reversible with undo.", false, true, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string `json:"dashboard_id"`
		Reason      string `json:"reason,omitempty"`
	}) (struct {
		Deleted string `json:"deleted"`
	}, error) {
		// Archive filters by the caller's own user id, so only the owner can delete.
		e := s.Dashboard.Archive(ctx, p, in.DashboardID, true, in.Reason)
		return struct {
			Deleted string `json:"deleted"`
		}{in.DashboardID}, e
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
	add(r, "audit_log", "audit", "ledger:read", "Use this when asked who changed what. Events are hash-chained per user and include durable workflow outcomes; missing outcome indicates interruption. Before/after payloads (full specs) are omitted unless include_payload is true. Page with next_cursor.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		audit.ListInput
		IncludePayload bool `json:"include_payload,omitempty" jsonschema:"return full before/after payloads (default false: event metadata only)"`
	}) (audit.Page, error) {
		page, e := s.Audit.List(ctx, p, in.ListInput)
		if e == nil && !in.IncludePayload {
			for i := range page.Events {
				page.Events[i].Before, page.Events[i].After = nil, nil
				if o := page.Events[i].Outcome; o != nil {
					o.Before, o.After = nil, nil
				}
			}
		}
		return page, e
	})
	add(r, "history_of", "audit", "ledger:read", "Use this when tracing a specific entry, rule, table or dashboard. Returns that target's audit events and outcomes; before/after payloads (full specs) are omitted unless include_payload is true.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		TargetID       string `json:"target_id"`
		IncludePayload bool   `json:"include_payload,omitempty" jsonschema:"return full before/after payloads (default false: event metadata only)"`
	}) (history.HistoryResult, error) {
		return s.History.Of(ctx, p, in.TargetID, in.IncludePayload)
	})
	add(r, "undo", "audit", "ledger:write", "Use this to append an inverse for a completed mutation. Requires event_id and reason; posted journal entries are reversed and history is retained. Each affected domain scope is required.", false, true, s.History.Undo)
	add(r, "income_reconcile", "model", "ledger:read", "Use this when checking income against the model. For each stream (or one stream_id) and month in from..to returns expected (from the model), actual (posted entries matched by payer_match, net of postings on lands_in or of income accounts, in stream currency), variance and entry_ids.", true, false, func(ctx context.Context, p principal.Principal, in model.ReconcileInput) (struct {
		Months []model.IncomeMonth `json:"months"`
	}, error) {
		v, e := s.Model.IncomeReconcile(ctx, p, in)
		return struct {
			Months []model.IncomeMonth `json:"months"`
		}{v}, e
	})
	add(r, "consistency_check", "audit", "ledger:read", "Use this to check that every figure has one source. Read-only: trial balance, assertions, published dashboard net worth, orphans, numeric literals, model-input duplicates, receivables, reversals, budget mapping and scenario start. Returns ok and per-check detail.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (consistency.Result, error) {
		return s.Consistency.Run(ctx, p)
	})
	registerWorkflows(r, s)
	registerViewsAndTables(r, s)
	registerSemantic(r, s)
	registerReference(server, s)
	return server
}
