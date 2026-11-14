package ledger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
)

type combinedJournalKey struct{}
type combinedBudgetKey struct{}
type combinedHeadKey struct{}
type combinedBudgetRange struct{ from, to string }

// JournalMonthRow is a grouped expense amount for a date, account, currency and price.
type JournalMonthRow struct {
	ID struct {
		Date      string  `json:"date"`
		Account   string  `json:"account"`
		Commodity string  `json:"commodity"`
		Price     *Amount `json:"price"`
	} `json:"_id"`
	Amount Decimal `json:"amount"`
}

type combinedJournal struct {
	entries  []json.RawMessage
	balances [][]rawBalance
	months   []JournalMonthRow
	head     *Entry
}

// WithCombinedJournal lets journal and balance sources share a bounded database read.
func WithCombinedJournal(ctx context.Context, dates []string) context.Context {
	if len(dates) == 0 || len(dates) > 12 {
		return ctx
	}
	for i, date := range dates {
		if _, err := time.Parse("2006-01-02", date); err != nil || i > 0 && date <= dates[i-1] {
			return ctx
		}
	}
	return context.WithValue(ctx, combinedJournalKey{}, append([]string(nil), dates...))
}

// WithCombinedBudget lets the same journal read return grouped expense rows for a bounded window.
func WithCombinedBudget(ctx context.Context, from, to string) context.Context {
	if _, err := time.Parse("2006-01-02", from); err != nil {
		return ctx
	}
	if _, err := time.Parse("2006-01-02", to); err != nil || from > to {
		return ctx
	}
	return context.WithValue(ctx, combinedBudgetKey{}, combinedBudgetRange{from, to})
}

// WithCombinedHead includes the latest journal sequence, including drafts, in a combined read.
func WithCombinedHead(ctx context.Context) context.Context {
	return context.WithValue(ctx, combinedHeadKey{}, true)
}

func combinedDates(ctx context.Context) ([]string, bool) {
	dates, balance := ctx.Value(combinedJournalKey{}).([]string)
	_, budget := ctx.Value(combinedBudgetKey{}).(combinedBudgetRange)
	return dates, balance || budget
}

// CombinedMonthRows returns reusable grouped rows when this request has a matching shared journal window.
func (r *PostgresRepository) CombinedMonthRows(ctx context.Context, p principal.Principal, from, to string) ([]JournalMonthRow, bool, error) {
	if err := p.Require("ledger:read"); err != nil {
		return nil, false, err
	}
	window, ok := ctx.Value(combinedBudgetKey{}).(combinedBudgetRange)
	if !ok || from != window.from || to != window.to {
		return nil, false, nil
	}
	dates, _ := combinedDates(ctx)
	combined, err := r.combinedJournal(ctx, p, dates)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	rows := slices.Clone(combined.months)
	for i := range rows {
		if rows[i].ID.Price != nil {
			price := *rows[i].ID.Price
			rows[i].ID.Price = &price
		}
	}
	return rows, true, nil
}

