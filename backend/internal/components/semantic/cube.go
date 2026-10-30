package semantic

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"
)

type CubeAccount struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Category  string `json:"category"`
	Type      string `json:"type"`
	Pot       string `json:"pot"`
	Source    string `json:"source"`
	Class     string `json:"class"`
	Estimated bool   `json:"estimated"`
}
type CubeFlows struct {
	D []int   `json:"d"`
	A []int   `json:"a"`
	P []int   `json:"p"`
	T []int   `json:"t"`
	V []int64 `json:"v"`
	N []int   `json:"n"`
}
type CubeBalances struct {
	D              []int   `json:"d"`
	Net            []int64 `json:"net"`
	Assets         []int64 `json:"assets"`
	Liabilities    []int64 `json:"liabilities"`
	EstimatedUntil string  `json:"estimated_until,omitempty"`
}
type CubeRate struct {
	D []int     `json:"d"`
	V []float64 `json:"v"`
}
type Cube struct {
	Revision    int64               `json:"revision"`
	AsOf        string              `json:"as_of"`
	Base        string              `json:"base"`
	Start       string              `json:"start"`
	End         string              `json:"end"`
	Accounts    []CubeAccount       `json:"accounts"`
	Payees      []string            `json:"payees"`
	TagSets     [][]string          `json:"tag_sets"`
	Flows       CubeFlows           `json:"flows"`
	Balances    CubeBalances        `json:"balances"`
	Rates       map[string]CubeRate `json:"rates"`
	Metrics     []Metric            `json:"metrics"`
	Annotations []Annotation        `json:"annotations"`
	Labels      map[string]string   `json:"labels"`
	// Unvalued counts postings in a commodity with no quote; they carry v=0, and exact figures come from metric_query.
	Unvalued int `json:"unvalued,omitempty"`
}

func cents(r *big.Rat) int64 {
	scaled := new(big.Rat).Mul(r, big.NewRat(100, 1))
	v, _ := scaled.Float64()
	return int64(math.Round(v))
}

