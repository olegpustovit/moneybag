package model

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"math"
	"strings"
	"time"
)

// RateUsed names the quote a live start was valued with.
type RateUsed struct {
	ID    string  `json:"id"`
	Pair  string  `json:"pair"`
	At    string  `json:"at"`
	Value float64 `json:"value"`
}

// ResolvedInput is the value a ref override resolved to.
type ResolvedInput struct {
	Assumption string  `json:"assumption"`
	Ref        string  `json:"ref"`
	Value      float64 `json:"value"`
}

// Provenance says where a projection's starting state came from.
type Provenance struct {
	StartAsOf   string          `json:"start_as_of"`
	StartSource string          `json:"start_source"`
	StartNetUSD float64         `json:"start_net_usd"`
	JournalSeq  int64           `json:"journal_seq"`
	RatesUsed   []RateUsed      `json:"rates_used"`
	Inputs      []ResolvedInput `json:"resolved_inputs"`
}

// liveStart values the ledger as of a date (default today, Europe/Zagreb). Pots are the account `pot` field: pots starting with "wise" (wise_usd, wise_eur) and pots containing "kyiv" (cash_kyiv) form those cash pots; gold is the XAU_g commodity.
// It returns nil values (stored assumptions are used) when no ledger, rates or balances are available.
func (s *Service) liveStart(ctx context.Context, p principal.Principal, asOf string) (map[string]float64, Provenance, error) {
	type start struct {
		live map[string]float64
		pr   Provenance
	}
	v, e := reqcache.Do(ctx, "live:"+p.UserID+":"+asOf, func() (start, error) {
		live, pr, e := s.computeStart(ctx, p, asOf)
		return start{live, pr}, e
	})
	return v.live, v.pr, e
}

func (s *Service) computeStart(ctx context.Context, p principal.Principal, asOf string) (map[string]float64, Provenance, error) {
	pr := Provenance{StartAsOf: asOf, StartSource: "assumptions", RatesUsed: []RateUsed{}, Inputs: []ResolvedInput{}}
	if asOf == "" {
		loc, e := time.LoadLocation("Europe/Zagreb")
		if e != nil {
			loc = time.UTC
		}
		pr.StartAsOf = time.Now().In(loc).Format("2006-01-02")
	} else if _, e := time.Parse("2006-01-02", asOf); e != nil {
		return nil, pr, fmt.Errorf("as_of must be YYYY-MM-DD")
	}
	if s.Ledger == nil || s.Rates == nil {
		return nil, pr, nil
	}
	at := pr.StartAsOf + "T23:59:59Z"
	bal, e := s.Ledger.Balances(ctx, p, ledger.BalancesInput{At: pr.StartAsOf}, nil)
	if e != nil || len(bal.Rows) == 0 {
		return nil, pr, e
	}
	fx, e := s.Rates.Get(ctx, p, []string{"EUR/USD"}, at)
	if e != nil {
		return nil, pr, nil
	}
	rateOf := func(q rates.Quote) RateUsed { return RateUsed{q.ID, q.Pair, q.At, q.Value.Float()} }
	pr.RatesUsed = append(pr.RatesUsed, rateOf(fx[0]))
	accounts, e := s.accountMap(ctx, p)
	if e != nil {
		return nil, pr, e
	}
	var net, wise, kyiv, grams float64
	for _, row := range bal.Rows {
		acc := accounts[row.Key]
		if acc.Type != "asset" && acc.Type != "liability" {
			continue
		}
		for _, amt := range row.Amounts {
			v, e := s.Rates.Convert(ctx, p, amt, "USD", at)
			if e != nil {
				return nil, pr, nil // an unvalued commodity: keep the stored assumptions rather than a partial net worth
			}
			usd := v.Value.Float()
			net += usd
			if amt.Commodity == "XAU_g" {
				grams += amt.Value.Float()
				continue
			}
			// Pots are named per account (wise_usd, wise_eur, cash_kyiv), so match by name.
			switch {
			case strings.HasPrefix(acc.Pot, "wise"):
				wise += usd
			case strings.Contains(acc.Pot, "kyiv"):
				kyiv += usd
			}
		}
	}
	live := map[string]float64{"start_net_usd": round(net), "wise_usd": round(wise), "kyiv_usd": round(kyiv), "gold_g": grams, "eurusd": fx[0].Value.Float(), "gold_usd_g": 0}
	if grams != 0 {
		gq, e := s.Rates.Get(ctx, p, []string{"XAU_g/USD"}, at)
		if e != nil {
			return nil, pr, nil
		}
		pr.RatesUsed = append(pr.RatesUsed, rateOf(gq[0]))
		live["gold_usd_g"] = gq[0].Value.Float()
	}
	if seq, e := s.Ledger.LatestSequence(ctx, p); e == nil {
		pr.JournalSeq = seq
	}
	pr.StartSource = "ledger"
	return live, pr, nil
}

