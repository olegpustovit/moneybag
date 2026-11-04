package tables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type JoinedEntry struct {
	ID             string          `json:"id"`
	Date           string          `json:"date"`
	Payee          string          `json:"payee"`
	Accounts       []string        `json:"accounts"`
	Amount         *ledger.Decimal `json:"amount"`
	Commodity      string          `json:"commodity"`
	ValueEUR       *ledger.Decimal `json:"value_eur"`
	Effective      bool            `json:"effective"`
	Tags           []string        `json:"tags"`
	ValuationError string          `json:"valuation_error,omitempty"`
}

func (e JoinedEntry) fields() map[string]any {
	var amount, eur any
	if e.Amount != nil {
		amount = *e.Amount
	}
	if e.ValueEUR != nil {
		eur = *e.ValueEUR
	}
	return map[string]any{"id": e.ID, "date": e.Date, "payee": e.Payee, "accounts": e.Accounts, "amount": amount, "commodity": e.Commodity, "value_eur": eur, "effective": e.Effective, "tags": e.Tags, "valuation_error": e.ValuationError}
}
func (r Row) MarshalJSON() ([]byte, error) {
	type plain Row
	if r.joined {
		return json.Marshal(struct {
			plain
			Entry *JoinedEntry `json:"entry"`
		}{plain(r), r.Entry})
	}
	return json.Marshal(plain(r))
}
func (s *Service) enrich(ctx context.Context, p principal.Principal, t Table, rows []Row, join bool, expand []string) error {
	ctx = reqcache.With(ctx)
	if join {
		column := ""
		if t.Ledger != nil {
			column = t.Ledger.Entry
		} else {
			for _, c := range t.Columns {
				if c.Type == "entry" {
					column = c.ID
					break
				}
			}
		}
		if column == "" {
			return fmt.Errorf("join_entry requires an entry column")
		}
		ids := []string{}
		for _, r := range rows {
			if id, ok := r.Cells[column].(string); ok {
				ids = append(ids, id)
			}
		}
		if s.Ledger == nil {
			return fmt.Errorf("ledger joins are not configured")
		}
		entries, err := s.Ledger.GetMany(ctx, p, slices.Compact(slices.Sorted(slices.Values(ids))))
		if err != nil {
			return err
		}
		joined := map[string]*JoinedEntry{}
		cache := map[string]*big.Rat{}
		for id, entry := range entries {
			effective, err := s.Ledger.IsEffective(ctx, p, entry)
			if err != nil {
				return err
			}
			e := &JoinedEntry{ID: id, Date: entry.Date, Payee: entry.Payee, Accounts: []string{}, Tags: entry.Tags, Effective: effective}
			sum, eur := new(big.Rat), new(big.Rat)
			count := 0
			single, valued := true, true
			for i, post := range entry.Postings {
				if !slices.Contains(e.Accounts, post.Account) {
					e.Accounts = append(e.Accounts, post.Account)
				}
				selected := i == 0
				if t.Ledger != nil {
					selected = false
					for _, prefix := range t.Ledger.Accounts {
						if strings.HasPrefix(post.Account, prefix) {
							selected = true
							break
						}
					}
				}
				if !selected {
					continue
				}
				if count == 0 {
					e.Commodity = post.Amount.Commodity
				} else if e.Commodity != post.Amount.Commodity {
					single = false
				}
				count++
				sum.Add(sum, post.Amount.Value.Rat())
				value := post.Amount.Value
				if s.Rates != nil {
					value, err = s.Rates.ValueAt(ctx, p, post, "EUR", entry.Date, cache)
				} else if post.Amount.Commodity != "EUR" {
					err = rates.ErrNoQuote
				}
				if err != nil {
					if !errors.Is(err, rates.ErrNoQuote) {
						return err
					}
					valued = false
					e.ValuationError = err.Error()
					err = nil
				} else {
					eur.Add(eur, value.Rat())
				}
			}
			if count > 0 && single {
				v, err := ledger.DecimalFromRat(sum)
				if err != nil {
					return err
				}
				e.Amount = &v
			}
			if count > 0 && valued {
				v, err := ledger.DecimalFromRat(eur)
				if err != nil {
					return err
				}
				e.ValueEUR = &v
			}
			joined[id] = e
		}
		for i := range rows {
			id, _ := rows[i].Cells[column].(string)
			rows[i].Entry = joined[id]
			rows[i].joined = true
		}
	}
	for _, id := range expand {
		var column Column
		for _, c := range t.Columns {
			if c.ID == id {
				column = c
				break
			}
		}
		if _, err := s.Get(ctx, p, column.Table); err != nil {
			return err
		}
		ids := []string{}
		for _, r := range rows {
			if value, ok := r.Cells[id].(string); ok {
				ids = append(ids, value)
			}
		}
		targets := map[string]Row{}
		for start := 0; start < len(ids); start += 500 {
			cursor, err := s.repo.db.Collection("rows").Find(ctx, bson.M{"user_id": p.UserID, "table_id": column.Table, "id": bson.M{"$in": ids[start:min(start+500, len(ids))]}, "archived": bson.M{"$ne": true}})
			if err != nil {
				return err
			}
			for cursor.Next(ctx) {
				var r Row
				if err = cursor.Decode(&r); err != nil {
					break
				}
				targets[r.ID] = r
			}
			ce := cursor.Err()
			_ = cursor.Close(ctx)
			if err != nil {
				return err
			}
			if ce != nil {
				return ce
			}
		}
		for i := range rows {
			value, _ := rows[i].Cells[id].(string)
			rows[i].Cells = maps.Clone(rows[i].Cells)
			rows[i].Cells[id] = nil
			if target, ok := targets[value]; ok {
				fields := maps.Clone(target.Cells)
				if fields == nil {
					fields = map[string]any{}
				}
				fields["_row_id"] = target.ID
				if _, ok := fields["id"]; !ok {
					fields["id"] = target.ID
				}
				rows[i].Cells[id] = expressions.Reference(target.ID, fields)
			}
		}
	}
	return nil
}
