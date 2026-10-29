// Package consistency reads the ledger, model, tables and dashboards and reports where one fact is stored twice or disagrees.
package consistency

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
)

type Service struct {
	Ledger    *ledger.Service
	Reconcile *reconcile.Service
	Dashboard *dashboard.Service
	Model     *model.Service
	Tables    *tables.Service
	Budget    *budget.Service
	Rates     *rates.Service
}
type Check struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Detail any    `json:"detail"`
}
type Result struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

const tolerance = 0.005

// netTitle is the heuristic for "this value claims to be net worth": component id or title mentions net worth or total pots.
var netTitle = []string{"net worth", "networth", "net_worth", "total pots", "total_pots", "pots_total"}

func isNetWorth(c dashboard.Component) bool {
	s := strings.ToLower(c.ID + " " + c.Title)
	for _, n := range netTitle {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// receivable falls back to the account id until accounts carry a class.
func receivable(id string) bool {
	return strings.Contains(id, "receivable") || strings.Contains(id, "reimbursable")
}

func (s *Service) entries(ctx context.Context, p principal.Principal) ([]ledger.Entry, error) {
	var out []ledger.Entry
	for cursor := int64(0); ; {
		page, e := s.Ledger.Entries(ctx, p, ledger.EntriesInput{Status: "posted", Limit: 500, Cursor: cursor})
		if e != nil {
			return nil, e
		}
		out = append(out, page.Items...)
		if page.NextCursor == 0 || len(page.Items) == 0 {
			return out, nil
		}
		cursor = page.NextCursor
	}
}
func (s *Service) accounts(ctx context.Context, p principal.Principal) ([]ledger.Account, error) {
	var out []ledger.Account
	for cursor := ""; ; {
		page, e := s.Ledger.Accounts(ctx, p, ledger.AccountsInput{IncludeClosed: true, Limit: 500, Cursor: cursor})
		if e != nil {
			return nil, e
		}
		out = append(out, page.Items...)
		if page.NextCursor == "" {
			return out, nil
		}
		cursor = page.NextCursor
	}
}

// netWorth is assets plus liabilities valued in cur at today's rates.
func (s *Service) netWorth(ctx context.Context, p principal.Principal, cur string) (float64, error) {
	res, e := s.Ledger.Balances(ctx, p, ledger.BalancesInput{GroupBy: "type", ValueIn: cur}, s.Rates)
	if e != nil {
		return 0, e
	}
	total := 0.0
	for _, r := range res.Rows {
		if (r.Key == "asset" || r.Key == "liability") && r.Value != nil {
			total += r.Value.Value.Float()
		}
	}
	return total, nil
}

type published struct {
	Spec     dashboard.Spec
	Rendered dashboard.Rendered
}

// SpecLint runs the spec-only lints (orphans and literals) for one dashboard; it needs no ledger access.
func (s *Service) SpecLint(ctx context.Context, p principal.Principal, spec dashboard.Spec) ([]string, error) {
	tbl, sc, e := s.known(ctx, p)
	if e != nil {
		return nil, e
	}
	var out []string
	for _, o := range SpecOrphans(spec, tbl, sc) {
		out = append(out, "orphan: "+o)
	}
	for _, l := range SpecLiterals(spec) {
		out = append(out, fmt.Sprintf("literal %s in component %s field %s", l.Literal, l.ComponentID, l.Field))
	}
	return out, nil
}
func (s *Service) known(ctx context.Context, p principal.Principal) (map[string]bool, map[string]bool, error) {
	tl, e := s.Tables.List(ctx, p)
	if e != nil {
		return nil, nil, e
	}
	st, e := s.Model.Get(ctx, p)
	if e != nil {
		return nil, nil, e
	}
	tbl, sc := map[string]bool{}, map[string]bool{}
	for _, t := range tl {
		tbl[t.ID] = true
	}
	for _, x := range st.Scenarios {
		sc[x.ID] = true
	}
	return tbl, sc, nil
}

// runTimeout bounds one consistency run so the API and MCP tool cannot hang.
const runTimeout = 30 * time.Second

func (s *Service) Run(ctx context.Context, p principal.Principal) (Result, error) {
	ctx = ledger.WithAccountCache(rates.WithQuoteCache(reqcache.With(ctx))) // sources, quotes and accounts are read once per request
	if e := p.Require("ledger:read"); e != nil {
		return Result{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	res, e := s.run(ctx, p)
	if e != nil && ctx.Err() != nil {
		return Result{Checks: []Check{{"consistency", false, "timed out"}}}, nil
	}
	return res, e
}

func (s *Service) run(ctx context.Context, p principal.Principal) (Result, error) {
	entries, e := s.entries(ctx, p)
	if e != nil {
		return Result{}, e
	}
	accts, e := s.accounts(ctx, p)
	if e != nil {
		return Result{}, e
	}
	state, e := s.Model.Get(ctx, p)
	if e != nil {
		return Result{}, e
	}
	lines, e := s.Budget.Lines(ctx, p)
	if e != nil {
		return Result{}, e
	}
	tl, e := s.Tables.List(ctx, p)
	if e != nil {
		return Result{}, e
	}
	heads, e := s.Dashboard.List(ctx, p)
	if e != nil {
		return Result{}, e
	}
	// Render published dashboards in parallel; they share the request caches.
	slots := make([]*published, len(heads))
	errs := make([]string, len(heads))
	var wg sync.WaitGroup
	for i, h := range heads {
		if h.PublishedVersion == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.Dashboard.Get(ctx, p, h.ID, "published")
			if e != nil {
				errs[i] = h.ID + ": " + e.Error()
				return
			}
			r, e := s.Dashboard.Preview(ctx, p, h.ID, "published", nil)
			if e != nil {
				errs[i] = h.ID + ": " + e.Error()
			}
			slots[i] = &published{v.Spec, r}
		}()
	}
	wg.Wait()
	var dashes []published
	var failed []string
	for i := range heads {
		if slots[i] != nil {
			dashes = append(dashes, *slots[i])
		}
		if errs[i] != "" {
			failed = append(failed, errs[i])
		}
	}
	var checks []Check
	add := func(id string, bad any, empty bool) {
		checks = append(checks, Check{id, empty, bad})
	}
	list := func(xs []string) any {
		if len(xs) == 0 {
			return "ok"
		}
		return xs
	}

	// 1. trial balance per commodity
	sums := map[string]*big.Rat{}
	for _, en := range entries {
		for _, po := range en.Postings {
			if sums[po.Amount.Commodity] == nil {
				sums[po.Amount.Commodity] = new(big.Rat)
			}
			sums[po.Amount.Commodity].Add(sums[po.Amount.Commodity], po.Amount.Value.Rat())
		}
	}
	var bad []string
	for c, r := range sums {
		if r.Sign() != 0 {
			bad = append(bad, c+" off by "+r.FloatString(4))
		}
	}
	sort.Strings(bad)
	add("trial_balance", list(bad), len(bad) == 0)

	// 2. assertions: open discrepancies
	bad = nil
	status, e := s.Reconcile.Status(ctx, p, "")
	if e != nil {
		return Result{}, e
	}
	for _, st := range status {
		for _, d := range st.Discrepancies {
			if d.Status == "open" {
				bad = append(bad, fmt.Sprintf("%s open discrepancy %s %s", d.Account, d.Diff.Value, d.Diff.Commodity))
			}
		}
	}
	add("balance_assertions", list(bad), len(bad) == 0)

	// 3. net-worth figures. shortcut: heuristic by component id/title, compared in USD or EUR; computed values are not exposed by the engine.
	bad = append([]string{}, failed...)
	if len(dashes) > 0 {
		usd, e1 := s.netWorth(ctx, p, "USD")
		eur, e2 := s.netWorth(ctx, p, "EUR")
		if e1 != nil && e2 != nil {
			bad = append(bad, "cannot value ledger net worth: "+e1.Error())
		}
		for _, d := range dashes {
			for _, c := range d.Spec.Components {
				value, _ := d.Rendered.Values[c.ID].(map[string]any)
				v, ok := value["value"].(float64)
				if !ok || !isNetWorth(c) {
					continue
				}
				if (e1 != nil || math.Abs(v-usd) > tolerance) && (e2 != nil || math.Abs(v-eur) > tolerance) {
					bad = append(bad, fmt.Sprintf("%s/%s shows %.2f, ledger net worth is %.2f USD / %.2f EUR", d.Spec.ID, c.ID, v, usd, eur))
				}
			}
		}
	}
	add("dashboard_net_worth", list(bad), len(bad) == 0)

	// 4-6 inputs
	tbl, sc := map[string]bool{}, map[string]bool{}
	for _, t := range tl {
		tbl[t.ID] = true
	}
	for _, x := range state.Scenarios {
		sc[x.ID] = true
	}
	assumptions := map[string]bool{}
	var inputs []Input
	for _, r := range state.Assumptions {
		assumptions[r.ID] = true
		if v, ok := r.Cells["value"].(float64); ok {
			inputs = append(inputs, Input{Name: "assumption " + r.ID, Value: v})
		}
	}
	for _, st := range state.Streams {
		inputs = append(inputs, Input{Name: "income stream " + st.ID, Value: st.Amount})
	}
	for _, l := range lines {
		inputs = append(inputs, Input{Name: "budget line " + l.ID, Value: l.Limit.Float()})
	}
	seen := map[string]bool{}
	for _, en := range entries {
		for _, po := range en.Postings {
			if c := po.Amount.Commodity; c != "USD" && !seen[c] {
				seen[c] = true
				if q, e := s.Rates.Get(ctx, p, []string{c + "/USD"}, ""); e == nil && len(q) > 0 {
					inputs = append(inputs, Input{Name: "rate " + c + "/USD", Value: q[0].Value.Float(), Exact: true})
				}
			}
		}
	}

	// rates_fresh
	stale, e := s.Rates.Stale(ctx, p, time.Now())
	if e != nil {
		return Result{}, e
	}
	add("rates_fresh", list(stale), len(stale) == 0)

	// 4. orphans
	bad = nil
	blob := ""
	for _, d := range dashes {
		for _, o := range SpecOrphans(d.Spec, tbl, sc) {
			bad = append(bad, d.Spec.ID+": "+o)
		}
		blob += fmt.Sprint(d.Spec)
	}
	streamIDs := map[string]bool{}
	for _, st := range state.Streams {
		streamIDs[st.ID] = true
	}
	for _, x := range state.Scenarios {
		for _, o := range x.Overrides {
			// Stream-id overrides and ref overrides are valid without a stored assumption.
			if !assumptions[o.Assumption] && !streamIDs[o.Assumption] && !slices.Contains(model.EngineAssumptions, o.Assumption) && o.Ref == "" {
				bad = append(bad, fmt.Sprintf("scenario %s overrides missing assumption %s", x.ID, o.Assumption))
			}
			blob += o.Assumption + " " + o.Expr + " " + o.Ref + " "
		}
	}
	// shortcut: assumption use is a substring search, so engine-internal names (horizon) can still show as unused.
	for k := range assumptions {
		if !strings.Contains(blob, k) {
			bad = append(bad, "assumption "+k+" is never referenced")
		}
	}
	sort.Strings(bad)
	add("orphans", list(bad), len(bad) == 0)

	// 5. literals in specs
	var lits []Literal
	for _, d := range dashes {
		lits = append(lits, SpecLiterals(d.Spec)...)
	}
	add("spec_literals", litDetail(lits), len(lits) == 0)

	// 6. model-input duplicates in scenarios, dashboards and user tables
	all := append([]Literal{}, lits...)
	for _, x := range state.Scenarios {
		for _, o := range x.Overrides {
			if o.Value != nil {
				all = append(all, num("scenario:"+x.ID, "override."+o.Assumption, *o.Value))
			}
			for _, l := range ScanText(o.Expr) {
				l.ComponentID, l.Field = "scenario:"+x.ID, "override."+o.Assumption+".expr"
				all = append(all, l)
			}
		}
		for i, ev := range x.Events {
			all = append(all, num("scenario:"+x.ID, fmt.Sprintf("events.%d.amount", i), ev.Amount))
		}
		if lg := x.Legacy; lg != nil {
			for f, v := range map[string]float64{"salaryBiweeklyUsd": lg.SalaryBiweeklyUSD, "reimbursementBiweeklyUsd": lg.ReimbursementBiweeklyUSD, "sideIncomeMonthlyUsd": lg.SideIncomeMonthlyUSD, "kyivExtraDrainMonthlyUsd": lg.KyivExtraDrainMonthlyUSD, "bufferUsd": lg.BufferUSD} {
				all = append(all, num("scenario:"+x.ID, "legacy."+f, v))
			}
			for _, sp := range lg.Spend {
				all = append(all, num("scenario:"+x.ID, "legacy.spend."+sp.Name, sp.Amount))
			}
		}
	}
	var numberCols []string
	for _, t := range tl {
		var cols []string
		for _, c := range t.Columns {
			if c.Type == "number" {
				cols = append(cols, c.ID)
			}
		}
		if len(cols) == 0 {
			continue
		}
		// tables have no kind metadata yet, so every number column is listed as info (check 11) and the model table is the source, not a copy.
		numberCols = append(numberCols, fmt.Sprintf("%s: %s", t.ID, strings.Join(cols, ",")))
		if t.ID == "assumptions" {
			continue
		}
		q, e := s.Tables.Query(ctx, p, tables.QueryInput{DB: t.ID})
		if e != nil {
			return Result{}, e
		}
		for _, r := range q.Rows {
			for _, c := range cols {
				if v, ok := r.Cells[c].(float64); ok {
					all = append(all, num("table:"+t.ID, r.ID+"."+c, v))
				}
			}
		}
	}
	dups := Duplicates(inputs, all, func(l Literal) string { return strings.Trim(l.Dashboard+"/"+l.ComponentID+"/"+l.Field, "/") })
	add("model_input_duplicates", dupDetail(dups), len(dups) == 0)

	// 7. receivables: balance and age. shortcut: age runs from the entry that took the account off zero; falls back on id until accounts carry a class.
	checks = append(checks, Check{"receivables", true, receivables(entries, accts)})

	// 8. reversals
	bad = nil
	byID := map[string]bool{}
	for _, en := range entries {
		byID[en.ID] = true
	}
	reversed := map[string]int{}
	for _, en := range entries {
		for _, l := range en.Links {
			if l.Rel == "reverses" {
				reversed[l.ID]++
				if !byID[l.ID] {
					bad = append(bad, fmt.Sprintf("entry %s reverses missing %s", en.ID, l.ID))
				}
			}
		}
	}
	for id, n := range reversed {
		if n > 1 {
			bad = append(bad, fmt.Sprintf("entry %s reversed %d times", id, n))
		}
	}
	sort.Strings(bad)
	add("reversals", list(bad), len(bad) == 0)

	// 9. budget mapping covers expense accounts with postings
	mapped := map[string]bool{}
	for _, l := range lines {
		for _, a := range l.Accounts {
			mapped[a] = true
		}
	}
	used := map[string]bool{}
	for _, en := range entries {
		for _, po := range en.Postings {
			if strings.HasPrefix(po.Account, "expenses:") && !mapped[po.Account] {
				used[po.Account] = true
			}
		}
	}
	bad = nil
	for a := range used {
		bad = append(bad, a)
	}
	sort.Strings(bad)
	add("budget_mapping", list(bad), len(bad) == 0)

	// 10. scenario start equals live net worth (assumption start_net_usd)
	detail, ok := any("no start_net_usd assumption"), true
	for _, r := range state.Assumptions {
		if src, e := s.Model.StartSource(ctx, p); e == nil && src == "ledger" {
			detail, ok = "start resolves live from the ledger; the stored start_net_usd assumption is unused", true
			break
		}
		if v, has := r.Cells["value"].(float64); r.ID == "start_net_usd" && has {
			live, e := s.netWorth(ctx, p, "USD")
			ok = e == nil && math.Abs(v-live) <= tolerance
			detail = fmt.Sprintf("assumption %.2f, ledger %.2f USD at %s", v, live, time.Now().Format("2006-01-02"))
			if e != nil {
				detail = "cannot value ledger net worth: " + e.Error()
			}
		}
	}
	checks = append(checks, Check{"scenario_start", ok, detail})

	// 11. info: number columns in user tables (R-21)
	sort.Strings(numberCols)
	checks = append(checks, Check{"table_number_columns", true, numberCols})

	out := Result{OK: true, Checks: checks}
	for _, c := range checks {
		out.OK = out.OK && c.OK
	}
	return out, nil
}

func num(where, field string, v float64) Literal {
	return Literal{ComponentID: where, Field: field, Literal: fmt.Sprint(v), Value: v}
}
func litDetail(l []Literal) any {
	if len(l) == 0 {
		return "ok"
	}
	return l
}
func dupDetail(d []Duplicate) any {
	if len(d) == 0 {
		return "ok"
	}
	return d
}

type receivableRow struct {
	Account   string `json:"account"`
	Commodity string `json:"commodity"`
	Balance   string `json:"balance"`
	Since     string `json:"oldest_unsettled,omitempty"`
	AgeDays   int    `json:"age_days,omitempty"`
}

func receivables(entries []ledger.Entry, accts []ledger.Account) []receivableRow {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Date == entries[j].Date {
			return entries[i].Seq < entries[j].Seq
		}
		return entries[i].Date < entries[j].Date
	})
	out := []receivableRow{}
	for _, a := range accts {
		if !receivable(a.ID) {
			continue
		}
		for _, commodity := range a.Commodities {
			bal, since := new(big.Rat), ""
			for _, en := range entries {
				for _, po := range en.Postings {
					if po.Account != a.ID || po.Amount.Commodity != commodity {
						continue
					}
					if bal.Sign() == 0 {
						since = en.Date
					}
					bal.Add(bal, po.Amount.Value.Rat())
					if bal.Sign() == 0 {
						since = ""
					}
				}
			}
			r := receivableRow{Account: a.ID, Commodity: commodity, Balance: bal.FloatString(2), Since: since}
			if t, e := time.Parse("2006-01-02", since); e == nil {
				r.AgeDays = int(time.Since(t).Hours() / 24)
			}
			out = append(out, r)
		}
	}
	return out
}