// Cube builds the columnar ledger summary the browser explores locally. Equity legs are left out: no dimension reads them and they double every exchange.
func (s *Service) Cube(ctx context.Context, p principal.Principal, revision int64) (Cube, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Cube{}, e
	}
	ctx = reqcache.With(rates.WithQuoteCache(ctx))
	b, e := s.book(ctx, p)
	if e != nil {
		return Cube{}, e
	}
	today := time.Now().UTC().Format(day)
	out := Cube{Revision: revision, AsOf: today, Base: "EUR", Start: b.first, End: today, Accounts: []CubeAccount{}, Payees: []string{}, TagSets: [][]string{}, Rates: map[string]CubeRate{}, Labels: b.labels}
	if out.Start == "" {
		out.Start = today
	}
	start, e := time.Parse(day, out.Start)
	if e != nil {
		return Cube{}, e
	}
	offset := func(date string) int {
		t, _ := time.Parse(day, date)
		return int(t.Sub(start).Hours() / 24)
	}
	ids := make([]string, 0, len(b.accounts))
	for id := range b.accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	accountIndex := make(map[string]int, len(ids))
	for i, id := range ids {
		a := b.accounts[id]
		accountIndex[id] = i
		category := a.Groups["category"]
		if category == "" {
			category = b.accountLabel(a, id)
		} else {
			category = b.display("category", category)
		}
		out.Accounts = append(out.Accounts, CubeAccount{id, b.accountLabel(a, id), category, a.Groups["type"], a.Pot, a.Groups["source"], a.Class, a.IsEstimated()})
	}
	payeeIndex := map[string]int{}
	tagIndex := map[string]int{}
	type flowKey struct{ d, a, p, t int }
	flows := map[flowKey]*struct {
		v *big.Rat
		n int
	}{}
	type holdKey struct{ kind, commodity string }
	holdings := map[holdKey]*big.Rat{}
	changed := map[string]bool{}
	for i := range b.rows {
		r := &b.rows[i]
		if i%1000 == 0 && ctx.Err() != nil {
			return Cube{}, ctx.Err()
		}
		if r.acct.Type == "asset" || r.acct.Type == "liability" {
			key := holdKey{r.acct.Type, r.commodity}
			if holdings[key] == nil {
				holdings[key] = new(big.Rat)
			}
			changed[r.date] = true
		}
		if strings.HasPrefix(r.account, "equity:") {
			continue
		}
		pi, ok := payeeIndex[r.name]
		if !ok {
			pi = len(out.Payees)
			payeeIndex[r.name] = pi
			out.Payees = append(out.Payees, r.name)
		}
		sorted := append([]string(nil), r.tags...)
		sort.Strings(sorted)
		tagKey := strings.Join(sorted, "\x00")
		ti, ok := tagIndex[tagKey]
		if !ok {
			ti = len(out.TagSets)
			tagIndex[tagKey] = ti
			if sorted == nil {
				sorted = []string{}
			}
			out.TagSets = append(out.TagSets, sorted)
		}
		v, e := s.amountIn(ctx, p, b, r, "eur")
		if e != nil {
			out.Unvalued++
			v = new(big.Rat)
		}
		key := flowKey{offset(r.date), accountIndex[r.account], pi, ti}
		item := flows[key]
		if item == nil {
			item = &struct {
				v *big.Rat
				n int
			}{new(big.Rat), 0}
			flows[key] = item
		}
		item.v.Add(item.v, v)
		item.n++
		if r.estimated {
			out.Balances.EstimatedUntil = max(out.Balances.EstimatedUntil, r.date)
		}
	}
	keys := make([]flowKey, 0, len(flows))
	for key := range flows {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		x, y := keys[i], keys[j]
		if x.d != y.d {
			return x.d < y.d
		}
		if x.a != y.a {
			return x.a < y.a
		}
		if x.p != y.p {
			return x.p < y.p
		}
		return x.t < y.t
	})
	for _, key := range keys {
		item := flows[key]
		out.Flows.D = append(out.Flows.D, key.d)
		out.Flows.A = append(out.Flows.A, key.a)
		out.Flows.P = append(out.Flows.P, key.p)
		out.Flows.T = append(out.Flows.T, key.t)
		out.Flows.V = append(out.Flows.V, cents(item.v))
		out.Flows.N = append(out.Flows.N, item.n)
	}
	// Valuation days are the days holdings changed plus every month end and today, so a chart can read any month's closing balance.
	for t := start; !t.After(time.Now().UTC()); t = t.AddDate(0, 1, 0) {
		changed[time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Format(day)] = true
	}
	changed[today] = true
	dates := make([]string, 0, len(changed))
	for date := range changed {
		if date <= today {
			dates = append(dates, date)
		}
	}
	sort.Strings(dates)
	next := 0
	for _, date := range dates {
		for ; next < len(b.rows) && b.rows[next].date <= date; next++ {
			r := &b.rows[next]
			if r.acct.Type == "asset" || r.acct.Type == "liability" {
				holdings[holdKey{r.acct.Type, r.commodity}].Add(holdings[holdKey{r.acct.Type, r.commodity}], r.amount.Rat())
			}
		}
		assets, liabilities := new(big.Rat), new(big.Rat)
		for key, amount := range holdings {
			if amount.Sign() == 0 {
				continue
			}
			rate, e := s.rates.Rate(ctx, p, key.commodity, "EUR", date+"T23:59:59Z")
			if e != nil {
				continue // No quote yet for this commodity on this day; the earliest balances read low rather than failing the cube.
			}
			value := new(big.Rat).Mul(amount, rate)
			if key.kind == "asset" {
				assets.Add(assets, value)
			} else {
				liabilities.Add(liabilities, value)
			}
		}
		out.Balances.D = append(out.Balances.D, offset(date))
		out.Balances.Assets = append(out.Balances.Assets, cents(assets))
		out.Balances.Liabilities = append(out.Balances.Liabilities, cents(liabilities))
		out.Balances.Net = append(out.Balances.Net, cents(new(big.Rat).Add(assets, liabilities)))
	}
	if e = s.cubeRates(ctx, p, b, &out, offset); e != nil {
		return Cube{}, e
	}
	if out.Metrics, e = s.Metrics(ctx, p); e != nil {
		return Cube{}, e
	}
	if out.Annotations, e = s.Annotations(ctx, p, "trips", "", ""); e != nil {
		return Cube{}, e
	}
	return out, nil
}

// cubeRates keeps the last quote per day for each commodity against EUR, or against USD when only that exists, plus EUR/USD.
// shortcut: a pair above rates.History's 5000-row cap is dropped from the cube, which still has its values in flows; page History if a pair grows that long.
func (s *Service) cubeRates(ctx context.Context, p principal.Principal, b *book, out *Cube, offset func(string) int) error {
	commodities := map[string]bool{"USD": true}
	for i := range b.rows {
		commodities[b.rows[i].commodity] = true
	}
	delete(commodities, "EUR")
	names := make([]string, 0, len(commodities))
	for name := range commodities {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		pair := name + "/EUR"
		if name == "USD" {
			pair = "EUR/USD"
		}
		quotes, e := s.rates.History(ctx, p, []string{pair}, out.Start, out.End)
		if e == nil && len(quotes) == 0 && name != "USD" {
			pair = name + "/USD"
			quotes, e = s.rates.History(ctx, p, []string{pair}, out.Start, out.End)
		}
		if e != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("read cube rates: %w", e)
			}
			continue
		}
		rate := CubeRate{D: []int{}, V: []float64{}}
		for _, q := range quotes {
			d := offset(q.At[:10])
			value := ledger.Decimal(q.Value).Float()
			if n := len(rate.D); n > 0 && rate.D[n-1] == d {
				rate.V[n-1] = value
				continue
			}
			rate.D = append(rate.D, d)
			rate.V = append(rate.V, value)
		}
		if len(rate.D) > 0 {
			out.Rates[pair] = rate
		}
	}
	return nil
}
