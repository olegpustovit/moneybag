package model

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"time"
)

type ReconcileInput struct {
	StreamID string `json:"stream_id,omitempty" jsonschema:"income stream id; empty reconciles every stream"`
	From     string `json:"from" jsonschema:"first day, YYYY-MM-DD"`
	To       string `json:"to" jsonschema:"last day, YYYY-MM-DD"`
}
type IncomeMonth struct {
	Stream   string   `json:"stream"`
	Month    string   `json:"month"`
	Currency string   `json:"currency"`
	Expected float64  `json:"expected"`
	Actual   float64  `json:"actual"`
	Variance float64  `json:"variance"`
	EntryIDs []string `json:"entry_ids"`
}

// IncomeReconcile compares each stream's expected monthly amount with posted entries selected by payer_match.
// Actual is the net of postings on lands_in, or of income-type accounts (sign flipped) when lands_in is empty, converted to the stream currency.
// shortcut: expected ignores growth_pct and uses the average monthly amount (26/12 for biweekly); upgrade when pay-date counts matter.
func (s *Service) IncomeReconcile(ctx context.Context, p principal.Principal, in ReconcileInput) ([]IncomeMonth, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	from, e1 := time.Parse("2006-01-02", in.From)
	to, e2 := time.Parse("2006-01-02", in.To)
	if e1 != nil || e2 != nil || to.Before(from) || to.Sub(from) > 3660*24*time.Hour {
		return nil, fmt.Errorf("from and to must be YYYY-MM-DD, ordered, at most 10 years apart")
	}
	if s.Ledger == nil {
		return nil, fmt.Errorf("ledger unavailable")
	}
	state, e := s.Get(ctx, p)
	if e != nil {
		return nil, e
	}
	accounts, e := s.accountMap(ctx, p)
	if e != nil {
		return nil, e
	}
	var entries []ledger.Entry
	page := ledger.EntriesInput{From: in.From, To: in.To, Status: "posted", Limit: 100}
	for {
		res, e := s.Ledger.Entries(ctx, p, page)
		if e != nil {
			return nil, e
		}
		entries = append(entries, res.Items...)
		if res.NextCursor == 0 {
			break
		}
		if len(entries) > 20000 {
			return nil, fmt.Errorf("range holds too many entries; narrow from/to")
		}
		page.Cursor = res.NextCursor
	}
	out := []IncomeMonth{}
	found := in.StreamID == ""
	for _, st := range state.Streams {
		if in.StreamID != "" && st.ID != in.StreamID {
			continue
		}
		found = true
		expected := st.Amount
		switch st.Frequency {
		case "biweekly":
			expected *= 26.0 / 12
		case "yearly":
			expected /= 12
		}
		rows := map[string]*IncomeMonth{}
		for _, en := range entries {
			if st.PayerMatch != "" {
				r, e := s.expr.Eval(ctx, st.PayerMatch, map[string]any{"row": map[string]any{"payee": en.Payee, "tags": toAny(en.Tags)}})
				if e != nil {
					return nil, fmt.Errorf("stream %s payer_match: %w", st.ID, e)
				}
				if ok, _ := r.Value.(bool); !ok {
					continue
				}
			} else if st.LandsIn == "" {
				continue // nothing selects entries for this stream
			}
			sum, hit := 0.0, false
			for _, po := range en.Postings {
				if st.LandsIn != "" && po.Account != st.LandsIn || st.LandsIn == "" && accounts[po.Account].Type != "income" {
					continue
				}
				v := po.Amount.Value.Float()
				if st.LandsIn != "" && st.PayerMatch == "" && v <= 0 {
					continue // without a payer match only inflows to the account count
				}
				if po.Amount.Commodity != st.Currency {
					if s.Rates == nil {
						return nil, fmt.Errorf("rates unavailable")
					}
					c, e := s.Rates.Convert(ctx, p, po.Amount, st.Currency, en.Date+"T23:59:59Z")
					if e != nil {
						return nil, e
					}
					v = c.Value.Float()
				}
				if st.LandsIn == "" {
					v = -v
				}
				sum, hit = sum+v, true
			}
			if !hit || len(en.Date) < 7 {
				continue
			}
			r := rows[en.Date[:7]]
			if r == nil {
				r = &IncomeMonth{EntryIDs: []string{}}
				rows[en.Date[:7]] = r
			}
			r.Actual += sum
			r.EntryIDs = append(r.EntryIDs, en.ID)
		}
		for m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC); !m.After(to); m = m.AddDate(0, 1, 0) {
			end := m.AddDate(0, 1, -1).Format("2006-01-02")
			if st.Start != "" && st.Start > end || st.End != "" && st.End < m.Format("2006-01-02") {
				continue
			}
			key := m.Format("2006-01")
			r := rows[key]
			if r == nil {
				r = &IncomeMonth{EntryIDs: []string{}}
			}
			r.Stream, r.Month, r.Currency, r.Expected = st.ID, key, st.Currency, round(expected)
			r.Actual = round(r.Actual)
			r.Variance = round(r.Actual - r.Expected)
			out = append(out, *r)
		}
	}
	if !found {
		return nil, principal.ErrNotFound
	}
	return out, nil
}
func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// accountMap returns every account (closed included) by id.
func (s *Service) accountMap(ctx context.Context, p principal.Principal) (map[string]ledger.Account, error) {
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
