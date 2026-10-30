package ledger

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// BalanceSeries computes the same type-grouped, valued balances for several dates in one journal scan.
func (s *Service) BalanceSeries(ctx context.Context, p principal.Principal, dates []string, valuer Valuer) ([]BalancesResult, error) {
	if err := p.Require("ledger:read"); err != nil {
		return nil, err
	}
	if len(dates) == 0 || len(dates) > 12 {
		return nil, fmt.Errorf("balance series needs 1..12 dates")
	}
	for i, date := range dates {
		if _, err := time.Parse("2006-01-02", date); err != nil || i > 0 && date <= dates[i-1] {
			return nil, fmt.Errorf("balance series dates must be increasing YYYY-MM-DD values")
		}
	}
	perDate, err := s.balanceSeriesRows(ctx, p, dates)
	if err != nil {
		return nil, err
	}
	accounts, err := s.balanceAccounts(ctx, p, "type")
	if err != nil {
		return nil, err
	}
	out := make([]BalancesResult, len(dates))
	for i, date := range dates {
		if len(perDate[i]) > 5000 {
			return nil, fmt.Errorf("balance result exceeds 5000 rows")
		}
		out[i], err = s.balanceResult(ctx, p, BalancesInput{At: date, GroupBy: "type", ValueIn: "USD"}, valuer, perDate[i], accounts)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

type balanceSeriesKey struct{}

// WithBalanceSeries shares the last date's raw balances with an adjacent balance read.
func WithBalanceSeries(ctx context.Context, dates []string) context.Context {
	if len(dates) == 0 || len(dates) > 12 {
		return ctx
	}
	for i, date := range dates {
		if _, err := time.Parse("2006-01-02", date); err != nil || i > 0 && date <= dates[i-1] {
			return ctx
		}
	}
	return context.WithValue(ctx, balanceSeriesKey{}, append([]string(nil), dates...))
}

func (s *Service) balanceSeriesRows(ctx context.Context, p principal.Principal, dates []string) ([][]rawBalance, error) {
	db, err := s.repo.db(p)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("balance-series|%p|%d:%s|%s", db, len(p.UserID), p.UserID, strings.Join(dates, ","))
	return reqcache.Do(ctx, key, func() ([][]rawBalance, error) {
		// shortcut: facets are capped at 100 MiB; stream per-date aggregates if grouped output exceeds that limit.
		facets := bson.D{}
		for i, date := range dates {
			facets = append(facets, bson.E{Key: strconv.Itoa(i), Value: bson.A{
				bson.M{"$match": bson.M{"date": bson.M{"$lte": date}}},
				bson.M{"$group": bson.M{"_id": bson.M{"account": "$postings.account", "commodity": "$postings.amount.c"}, "amount": bson.M{"$sum": "$postings.amount.v"}}},
				bson.M{"$sort": bson.M{"_id.account": 1, "_id.commodity": 1}}, bson.M{"$limit": 5001},
			}})
		}
		pipeline := bson.A{
			bson.M{"$match": bson.M{"user_id": p.UserID, "status": "posted", "date": bson.M{"$lte": dates[len(dates)-1]}}},
			bson.M{"$unwind": "$postings"}, bson.M{"$project": bson.M{"date": 1, "postings.account": 1, "postings.amount": 1}},
			bson.M{"$facet": facets}, bson.M{"$project": bson.M{"facets": bson.M{"$objectToArray": "$$ROOT"}}},
			bson.M{"$unwind": "$facets"}, bson.M{"$unwind": "$facets.v"},
			bson.M{"$project": bson.M{"index": bson.M{"$toInt": "$facets.k"}, "_id": "$facets.v._id", "amount": "$facets.v.amount"}},
		}
		cur, err := db.Collection("journal").Aggregate(ctx, pipeline, options.Aggregate().SetBatchSize(5001))
		if err != nil {
			return nil, err
		}
		defer cur.Close(ctx)
		var rows []struct {
			Index int `bson:"index"`
			ID    struct {
				Account   string `bson:"account"`
				Commodity string `bson:"commodity"`
			} `bson:"_id"`
			Amount Decimal `bson:"amount"`
		}
		if err := cur.All(ctx, &rows); err != nil {
			return nil, err
		}
		perDate := make([][]rawBalance, len(dates))
		for _, row := range rows {
			if row.Index < 0 || row.Index >= len(dates) {
				return nil, fmt.Errorf("invalid balance series index")
			}
			perDate[row.Index] = append(perDate[row.Index], rawBalance{ID: row.ID, Amount: row.Amount})
		}
		return perDate, nil
	})
}
