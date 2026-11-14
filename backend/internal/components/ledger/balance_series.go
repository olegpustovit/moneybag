package ledger

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
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
	if combinedDates, ok := ctx.Value(combinedJournalKey{}).([]string); ok && slices.Equal(combinedDates, dates) {
		combined, err := s.repo.combinedJournal(ctx, p, dates)
		if err == nil {
			return combined.balances, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
	}
	db, err := s.repo.db(p)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("balance-series|%p|%d:%s|%s", db, len(p.UserID), p.UserID, strings.Join(dates, ","))
	return reqcache.Do(ctx, key, func() ([][]rawBalance, error) {
		const seriesSQL = `WITH grouped AS (
 SELECT (d.i-1)::int AS index,p->>'account' AS account,p->'amount'->>'c' AS commodity,SUM(COALESCE(p->'amount'->>'v','0')::numeric) AS amount
 FROM journal j CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(j.postings,'null'::jsonb),'[]'::jsonb)) p
 JOIN unnest(@dates::text[]) WITH ORDINALITY d(date,i) ON j.date<=d.date
 WHERE j.user_id=@user AND j.status='posted' AND j.date<=@last GROUP BY d.i,p->>'account',p->'amount'->>'c'
 ), ranked AS (SELECT *,row_number() OVER (PARTITION BY index ORDER BY account,commodity) AS n FROM grouped)
 SELECT index,account,commodity,amount FROM ranked WHERE n<=5001 ORDER BY index,account,commodity`
		cur, err := db.Query(ctx, seriesSQL, pgx.NamedArgs{"dates": dates, "last": dates[len(dates)-1], "user": p.UserID})
		if err != nil {
			return nil, err
		}
		defer cur.Close()
		var rows []struct {
			Index  int
			ID     struct{ Account, Commodity string }
			Amount Decimal
		}
		for cur.Next() {
			var row struct {
				Index  int
				ID     struct{ Account, Commodity string }
				Amount Decimal
			}
			if err := cur.Scan(&row.Index, &row.ID.Account, &row.ID.Commodity, &row.Amount); err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
		if err := cur.Err(); err != nil {
			return nil, err
		}
		perDate := make([][]rawBalance, len(dates))
		for _, row := range rows {
			if row.Index < 0 || row.Index >= len(dates) {
				return nil, fmt.Errorf("invalid balance series index")
			}
			value := rawBalance{Amount: row.Amount}
			value.ID.Account, value.ID.Commodity = row.ID.Account, row.ID.Commodity
			perDate[row.Index] = append(perDate[row.Index], value)
		}
		return perDate, nil
	})
}
