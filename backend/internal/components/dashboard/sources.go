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
	"maps"
	"math/big"
	"strings"
	"sync"
	"time"
)

type Sources struct {
	Ledger *ledger.Service
	Budget *budget.Service
	Model  *model.Service
	Tables *tables.Service
	Valuer ledger.Valuer
	Rates  *rates.Service
	Views  *Service // saved views (view:<id>); set once the dashboard service exists
	Expr   *expressions.Engine
}

type rowCacheKey struct{}

// rowCache keeps each source's rows for one request, so views and queries over the same source build it once.
type rowCache struct {
	mu   sync.Mutex
	rows map[string][]map[string]any
}

// WithRowCache scopes a row cache to ctx; use it only around reads.
func WithRowCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(rowCacheKey{}).(*rowCache); ok {
		return ctx
	}
	return context.WithValue(ctx, rowCacheKey{}, &rowCache{rows: map[string][]map[string]any{}})
}

func (s *Sources) Rows(ctx context.Context, p principal.Principal, source string) ([]map[string]any, error) {
	c, ok := ctx.Value(rowCacheKey{}).(*rowCache)
	if !ok {
		return s.rows(ctx, p, source)
	}
	params, _ := json.Marshal(ctx.Value(paramsKey{}))
	key := p.UserID + "|" + source + "|" + string(params)
	c.mu.Lock()
	cached, hit := c.rows[key]
	c.mu.Unlock()
	if !hit {
		rows, e := s.rows(ctx, p, source)
		if e != nil {
			return nil, e
		}
		c.mu.Lock()
		c.rows[key] = rows
		c.mu.Unlock()
		cached = rows
	}
	// Queries derive in place, so every caller gets its own row maps.
	out := make([]map[string]any, len(cached))
	for i, r := range cached {
		out[i] = maps.Clone(r)
	}
	return out, nil
}

func (s *Sources) rows(ctx context.Context, p principal.Principal, source string) ([]map[string]any, error) {
	params, _ := ctx.Value(paramsKey{}).(map[string]any)
	month, _ := params["month"].(string)
	out := []map[string]any{}
	switch {
	case source == "journal" || source == "effective_entries" || source == "postings":
		in := ledger.EntriesInput{Status: "posted", Limit: 500, Effective: source != "journal"}
		if source == "postings" {
			in.From, _ = params["from"].(string)
			in.To, _ = params["to"].(string)
		}
		cache := map[string]*big.Rat{}
		var accounts map[string]ledger.Account
		if source == "postings" {
			var e error
			if accounts, e = s.accountMap(ctx, p); e != nil {
				return nil, e
			}
		}
		bytes := 0
		for {
			page, e := s.Ledger.Entries(ctx, p, in)
			if e != nil {
				return nil, e
			}
			for _, entry := range page.Items {
				encoded, e := json.Marshal(entry)
				if e != nil {
					return nil, e
				}
				bytes += len(encoded)
				if bytes > 8*1024*1024 {
					return nil, fmt.Errorf("journal source exceeds 8 MiB")
				}
				if source == "postings" {
					rows, e := s.postingRows(ctx, p, entry, accounts, cache)
					if e != nil {
						return nil, e
					}
					out = append(out, rows...)
					continue
				}
				r, e := native(entry)
				if e != nil {
					return nil, e
				}
				out = append(out, r)
			}
			if page.NextCursor == 0 {
				break
			}
			if len(out) > 5000 {
				return nil, fmt.Errorf("journal source exceeds 5000 rows; use a date-filtered report")
			}
			in.Cursor = page.NextCursor
		}
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
			out = append(out, map[string]any{"window_label": status.Window.Label, "window_from": status.Window.From, "window_to": status.Window.To, "window_months": status.Window.Months, "id": l.ID, "name": l.Name, "limit": l.Limit, "avg": l.Avg6, "avg6": l.Avg6, "oct": l.Actual, "actual": l.Actual, "status": l.Status})
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
		for i := 11; i >= 0; i-- {
			end := time.Date(today.Year(), today.Month()-time.Month(i)+1, 0, 0, 0, 0, 0, time.UTC)
			if i == 0 {
				end = today
			}
			res, e := s.Ledger.Balances(ctx, p, ledger.BalancesInput{At: end.Format("2006-01-02"), GroupBy: "type", ValueIn: "USD"}, s.Valuer)
			if e != nil {
				return nil, e
			}
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
			out = append(out, map[string]any{"month": end.Format("2006-01"), "date": end.Format("2006-01-02"), "assets": assets, "liabilities": liabilities, "net": assets + liabilities})
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
