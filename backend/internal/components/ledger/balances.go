package ledger

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"math/big"
	"sort"
	"strings"
	"time"
)

type Valuer interface {
	Convert(context.Context, principal.Principal, Amount, string, string) (Amount, error)
}
type BalancesInput struct {
	At          string   `json:"at,omitempty"`
	Accounts    []string `json:"accounts,omitempty"`
	GroupBy     string   `json:"group_by,omitempty"`
	ValueIn     string   `json:"value_in,omitempty"`
	IncludeLots bool     `json:"include_lots,omitempty"`
}
type BalanceRow struct {
	Key     string   `json:"key"`
	Amounts []Amount `json:"amounts"`
	Value   *Amount  `json:"value,omitempty"`
}
type BalancesResult struct {
	Rows []BalanceRow `json:"rows"`
	AsOf string       `json:"as_of"`
	Lots []Lot        `json:"lots,omitempty"`
}

type rawBalance struct {
	ID struct {
		Account   string `bson:"account"`
		Commodity string `bson:"commodity"`
	} `bson:"_id"`
	Amount Decimal `bson:"amount"`
}

func (s *Service) Balances(ctx context.Context, p principal.Principal, in BalancesInput, valuer Valuer) (BalancesResult, error) {
	if e := p.Require("ledger:read"); e != nil {
		return BalancesResult{}, e
	}
	if in.At == "" {
		in.At = time.Now().Format("2006-01-02")
	}
	if _, e := time.Parse("2006-01-02", in.At); e != nil {
		return BalancesResult{}, fmt.Errorf("at must be YYYY-MM-DD")
	}
	if len(in.Accounts) > 100 || in.ValueIn != "" && in.ValueIn != "USD" && in.ValueIn != "EUR" {
		return BalancesResult{}, fmt.Errorf("at most 100 accounts; value_in must be USD or EUR")
	}
	if in.GroupBy == "" {
		in.GroupBy = "account"
	}
	if in.GroupBy != "account" && in.GroupBy != "commodity" && in.GroupBy != "pot" && in.GroupBy != "type" {
		return BalancesResult{}, fmt.Errorf("group_by must be account, pot, commodity or type")
	}
	db, e := s.repo.db(p)
	if e != nil {
		return BalancesResult{}, e
	}
	match := bson.M{"user_id": p.UserID, "status": "posted", "date": bson.M{"$lte": in.At}}
	if len(in.Accounts) > 0 {
		match["postings.account"] = bson.M{"$in": in.Accounts}
	}
	postMatch := bson.M{}
	if len(in.Accounts) > 0 {
		postMatch["postings.account"] = bson.M{"$in": in.Accounts}
	}
	pipeline := bson.A{bson.M{"$match": match}, bson.M{"$unwind": "$postings"}, bson.M{"$match": postMatch}, bson.M{"$group": bson.M{"_id": bson.M{"account": "$postings.account", "commodity": "$postings.amount.c"}, "amount": bson.M{"$sum": "$postings.amount.v"}}}, bson.M{"$sort": bson.M{"_id.account": 1, "_id.commodity": 1}}, bson.M{"$limit": 5001}}
	var key strings.Builder
	fmt.Fprintf(&key, "balances|%p|%d:%s|%s", db, len(p.UserID), p.UserID, in.At)
	for _, account := range in.Accounts {
		fmt.Fprintf(&key, "|%d:%s", len(account), account)
	}
	raw, e := reqcache.Do(ctx, key.String(), func() ([]rawBalance, error) {
		cur, err := db.Collection("journal").Aggregate(ctx, pipeline)
		if err != nil {
			return nil, err
		}
		defer cur.Close(ctx)
		var rows []rawBalance
		err = cur.All(ctx, &rows)
		return rows, err
	})
	if e != nil {
		return BalancesResult{}, e
	}
	if len(raw) > 5000 {
		return BalancesResult{}, fmt.Errorf("balance result exceeds 5000 rows")
	}
	accounts, e := s.balanceAccounts(ctx, p, in.GroupBy)
	if e != nil {
		return BalancesResult{}, e
	}
	return s.balanceResult(ctx, p, in, valuer, raw, accounts)
}

func (s *Service) balanceAccounts(ctx context.Context, p principal.Principal, groupBy string) (map[string]Account, error) {
	accounts := map[string]Account{}
	if groupBy == "pot" || groupBy == "type" {
		for cursor := ""; ; {
			page, err := s.repo.Accounts(ctx, p, AccountsInput{IncludeClosed: true, Limit: 500, Cursor: cursor})
			if err != nil {
				return nil, err
			}
			for _, a := range page.Items {
				accounts[a.ID] = a
			}
			if cursor = page.NextCursor; cursor == "" {
				break
			}
		}
	}
	return accounts, nil
}
func (s *Service) balanceResult(ctx context.Context, p principal.Principal, in BalancesInput, valuer Valuer, raw []rawBalance, accounts map[string]Account) (BalancesResult, error) {
	groups := map[string]map[string]*big.Rat{}
	for _, r := range raw {
		key := r.ID.Account
		switch in.GroupBy {
		case "commodity":
			key = r.ID.Commodity
		case "pot", "type":
			a, ok := accounts[r.ID.Account]
			if !ok {
				return BalancesResult{}, fmt.Errorf("account %s not found", r.ID.Account)
			}
			if in.GroupBy == "pot" {
				key = a.Pot
				if key == "" {
					key = a.ID
				}
			} else {
				key = a.Type
			}
		}
		if groups[key] == nil {
			groups[key] = map[string]*big.Rat{}
		}
		if groups[key][r.ID.Commodity] == nil {
			groups[key][r.ID.Commodity] = new(big.Rat)
		}
		groups[key][r.ID.Commodity].Add(groups[key][r.ID.Commodity], r.Amount.Rat())
	}
	out := BalancesResult{Rows: []BalanceRow{}, AsOf: in.At}
	keys := []string{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		row := BalanceRow{Key: k, Amounts: []Amount{}}
		commodities := []string{}
		for c := range groups[k] {
			commodities = append(commodities, c)
		}
		sort.Strings(commodities)
		total := new(big.Rat)
		for _, c := range commodities {
			d, e := DecimalFromRat(groups[k][c])
			if e != nil {
				return BalancesResult{}, e
			}
			a := Amount{d, c}
			row.Amounts = append(row.Amounts, a)
			if in.ValueIn != "" {
				if valuer == nil {
					return BalancesResult{}, fmt.Errorf("valuation unavailable")
				}
				v, e := valuer.Convert(ctx, p, a, in.ValueIn, in.At+"T23:59:59Z")
				if e != nil {
					return BalancesResult{}, e
				}
				total.Add(total, v.Value.Rat())
			}
		}
		if in.ValueIn != "" {
			d, e := ParseDecimal(total.FloatString(2))
			if e != nil {
				return BalancesResult{}, e
			}
			v := Amount{d, in.ValueIn}
			row.Value = &v
		}
		out.Rows = append(out.Rows, row)
	}
	if in.IncludeLots {
		lots, err := s.lots(ctx, p, in, valuer)
		if err != nil {
			return BalancesResult{}, err
		}
		out.Lots = lots
	}
	return out, nil
}
