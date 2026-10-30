package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"maps"
	"math/big"
	"sort"
	"strings"
	"time"
)

type Sources struct {
	Ledger    *ledger.Service
	Budget    *budget.Service
	Model     *model.Service
	Tables    *tables.Service
	Valuer    ledger.Valuer
	Rates     *rates.Service
	Views     *Service // saved views (view:<id>); set once the dashboard service exists
	Expr      *expressions.Engine
	Snapshots *Snapshots
}

func (s *Sources) Rows(ctx context.Context, p principal.Principal, source string) ([]map[string]any, error) {
	rows, err := s.sharedRows(ctx, p, source)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		out[i] = cloneSourceValue(row).(map[string]any)
	}
	return out, nil
}

// sharedRows returns immutable rows for the query engine, which copies each row before deriving fields.
func (s *Sources) sharedRows(ctx context.Context, p principal.Principal, source string) ([]map[string]any, error) {
	ctx = expressions.WithVolatility(ctx)
	if strings.HasPrefix(source, "view:") {
		id := strings.TrimPrefix(source, "view:")
		stack, _ := ctx.Value(viewStackKey{}).([]string)
		for _, seen := range stack {
			if seen == id {
				return nil, fmt.Errorf("view cycle: %s -> %s", strings.Join(stack, " -> "), id)
			}
		}
		if len(stack) >= maxViewDepth {
			return nil, fmt.Errorf("views nest deeper than %d", maxViewDepth)
		}
	}
	params, _ := json.Marshal(ctx.Value(paramsKey{}))
	read := func() ([]map[string]any, error) { return s.rows(ctx, p, source) }
	stack, _ := ctx.Value(viewStackKey{}).([]string)
	if s.Snapshots != nil && !(len(stack) > 0 && strings.HasPrefix(source, "view:")) {
		build := read
		read = func() ([]map[string]any, error) {
			value, err := s.Snapshots.read(ctx, p, "source", source, json.RawMessage(params), func() (any, error) { return build() }, nil)
			if err != nil {
				return nil, err
			}
			return value.([]map[string]any), nil
		}
	}
	var rows []map[string]any
	var err error
	if len(stack) > 0 && strings.HasPrefix(source, "view:") {
		v, epoch, stable, e := s.viewDefinition(ctx, p, strings.TrimPrefix(source, "view:"))
		if e != nil {
			return nil, e
		}
		eligible := stable && memoizableChain(v, stack, func(id string) (View, bool) {
			next, nextEpoch, nextStable, err := s.viewDefinition(ctx, p, id)
			return next, err == nil && nextStable && nextEpoch == epoch
		})
		current, currentStable := s.Snapshots.memoEpoch(p.UserID)
		if eligible && currentStable && current == epoch {
			rows, err = cachedView(ctx, p, epoch, source, string(params), func() ([]map[string]any, error) { return s.evaluateView(ctx, p, v) })
		} else {
			rows, err = read()
		}
	} else {
		rows, err = reqcache.Do(ctx, "rows|"+p.UserID+"|"+source+"|"+string(params), read)
	}
	return rows, err
}

type viewResult struct {
	rows     []map[string]any
	volatile bool
	expires  time.Time
}

func cachedView(ctx context.Context, p principal.Principal, epoch uint64, source, params string, read func() ([]map[string]any, error)) ([]map[string]any, error) {
	result, err := reqcache.Do(ctx, fmt.Sprintf("view-chain|%s|%d|%s|%s", p.UserID, epoch, source, params), func() (viewResult, error) {
		rows, err := read()
		return viewResult{rows, expressions.Volatile(ctx), expressions.Expiry(ctx)}, err
	})
	if result.volatile {
		expressions.MarkVolatile(ctx)
	}
	expressions.ExpireAt(ctx, result.expires)
	return result.rows, err
}

func cloneSourceValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		copy := maps.Clone(value)
		for key, item := range copy {
			copy[key] = cloneSourceValue(item)
		}
		return copy
	case []any:
		copy := make([]any, len(value))
		for i, item := range value {
			copy[i] = cloneSourceValue(item)
		}
		return copy
	case []map[string]any:
		copy := make([]map[string]any, len(value))
		for i, item := range value {
			copy[i] = cloneSourceValue(item).(map[string]any)
		}
		return copy
	case []string:
		return append([]string(nil), value...)
	default:
		return v
	}
}

