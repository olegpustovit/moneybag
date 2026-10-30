package ledger

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type combinedJournalKey struct{}
type combinedJournal struct {
	entries  []bson.Raw
	balances [][]rawBalance
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

func (r *MongoRepository) combinedJournal(ctx context.Context, p principal.Principal, dates []string) (combinedJournal, error) {
	db, err := r.db(p)
	if err != nil {
		return combinedJournal{}, err
	}
	key := fmt.Sprintf("combined-journal|%p|%d:%s|%s", db, len(p.UserID), p.UserID, strings.Join(dates, ","))
	return reqcache.Do(ctx, key, func() (combinedJournal, error) {
		projection := bson.M{"user_id": 1, "seq": 1, "date": 1, "kind": 1, "payee": 1, "postings": 1, "tags": 1, "links": 1, "status": 1, "actor": 1, "created_at": 1}
		facets := bson.D{{Key: "entries", Value: bson.A{bson.M{"$project": projection}, bson.M{"$limit": 250001}, bson.M{"$replaceWith": bson.M{"kind": "entry", "data": "$$ROOT"}}}}}
		arrays := bson.A{"$entries"}
		for i, date := range dates {
			name := "d" + strconv.Itoa(i)
			arrays = append(arrays, "$"+name)
			facets = append(facets, bson.E{Key: name, Value: bson.A{
				bson.M{"$match": bson.M{"date": bson.M{"$lte": date}}}, bson.M{"$unwind": "$postings"},
				bson.M{"$group": bson.M{"_id": bson.M{"account": "$postings.account", "commodity": "$postings.amount.c"}, "amount": bson.M{"$sum": "$postings.amount.v"}}},
				bson.M{"$sort": bson.M{"_id.account": 1, "_id.commodity": 1}}, bson.M{"$limit": 5001},
				bson.M{"$replaceWith": bson.M{"kind": "balance", "index": i, "data": "$$ROOT"}},
			}})
		}
		pipeline := bson.A{bson.M{"$match": bson.M{"user_id": p.UserID, "status": "posted"}}, bson.M{"$facet": facets}, bson.M{"$project": bson.M{"_id": 0, "events": bson.M{"$concatArrays": arrays}}}, bson.M{"$unwind": "$events"}, bson.M{"$replaceWith": "$events"}}
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
			case "entry":
				size += len(frame.Data)
				// shortcut: fall back to separate streaming reads above 32 MiB or 250,000 entries.
				if size > 32<<20 || len(out.entries) >= 250000 {
					return combinedJournal{}, errors.New("combined journal exceeds cache limit")
				}
				out.entries = append(out.entries, append(bson.Raw(nil), frame.Data...))
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
