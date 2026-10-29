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
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"strings"
	"time"
)

type Sources struct {
	Ledger *ledger.Service
	Budget *budget.Service
	Model  *model.Service
	Tables *tables.Service
	Valuer ledger.Valuer
	Rates  *rates.Service
}

func (s *Sources) Rows(ctx context.Context, p principal.Principal, source string) ([]map[string]any, error) {
	params, _ := ctx.Value(paramsKey{}).(map[string]any)
	month, _ := params["month"].(string)
	out := []map[string]any{}
	switch {
	case source == "journal":
		in := ledger.EntriesInput{Status: "posted", Limit: 100}
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
				break
			}
			in.Cursor = page.NextCursor
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
				out = append(out, map[string]any{"account": a.ID, "pot": a.Pot, "name": a.Name, "kind": a.Type, "commodity": v.Commodity, "amount": v.Value.Float(), "usd": value.Value.Float()})
			}
		}
	case source == "budget_lines":
		status, e := s.Budget.Get(ctx, p, month)
		if e != nil {
			return nil, e
		}
		for _, l := range status.Lines {
			out = append(out, map[string]any{"id": l.ID, "name": l.Name, "limit": l.Limit, "avg": l.Avg6, "avg6": l.Avg6, "oct": l.Actual, "actual": l.Actual, "status": l.Status})
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
		return nil, fmt.Errorf("unknown source %q; use journal, accounts, budget_lines, months, range, income, balance_history, rates:<PAIR[,PAIR]>, scenarios, scenario:<id>, forecast_series:<id>, income_actuals or table:<id>", source)
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