func (s *Sources) rows(ctx context.Context, p principal.Principal, source string) ([]map[string]any, error) {
	params, _ := ctx.Value(paramsKey{}).(map[string]any)
	month, _ := params["month"].(string)
	out := []map[string]any{}
	switch {
	case source == "journal" || source == "effective_entries" || source == "postings":
		from, to := "", ""
		if source == "postings" {
			from, _ = params["from"].(string)
			to, _ = params["to"].(string)
		}
		cache := map[string]*big.Rat{}
		var accounts map[string]ledger.Account
		if source == "postings" {
			var err error
			if accounts, err = s.accountMap(ctx, p); err != nil {
				return nil, err
			}
		}
		err := s.Ledger.PostedStream(ctx, p, from, to, source != "journal", func(entry ledger.Entry) error {
			if source == "postings" {
				rows, err := s.postingRows(ctx, p, entry, accounts, cache)
				if err != nil {
					return err
				}
				out = append(out, rows...)
			} else {
				row, err := native(entry)
				if err != nil {
					return err
				}
				out = append(out, row)
			}
			if len(out) > 250000 {
				return fmt.Errorf("journal source exceeds 250000 rows")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		// Preserve the paged journal's descending sequence order for order-sensitive queries.
		sort.SliceStable(out, func(i, j int) bool { return out[i]["seq"].(float64) > out[j]["seq"].(float64) })

	case source == "accounts":
		balances, e := s.Ledger.Balances(ctx, p, ledger.BalancesInput{}, nil)
		if e != nil {
			return nil, e
		}
		accounts, e := s.accountMap(ctx, p)
		if e != nil {
			return nil, e
		}
		for _, r := range balances.Rows {
			a := accounts[r.Key]
			if a.Type != "asset" && a.Type != "liability" {
				continue
			}
			for _, v := range r.Amounts {
				value, e := s.Valuer.Convert(ctx, p, v, "USD", balances.AsOf+"T23:59:59Z")
				if e != nil {
					return nil, e
				}
				out = append(out, map[string]any{"account": a.ID, "pot": a.Pot, "name": a.Name, "kind": a.Type, "class": a.Class, "groups": groupsAny(a.Groups), "commodity": v.Commodity, "amount": v.Value.Float(), "usd": value.Value.Float()})
			}
		}
	case source == "budget_lines":
		window := budget.Window{}
		if n, ok := params["window_months"].(float64); ok {
			window.Months = int(n)
		}
		window.IncludeCurrent, _ = params["include_current"].(bool)
		status, e := s.Budget.GetWindow(ctx, p, month, window)
		if e != nil {
			return nil, e
		}
		for _, l := range status.Lines {
			out = append(out, map[string]any{"window_label": status.Window.Label, "window_from": status.Window.From, "window_to": status.Window.To, "window_months": status.Window.Months, "id": l.ID, "name": l.Name, "limit": l.Limit, "avg": l.Avg6, "avg6": l.Avg6, "oct": l.Actual, "actual": l.Actual, "status": l.Status, "accounts": l.Accounts})
		}
	case source == "months":
		now := time.Now()
		if month != "" {
			selected, e := time.Parse("2006-01", month)
			if e != nil {
				return nil, fmt.Errorf("month parameter must be YYYY-MM")
			}
			now = selected.AddDate(0, 1, -1)
		}
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -5, 0)
		months, e := s.Budget.Months(ctx, p, start.Format("2006-01-02"), now.Format("2006-01-02"))
		if e != nil {
			return nil, e
		}
		for _, m := range months {
			r, e := native(m)
			if e != nil {
				return nil, e
			}
			out = append(out, r)
		}
	case strings.HasPrefix(source, "table:"):
		q, e := s.Tables.Query(ctx, p, tables.QueryInput{DB: strings.TrimPrefix(source, "table:")})
		if e != nil {
			return nil, e
		}
		for _, r := range q.Rows {
			cells := map[string]any{}
			for k, v := range r.Cells {
				cells[k] = v
			}
			cells["_row_id"] = r.ID
			out = append(out, cells)
		}
	case source == "scenarios" || strings.HasPrefix(source, "scenario:"):
		state, e := s.Model.Get(ctx, p)
		if e != nil {
			return nil, e
		}
		ids := []string{}
		for _, sc := range state.Scenarios {
			if source == "scenarios" || sc.ID == strings.TrimPrefix(source, "scenario:") {
				ids = append(ids, sc.ID)
			}
		}
		if len(ids) == 0 {
			return out, nil
		}
		horizon := 12
		for _, r := range state.Assumptions {
			if r.ID == "horizon" {
				if n, ok := r.Cells["value"].(float64); ok {
					horizon = int(n)
				}
			}
		}
		if len(ids) > 20 {
			return nil, fmt.Errorf("scenario source supports at most 20 scenarios")
		}
		projections, e := s.Model.Forecast(ctx, p, model.ForecastInput{ScenarioIDs: ids, HorizonMonths: horizon})
		if e != nil {
			return nil, e
		}
		for _, proj := range projections {
			// A nil runway (no spending) must reach CEL as null; a typed nil pointer cannot be converted.
			var runway any
			if proj.Runway != nil {
				runway = *proj.Runway
			}
			var exGold any
			if proj.RunwayExGold != nil {
				exGold = *proj.RunwayExGold
			}
			out = append(out, map[string]any{"id": proj.Scenario, "name": proj.Name, "spendUsd": proj.SpendUSD, "endNetUsd": proj.EndNetUSD, "runway": runway, "savings_usd_mo": proj.SavingsUSDMo, "kyiv_empty_month": proj.KyivEmptyMonth, "runway_ex_gold": exGold, "start_as_of": proj.StartAsOf, "start_net_usd": proj.StartNetUSD, "journal_seq": proj.JournalSeq})
		}
	case source == "income":
		state, e := s.Model.Get(ctx, p)
		if e != nil {
			return nil, e
		}
		for _, v := range state.Streams {
			r, e := native(v)
			if e != nil {
				return nil, e
			}
			out = append(out, r)
		}
	case strings.HasPrefix(source, "rates:"):
		quotes, e := s.Rates.Get(ctx, p, strings.Split(strings.TrimPrefix(source, "rates:"), ","), "")
		if e != nil {
			return nil, e
		}
		for _, q := range quotes {
			out = append(out, map[string]any{"pair": q.Pair, "base": q.Base, "quote": q.Quote, "at": q.At, "value": q.Value.Float(), "source": q.Source})
		}
	case source == "rate_sync":
		st, e := s.Rates.SyncStatus(ctx, p)
		if e != nil {
			return nil, e
		}
		for _, x := range st {
			out = append(out, map[string]any{"provider": x.Provider, "status": x.Status, "last_attempt": x.LastAttempt, "last_success": x.LastSuccess, "error": x.Error, "consecutive_failures": x.ConsecutiveFailures, "newest_at": x.NewestAt, "newest_value": x.NewestValue})
		}
	case strings.HasPrefix(source, "rate_history:"):
		from, _ := params["from"].(string)
		to, _ := params["to"].(string)
		quotes, e := s.Rates.History(ctx, p, strings.Split(strings.TrimPrefix(source, "rate_history:"), ","), from, to)
		if e != nil {
			return nil, e
		}
		for _, q := range quotes {
			out = append(out, map[string]any{"pair": q.Pair, "at": q.At, "value": q.Value.Float(), "source": q.Source})
		}
	case strings.HasPrefix(source, "view:"):
		return s.viewRows(ctx, p, strings.TrimPrefix(source, "view:"))
	case source == "balance_history":
		// Month-end USD totals for the last 12 months (today for the current month).
		today := time.Now()
		dates := make([]string, 0, 12)
		for i := 11; i >= 0; i-- {
			end := time.Date(today.Year(), today.Month()-time.Month(i)+1, 0, 0, 0, 0, 0, time.UTC)
			if i == 0 {
				end = today
			}
			dates = append(dates, end.Format("2006-01-02"))
		}
		history, e := s.Ledger.BalanceSeries(ctx, p, dates, s.Valuer)
		if e != nil {
			return nil, e
		}
		for i, res := range history {
			assets, liabilities := 0.0, 0.0
			for _, r := range res.Rows {
				if r.Value == nil {
					continue
				}
				switch r.Key {
				case "asset":
					assets = r.Value.Value.Float()
				case "liability":
					liabilities = r.Value.Value.Float()
				}
			}
			out = append(out, map[string]any{"month": dates[i][:7], "date": dates[i], "assets": assets, "liabilities": liabilities, "net": assets + liabilities})
		}

	case strings.HasPrefix(source, "forecast_series:"):
		id := strings.TrimPrefix(source, "forecast_series:")
		state, e := s.Model.Get(ctx, p)
		if e != nil {
			return nil, e
		}
		horizon := 12
		for _, r := range state.Assumptions {
			if n, ok := r.Cells["value"].(float64); ok && r.ID == "horizon" {
				horizon = int(n)
			}
		}
		if h, ok := params["horizon"].(float64); ok {
			horizon = int(h)
		}
		projections, e := s.Model.Forecast(ctx, p, model.ForecastInput{ScenarioIDs: []string{id}, HorizonMonths: horizon})
		if e != nil {
			return nil, e
		}
		for _, pt := range projections[0].Months {
			out = append(out, map[string]any{"m": pt.M, "net": pt.Net, "cash": pt.Cash, "wise": pt.Wise, "kyiv": pt.Kyiv, "gold": pt.Gold})
		}
	case source == "income_actuals":
		// params from/to (YYYY-MM-DD) default to the last six months through today.
		to, _ := params["to"].(string)
		from, _ := params["from"].(string)
		if to == "" {
			to = time.Now().Format("2006-01-02")
		}
		if from == "" {
			from = time.Now().AddDate(0, -5, 0).Format("2006-01") + "-01"
		}
		months, e := s.Model.IncomeReconcile(ctx, p, model.ReconcileInput{From: from, To: to})
		if e != nil {
			return nil, e
		}
		for _, m := range months {
			r, e := native(m)
			if e != nil {
				return nil, e
			}
			out = append(out, r)
		}
	default:
		return nil, fmt.Errorf("unknown source %q; use journal, effective_entries, postings, accounts, budget_lines, months, range, income, balance_history, rates:<PAIR[,PAIR]>, rate_sync, rate_history:<PAIR[,PAIR]>, scenarios, scenario:<id>, forecast_series:<id>, income_actuals, table:<id> or view:<id>", source)
	}
	return out, nil
}
func (s *Service) WithReader(reader Reader) *Service {
	copy := *s
	engine := *s.engine
	engine.Reader = reader
	copy.engine = &engine
	copy.snapshots = nil
	return &copy
}

func (s *Sources) accountMap(ctx context.Context, p principal.Principal) (map[string]ledger.Account, error) {
	accounts := map[string]ledger.Account{}
	in := ledger.AccountsInput{IncludeClosed: true, Limit: 500}
	for {
		page, e := s.Ledger.Accounts(ctx, p, in)
		if e != nil {
			return nil, e
		}
		for _, a := range page.Items {
			accounts[a.ID] = a
		}
		if page.NextCursor == "" {
			return accounts, nil
		}
		in.Cursor = page.NextCursor
	}
}
func groupsAny(g map[string]string) map[string]any {
	out := make(map[string]any, len(g))
	for k, v := range g {
		out[k] = v
	}
	return out
}

// postingRows flattens one entry into a row per posting, valued at the entry date like the months source.
func (s *Sources) postingRows(ctx context.Context, p principal.Principal, entry ledger.Entry, accounts map[string]ledger.Account, cache map[string]*big.Rat) ([]map[string]any, error) {
	rows := []map[string]any{}
	for _, post := range entry.Postings {
		eur, e := s.valueAt(ctx, p, post, "EUR", entry.Date, cache)
		if e != nil {
			return nil, e
		}
		usd, e := s.valueAt(ctx, p, post, "USD", entry.Date, cache)
		if e != nil {
			return nil, e
		}
		a := accounts[post.Account]
		tags := make([]any, len(entry.Tags))
		for i, t := range entry.Tags {
			tags[i] = t
		}
		rows = append(rows, map[string]any{"date": entry.Date, "entry": entry.ID, "seq": float64(entry.Seq), "kind": entry.Kind, "payee": entry.Payee, "tags": tags, "account": post.Account, "class": a.Class, "groups": groupsAny(a.Groups), "commodity": post.Amount.Commodity, "amount": post.Amount.Value.Float(), "value_eur": eur.Float(), "value_usd": usd.Float()})
	}
	return rows, nil
}

// valueAt mirrors the budget conversion: a posting price in EUR wins, otherwise the rate at end of the entry date.
func (s *Sources) valueAt(ctx context.Context, p principal.Principal, post ledger.Posting, target, date string, cache map[string]*big.Rat) (ledger.Decimal, error) {
	amount := post.Amount
	if amount.Commodity == target {
		return amount.Value, nil
	}
	if post.Price != nil && post.Price.Commodity == "EUR" {
		v, e := ledger.DecimalFromRat(new(big.Rat).Mul(amount.Value.Rat(), post.Price.Value.Rat()))
		if e != nil {
			return ledger.Decimal{}, e
		}
		amount = ledger.Amount{Value: v, Commodity: "EUR"}
		if target == "EUR" {
			return v, nil
		}
	}
	if s.Rates == nil {
		v, e := s.Valuer.Convert(ctx, p, amount, target, date+"T23:59:59Z")
		return v.Value, e
	}
	key := amount.Commodity + ">" + target + "@" + date
	r, ok := cache[key]
	if !ok {
		var e error
		if r, e = s.Rates.Rate(ctx, p, amount.Commodity, target, date+"T23:59:59Z"); e != nil {
			return ledger.Decimal{}, e
		}
		cache[key] = r
	}
	v, e := rates.Apply(amount, r, target)
	return v.Value, e
}

// sourceFields describes rows produced by the source cases above; dynamic fields use dyn.
var sourceFields = map[string]map[string]string{
	"journal":              {"id": "string", "seq": "double", "date": "string", "kind": "string", "payee": "string", "postings": "list", "tags": "list", "links": "list", "status": "string", "actor": "map", "created_at": "string"},
	"effective_entries":    {"id": "string", "seq": "double", "date": "string", "kind": "string", "payee": "string", "postings": "list", "tags": "list", "links": "list", "status": "string", "actor": "map", "created_at": "string"},
	"postings":             {"date": "string", "entry": "string", "seq": "double", "kind": "string", "payee": "string", "tags": "list", "account": "string", "class": "string", "groups": "map", "commodity": "string", "amount": "double", "value_eur": "double", "value_usd": "double"},
	"accounts":             {"account": "string", "pot": "string", "name": "string", "kind": "string", "class": "string", "groups": "map", "commodity": "string", "amount": "double", "usd": "double"},
	"budget_lines":         {"window_label": "string", "window_from": "string", "window_to": "string", "window_months": "double", "id": "string", "name": "string", "limit": "double", "avg": "double", "avg6": "double", "oct": "double", "actual": "double", "status": "string", "accounts": "list"},
	"months":               {"month": "string", "byLine": "map", "total": "double"},
	"range":                {"i": "double"},
	"scenarios":            {"id": "string", "name": "string", "spendUsd": "double", "endNetUsd": "double", "runway": "dyn", "savings_usd_mo": "double", "kyiv_empty_month": "dyn", "runway_ex_gold": "dyn", "start_as_of": "string", "start_net_usd": "double", "journal_seq": "double"},
	"scenario:<id>":        {"id": "string", "name": "string", "spendUsd": "double", "endNetUsd": "double", "runway": "dyn", "savings_usd_mo": "double", "kyiv_empty_month": "dyn", "runway_ex_gold": "dyn", "start_as_of": "string", "start_net_usd": "double", "journal_seq": "double"},
	"income":               {"id": "string", "amount": "double", "currency": "string", "frequency": "string", "start": "string", "end": "string", "growth_pct": "double", "reimbursement": "bool", "kind": "string", "payer_match": "string", "lands_in": "string", "gross_or_net": "string"},
	"rates:<pairs>":        {"pair": "string", "base": "string", "quote": "string", "at": "string", "value": "double", "source": "string"},
	"rate_sync":            {"provider": "string", "status": "string", "last_attempt": "string", "last_success": "string", "error": "string", "consecutive_failures": "double", "newest_at": "string", "newest_value": "string"},
	"rate_history:<pairs>": {"pair": "string", "at": "string", "value": "double", "source": "string"},
	"balance_history":      {"month": "string", "date": "string", "assets": "double", "liabilities": "double", "net": "double"},
	"forecast_series:<id>": {"m": "double", "net": "double", "cash": "double", "wise": "double", "kyiv": "double", "gold": "double"},
	"income_actuals":       {"stream": "string", "month": "string", "currency": "string", "expected": "double", "actual": "double", "variance": "double", "entry_ids": "list"},
}
