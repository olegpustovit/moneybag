package ledger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type combinedJournalKey struct{}
type combinedBudgetKey struct{}
type combinedHeadKey struct{}
type combinedBudgetRange struct{ from, to string }

// JournalMonthRow is a grouped expense amount for a date, account, currency and price.
type JournalMonthRow struct {
	ID struct {
		Date      string  `bson:"date"`
		Account   string  `bson:"account"`
		Commodity string  `bson:"commodity"`
		Price     *Amount `bson:"price"`
	} `bson:"_id"`
	Amount Decimal `bson:"amount"`
}

type combinedJournal struct {
	entries  []bson.Raw
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
func (r *MongoRepository) CombinedMonthRows(ctx context.Context, p principal.Principal, from, to string) ([]JournalMonthRow, bool, error) {
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

func (r *MongoRepository) combinedJournal(ctx context.Context, p principal.Principal, dates []string) (combinedJournal, error) {
	db, err := r.db(p)
	if err != nil {
		return combinedJournal{}, err
	}
	window, _ := ctx.Value(combinedBudgetKey{}).(combinedBudgetRange)
	includeHead, _ := ctx.Value(combinedHeadKey{}).(bool)
	key := fmt.Sprintf("combined-journal|%p|%d:%s|%s|%s|%s|%t", db, len(p.UserID), p.UserID, strings.Join(dates, ","), window.from, window.to, includeHead)
	return reqcache.Do(ctx, key, func() (combinedJournal, error) {
		projection := bson.M{"user_id": 1, "seq": 1, "date": 1, "kind": 1, "payee": 1, "postings": 1, "tags": 1, "links": 1, "status": 1, "actor": 1, "created_at": 1}
		entryStages := bson.A{}
		if includeHead {
			entryStages = append(entryStages, bson.M{"$match": bson.M{"status": "posted"}})
		}
		entryStages = append(entryStages, bson.M{"$project": projection}, bson.M{"$limit": 250001}, bson.M{"$replaceWith": bson.M{"kind": "entry", "data": "$$ROOT"}})
		facets := bson.D{{Key: "entries", Value: entryStages}}
		arrays := bson.A{"$entries"}
		if includeHead {
			arrays = append(arrays, "$head")
			// Match Entries' two-row decode and tag overlay, including drafts and malformed records.
			facets = append(facets, bson.E{Key: "head", Value: bson.A{
				bson.M{"$sort": bson.M{"seq": -1}}, bson.M{"$limit": 2},
				bson.M{"$lookup": bson.M{"from": "entry_tags", "let": bson.M{"key": bson.M{"$concat": bson.A{p.UserID + ":", "$_id"}}}, "pipeline": bson.A{bson.M{"$match": bson.M{"user_id": p.UserID, "$expr": bson.M{"$eq": bson.A{"$_id", "$$key"}}}}}, "as": "tag_overlay"}},
				bson.M{"$set": bson.M{"tags": bson.M{"$ifNull": bson.A{bson.M{"$arrayElemAt": bson.A{"$tag_overlay.tags", 0}}, "$tags"}}}}, bson.M{"$unset": "tag_overlay"},
				bson.M{"$replaceWith": bson.M{"kind": "head", "data": "$$ROOT"}},
			}})
		}
		for i, date := range dates {
			name := "d" + strconv.Itoa(i)
			arrays = append(arrays, "$"+name)
			facets = append(facets, bson.E{Key: name, Value: bson.A{
				bson.M{"$match": bson.M{"status": "posted", "date": bson.M{"$lte": date}}}, bson.M{"$unwind": "$postings"},
				bson.M{"$group": bson.M{"_id": bson.M{"account": "$postings.account", "commodity": "$postings.amount.c"}, "amount": bson.M{"$sum": "$postings.amount.v"}}},
				bson.M{"$sort": bson.M{"_id.account": 1, "_id.commodity": 1}}, bson.M{"$limit": 5001},
				bson.M{"$replaceWith": bson.M{"kind": "balance", "index": i, "data": "$$ROOT"}},
			}})
		}
		if window.from != "" {
			arrays = append(arrays, "$months")
			facets = append(facets, bson.E{Key: "months", Value: bson.A{
				bson.M{"$match": bson.M{"status": "posted", "date": bson.M{"$gte": window.from, "$lte": window.to}}},
				bson.M{"$unwind": "$postings"}, bson.M{"$match": bson.M{"postings.account": bson.M{"$regex": "^expenses:(?!estimated)"}}},
				bson.M{"$group": bson.M{"_id": bson.M{"date": "$date", "account": "$postings.account", "commodity": "$postings.amount.c", "price": "$postings.price"}, "amount": bson.M{"$sum": "$postings.amount.v"}}},
				bson.M{"$limit": 5001}, bson.M{"$replaceWith": bson.M{"kind": "month", "data": "$$ROOT"}},
			}})
		}
		match := bson.M{"user_id": p.UserID}
		if !includeHead {
			match["status"] = "posted"
		}
		pipeline := bson.A{bson.M{"$match": match}, bson.M{"$facet": facets}, bson.M{"$project": bson.M{"_id": 0, "events": bson.M{"$concatArrays": arrays}}}, bson.M{"$unwind": "$events"}, bson.M{"$replaceWith": "$events"}}
		cur, err := db.Collection("journal").Aggregate(ctx, pipeline, options.Aggregate().SetBatchSize(250000))
		if err != nil {
			return combinedJournal{}, err
		}
		defer cur.Close(ctx)
		out := combinedJournal{balances: make([][]rawBalance, len(dates))}
		size := 0
		for cur.Next(ctx) {
			var frame struct {
				Kind  string   `bson:"kind"`
				Index int      `bson:"index"`
				Data  bson.Raw `bson:"data"`
			}
			if err := cur.Decode(&frame); err != nil {
				return combinedJournal{}, err
			}
			switch frame.Kind {
			case "head":
				var head Entry
				if err := bson.Unmarshal(frame.Data, &head); err != nil {
					return combinedJournal{}, err
				}
				if out.head == nil {
					out.head = &head
				}
			case "entry":
				size += len(frame.Data)
				// shortcut: fall back to separate streaming reads above 32 MiB or 250,000 entries.
				if size > 32<<20 || len(out.entries) >= 250000 {
					return combinedJournal{}, errors.New("combined journal exceeds cache limit")
				}
				out.entries = append(out.entries, append(bson.Raw(nil), frame.Data...))
			case "month":
				var row JournalMonthRow
				if err := bson.Unmarshal(frame.Data, &row); err != nil {
					return combinedJournal{}, err
				}
				out.months = append(out.months, row)
			case "balance":
				if frame.Index < 0 || frame.Index >= len(dates) {
					return combinedJournal{}, errors.New("invalid balance series index")
				}
				var row rawBalance
				if err := bson.Unmarshal(frame.Data, &row); err != nil {
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
