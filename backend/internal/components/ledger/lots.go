package ledger

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Lot groups holdings with the same explicit per-unit acquisition cost.
type Lot struct {
	Account      string  `json:"account"`
	Amount       Amount  `json:"amount"`
	UnitCost     *Amount `json:"unit_cost,omitempty"`
	CostBasis    *Amount `json:"cost_basis,omitempty"`
	Value        *Amount `json:"value,omitempty"`
	UnrealizedPL *Amount `json:"unrealized_pl,omitempty"`
}

func costLots(entries []Entry) ([]Lot, error) {
	type position struct {
		lot      Lot
		quantity *big.Rat
	}
	positions := map[string]*position{}
	for _, entry := range entries {
		for _, post := range entry.Postings {
			key := post.Account + "\x00" + post.Amount.Commodity + "\x00"
			if post.Cost != nil {
				key += post.Cost.Commodity + ":" + post.Cost.Value.Rat().RatString()
			}
			v := positions[key]
			if v == nil {
				v = &position{lot: Lot{Account: post.Account, Amount: Amount{Commodity: post.Amount.Commodity}, UnitCost: post.Cost}, quantity: new(big.Rat)}
				positions[key] = v
				if len(positions) > 5000 {
					return nil, fmt.Errorf("cost lot report exceeds 5000 lots; select accounts")
				}
			}
			v.quantity.Add(v.quantity, post.Amount.Value.Rat())
		}
	}
	keys := make([]string, 0, len(positions))
	for key := range positions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []Lot{}
	for _, key := range keys {
		v := positions[key]
		if v.quantity.Sign() == 0 {
			continue
		}
		amount, err := DecimalFromRat(v.quantity)
		if err != nil {
			return nil, err
		}
		v.lot.Amount.Value = amount
		if cost := v.lot.UnitCost; cost != nil {
			basis, err := DecimalFromRat(new(big.Rat).Mul(v.quantity, cost.Value.Rat()))
			if err != nil {
				return nil, fmt.Errorf("lot cost basis: %w", err)
			}
			v.lot.CostBasis = &Amount{Value: basis, Commodity: cost.Commodity}
		}
		out = append(out, v.lot)
	}
	return out, nil
}

func (s *Service) lots(ctx context.Context, p principal.Principal, in BalancesInput, valuer Valuer) ([]Lot, error) {
	db, err := s.repo.db(p)
	if err != nil {
		return nil, err
	}
	filter := bson.M{"user_id": p.UserID, "status": "posted", "date": bson.M{"$lte": in.At}}
	if len(in.Accounts) > 0 {
		filter["postings.account"] = bson.M{"$in": in.Accounts}
	}
	cur, err := db.Collection("journal").Find(ctx, filter, options.Find().SetLimit(10001).SetSort(bson.D{{Key: "seq", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	entries := []Entry{}
	bytes := 0
	accounts := map[string]bool{}
	for cur.Next(ctx) {
		bytes += len(cur.Current)
		if len(entries) >= 10000 || bytes > 16*1024*1024 {
			return nil, fmt.Errorf("cost lot report exceeds 10000 entries or 16 MiB; select accounts")
		}
		var entry Entry
		if err = cur.Decode(&entry); err != nil {
			return nil, err
		}
		posts := []Posting{}
		for _, post := range entry.Postings {
			if _, ok := accounts[post.Account]; !ok {
				a, e := s.repo.Account(ctx, p, post.Account)
				if e != nil {
					return nil, e
				}
				accounts[post.Account] = a.Type == "asset"
			}
			selected := len(in.Accounts) == 0
			for _, id := range in.Accounts {
				if id == post.Account {
					selected = true
					break
				}
			}
			if selected && accounts[post.Account] {
				posts = append(posts, post)
			}
		}
		entry.Postings = posts
		entries = append(entries, entry)
	}
	if err = cur.Err(); err != nil {
		return nil, err
	}
	lots, err := costLots(entries)
	if err != nil {
		return nil, err
	}
	if in.ValueIn == "" {
		return lots, nil
	}
	if valuer == nil {
		return nil, fmt.Errorf("valuation unavailable")
	}
	for i := range lots {
		lot := &lots[i]
		value, e := valuer.Convert(ctx, p, lot.Amount, in.ValueIn, in.At+"T23:59:59Z")
		if e != nil {
			return nil, e
		}
		lot.Value = &value
		if lot.CostBasis != nil {
			basis, e := valuer.Convert(ctx, p, *lot.CostBasis, in.ValueIn, in.At+"T23:59:59Z")
			if e != nil {
				return nil, e
			}
			d, e := DecimalFromRat(new(big.Rat).Sub(value.Value.Rat(), basis.Value.Rat()))
			if e != nil {
				return nil, e
			}
			lot.UnrealizedPL = &Amount{Value: d, Commodity: in.ValueIn}
		}
	}
	return lots, nil
}