func (r *PostgresRepository) combinedJournal(ctx context.Context, p principal.Principal, dates []string) (combinedJournal, error) {
	db, err := r.db(p)
	if err != nil {
		return combinedJournal{}, err
	}
	window, _ := ctx.Value(combinedBudgetKey{}).(combinedBudgetRange)
	includeHead, _ := ctx.Value(combinedHeadKey{}).(bool)
	key := fmt.Sprintf("combined-journal|%p|%d:%s|%s|%s|%s|%t", db, len(p.UserID), p.UserID, strings.Join(dates, ","), window.from, window.to, includeHead)
	return reqcache.Do(ctx, key, func() (combinedJournal, error) {
		const combinedSQL = `WITH source AS MATERIALIZED (
 SELECT * FROM journal WHERE user_id=@user AND (@head OR status='posted')
 ), entries AS (
 SELECT 'entry'::text AS kind,0 AS index,to_jsonb(j) AS data FROM source j WHERE status='posted' ORDER BY seq LIMIT 250001
 ), heads AS (
 SELECT 'head'::text AS kind,0 AS index,to_jsonb(j)||jsonb_build_object('tags',COALESCE(t.tags,j.tags)) AS data
 FROM (SELECT * FROM source WHERE @head ORDER BY seq DESC LIMIT 2) j ` + entryTagJoin + `
 ), balances AS (
 SELECT (d.i-1)::int AS index,p->>'account' AS account,p->'amount'->>'c' AS commodity,SUM(COALESCE(p->'amount'->>'v','0')::numeric) AS amount
 FROM source j CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(j.postings,'null'::jsonb),'[]'::jsonb)) p
 JOIN unnest(@dates::text[]) WITH ORDINALITY d(date,i) ON j.date<=d.date WHERE j.status='posted'
 GROUP BY d.i,p->>'account',p->'amount'->>'c'
 ), ranked_balances AS (
 SELECT *,row_number() OVER (PARTITION BY index ORDER BY account,commodity) AS n FROM balances
 ), month_groups AS (
 SELECT j.date,p->>'account' AS account,p->'amount'->>'c' AS commodity,p->'price'->>'c' AS price_commodity,
 (p->'price'->>'v')::numeric AS price_value,SUM(COALESCE(p->'amount'->>'v','0')::numeric) AS amount
 FROM source j CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(j.postings,'null'::jsonb),'[]'::jsonb)) p
 WHERE @from<>'' AND j.status='posted' AND j.date>=@from AND j.date<=@to AND starts_with(p->>'account','expenses:') AND NOT starts_with(p->>'account','expenses:estimated')
 GROUP BY j.date,p->>'account',p->'amount'->>'c',p->'price'->>'c',(p->'price'->>'v')::numeric LIMIT 5001
 ) SELECT kind,index,data FROM (SELECT kind,index,data FROM entries UNION ALL SELECT kind,index,data FROM heads
 UNION ALL SELECT 'balance',index,jsonb_build_object('_id',jsonb_build_object('account',account,'commodity',commodity),'amount',amount::text) FROM ranked_balances WHERE n<=5001
 UNION ALL SELECT 'month',0,jsonb_build_object('_id',jsonb_build_object('date',date,'account',account,'commodity',commodity,'price',CASE WHEN price_value IS NULL AND price_commodity IS NULL THEN NULL ELSE jsonb_build_object('v',price_value::text,'c',price_commodity) END),'amount',amount::text) FROM month_groups) frames ORDER BY kind,index,CASE WHEN kind='entry' THEN (data->>'seq')::bigint END ASC,CASE WHEN kind='head' THEN (data->>'seq')::bigint END DESC`
		cur, err := db.Query(ctx, combinedSQL, pgx.NamedArgs{"user": p.UserID, "head": includeHead, "dates": dates, "from": window.from, "to": window.to})
		if err != nil {
			return combinedJournal{}, err
		}
		defer cur.Close()
		out := combinedJournal{balances: make([][]rawBalance, len(dates))}
		size := 0
		for cur.Next() {
			var frame struct {
				Kind  string
				Index int
				Data  json.RawMessage
			}
			if err := cur.Scan(&frame.Kind, &frame.Index, &frame.Data); err != nil {
				return combinedJournal{}, err
			}
			switch frame.Kind {
			case "head":
				var head Entry
				if err := json.Unmarshal(frame.Data, &head); err != nil {
					return combinedJournal{}, err
				}
				head.UserID = p.UserID
				if out.head == nil {
					out.head = &head
				}
			case "entry":
				size += len(frame.Data)
				// shortcut: fall back to separate streaming reads above 32 MiB or 250,000 entries.
				if size > 32<<20 || len(out.entries) >= 250000 {
					return combinedJournal{}, errors.New("combined journal exceeds cache limit")
				}
				out.entries = append(out.entries, append(json.RawMessage(nil), frame.Data...))
			case "month":
				var row JournalMonthRow
				if err := json.Unmarshal(frame.Data, &row); err != nil {
					return combinedJournal{}, err
				}
				out.months = append(out.months, row)
			case "balance":
				if frame.Index < 0 || frame.Index >= len(dates) {
					return combinedJournal{}, errors.New("invalid balance series index")
				}
				var row rawBalance
				if err := json.Unmarshal(frame.Data, &row); err != nil {
					return combinedJournal{}, err
				}
				out.balances[frame.Index] = append(out.balances[frame.Index], row)
			default:
				return combinedJournal{}, errors.New("invalid combined journal result")
			}
		}
		if err := cur.Err(); err != nil {
			return combinedJournal{}, err
		}
		return out, nil
	})
}