// StartSource reports whether today's projection start resolves from the "ledger" or the stored "assumptions".
func (s *Service) StartSource(ctx context.Context, p principal.Principal) (string, error) {
	_, pr, e := s.liveStart(ctx, p, "")
	return pr.StartSource, e
}

func validRef(o Override) error {
	kind, id, _ := strings.Cut(o.Ref, ":")
	if o.Ref != "budget_lines.total" && !((kind == "budget_line" || kind == "income_stream" || kind == "view") && id != "") {
		return fmt.Errorf("ref must be budget_line:<id>, budget_lines.total, income_stream:<id> or view:<id>[.<column>]")
	}
	if !strings.HasSuffix(o.Assumption, "_usd") && !strings.HasSuffix(o.Assumption, "_eur") {
		return fmt.Errorf("ref overrides need an assumption ending in _usd or _eur")
	}
	if o.Factor != nil && (math.IsInf(*o.Factor, 0) || math.IsNaN(*o.Factor)) {
		return fmt.Errorf("finite factor required")
	}
	return nil
}

// resolveRef reads the referenced input in its own currency and converts it to the override assumption's currency.
func (s *Service) resolveRef(ctx context.Context, p principal.Principal, o Override, streams []Income, eurusd float64) (float64, error) {
	v, cur := 0.0, "EUR" // budget lines are EUR
	kind, id, _ := strings.Cut(o.Ref, ":")
	found := false
	if kind == "view" {
		// view:<id>.<column> reads the first row; the value is taken in the assumption's own currency.
		vid, col, ok := strings.Cut(id, ".")
		if !ok {
			col = "value"
		}
		if s.Views == nil {
			return 0, fmt.Errorf("ref %s: views unavailable", o.Ref)
		}
		rows, e := s.Views(ctx, p, vid)
		if e != nil {
			return 0, fmt.Errorf("ref %s: %w", o.Ref, e)
		}
		if len(rows) == 0 {
			return 0, fmt.Errorf("ref %s: view returned no rows", o.Ref)
		}
		n, ok := rows[0][col].(float64)
		if !ok {
			return 0, fmt.Errorf("ref %s: column %s is not a number", o.Ref, col)
		}
		found, v, cur = true, n, "EUR"
		if strings.HasSuffix(o.Assumption, "_usd") {
			cur = "USD"
		}
	} else if kind == "income_stream" {
		for _, st := range streams {
			if st.ID == id {
				found, v, cur = true, st.Amount, st.Currency
				switch st.Frequency {
				case "biweekly":
					v *= 26.0 / 12
				case "yearly":
					v /= 12
				}
			}
		}
	} else {
		if s.Budget == nil {
			return 0, fmt.Errorf("ref %s: budget unavailable", o.Ref)
		}
		lines, e := s.Budget.Lines(ctx, p)
		if e != nil {
			return 0, e
		}
		total := o.Ref == "budget_lines.total"
		found = total
		for _, l := range lines {
			if total || l.ID == id {
				found = true
				v += l.Limit.Float()
			}
		}
	}
	if !found {
		return 0, fmt.Errorf("ref %s not found", o.Ref)
	}
	if eurusd <= 0 {
		return 0, fmt.Errorf("ref %s: positive eurusd required", o.Ref)
	}
	if strings.HasSuffix(o.Assumption, "_usd") && cur == "EUR" {
		v *= eurusd
	} else if strings.HasSuffix(o.Assumption, "_eur") && cur == "USD" {
		v /= eurusd
	}
	if o.Factor != nil {
		v *= *o.Factor
	}
	return v, nil
}
