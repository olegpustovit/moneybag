package ledger

import (
	"context"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"regexp"
	"slices"
	"strings"
)

type MongoRepository struct{ DB *mongo.Database }

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func (r *MongoRepository) db(p principal.Principal) (*mongo.Database, error) {
	return mongodb.Shared(r.DB, p)
}
func (r *MongoRepository) Account(ctx context.Context, p principal.Principal, id string) (Account, error) {
	db, e := r.db(p)
	if e != nil {
		return Account{}, e
	}
	if all, ok, e := CachedAccounts(ctx, db, p); ok {
		if e != nil {
			return Account{}, e
		}
		i, found := slices.BinarySearchFunc(all, id, func(a Account, id string) int { return strings.Compare(a.ID, id) })
		if !found {
			return Account{}, principal.ErrNotFound
		}
		return all[i], nil
	}
	var a Account
	e = db.Collection("accounts").FindOne(ctx, bson.M{"user_id": p.UserID, "id": id}).Decode(&a)
	if errors.Is(e, mongo.ErrNoDocuments) {
		e = principal.ErrNotFound
	}
	return a, e
}
func (r *MongoRepository) Entry(ctx context.Context, p principal.Principal, id string) (Entry, error) {
	db, e := r.db(p)
	if e != nil {
		return Entry{}, e
	}
	var a Entry
	e = db.Collection("journal").FindOne(ctx, bson.M{"user_id": p.UserID, "_id": id}).Decode(&a)
	if errors.Is(e, mongo.ErrNoDocuments) {
		e = principal.ErrNotFound
	}
	if e == nil {
		var meta struct {
			Tags []string `bson:"tags"`
		}
		err := db.Collection("entry_tags").FindOne(ctx, bson.M{"_id": p.UserID + ":" + id, "user_id": p.UserID}).Decode(&meta)
		if err == nil {
			a.Tags = meta.Tags
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			return Entry{}, err
		}
	}
	return a, e
}
func (r *MongoRepository) insert(ctx context.Context, p principal.Principal, e *Entry) error {
	db, err := r.db(p)
	if err != nil {
		return err
	}
	e.Seq, err = mongodb.Next(ctx, db, p.UserID+":journal")
	if err != nil {
		return err
	}
	if _, err = db.Collection("journal").InsertOne(ctx, e); err != nil {
		return err
	}
	_, err = db.Collection("balances").UpdateMany(ctx, bson.M{"user_id": p.UserID}, bson.M{"$set": bson.M{"dirty": true}})
	return err
}
func (r *MongoRepository) Entries(ctx context.Context, p principal.Principal, in EntriesInput) (EntriesPage, error) {
	db, e := r.db(p)
	if e != nil {
		return EntriesPage{}, e
	}
	f := bson.M{"user_id": p.UserID}
	dates := bson.M{}
	if in.From != "" {
		dates["$gte"] = in.From
	}
	if in.To != "" {
		dates["$lte"] = in.To
	}
	if len(dates) > 0 {
		f["date"] = dates
	}
	if in.Cursor > 0 && in.Sort == "" {
		f["seq"] = bson.M{"$lt": in.Cursor}
	}
	if in.Cursor > 0 && (in.Sort == "date" || in.Sort == "-date") {
		op := "$gt"
		if in.Sort == "-date" {
			op = "$lt"
		}
		f["$or"] = bson.A{bson.M{"date": bson.M{op: in.CursorDate}}, bson.M{"date": in.CursorDate, "seq": bson.M{op: in.Cursor}}}
	}
	for k, v := range map[string]string{"postings.account": in.Account, "kind": in.Kind, "status": in.Status} {
		if v != "" {
			f[k] = v
		}
	}
	if len(in.Accounts) > 0 {
		f["postings.account"] = bson.M{"$in": in.Accounts}
	}
	if in.Effective {
		reversed, e := r.reversed(ctx, p)
		if e != nil {
			return EntriesPage{}, e
		}
		ids := make([]string, 0, len(reversed))
		for id := range reversed {
			ids = append(ids, id)
		}
		f["_id"] = bson.M{"$nin": ids}
		if in.Kind == "" {
			f["kind"] = bson.M{"$ne": "reversal"}
		}
	}
	if in.Text != "" {
		f["payee"] = bson.M{"$regex": regexp.QuoteMeta(in.Text), "$options": "i"}
	}
	limit := in.Limit
	if limit == 0 {
		limit = 100
	}
	overlay := bson.A{bson.M{"$lookup": bson.M{"from": "entry_tags", "let": bson.M{"key": bson.M{"$concat": bson.A{p.UserID + ":", "$_id"}}}, "pipeline": bson.A{bson.M{"$match": bson.M{"user_id": p.UserID, "$expr": bson.M{"$eq": bson.A{"$_id", "$$key"}}}}}, "as": "tag_overlay"}}, bson.M{"$set": bson.M{"tags": bson.M{"$ifNull": bson.A{bson.M{"$arrayElemAt": bson.A{"$tag_overlay.tags", 0}}, "$tags"}}}}, bson.M{"$unset": "tag_overlay"}}
	sort := bson.D{{Key: "seq", Value: -1}}
	if in.Sort == "date" || in.Sort == "-date" {
		direction := 1
		if in.Sort == "-date" {
			direction = -1
		}
		sort = bson.D{{Key: "date", Value: direction}, {Key: "seq", Value: direction}}
	}
	pipeline := bson.A{bson.M{"$match": f}}
	amountSort := in.Sort == "amount" || in.Sort == "-amount"
	if amountSort {
		postings := any("$postings")
		if in.Account != "" {
			postings = bson.M{"$filter": bson.M{"input": "$postings", "as": "posting", "cond": bson.M{"$eq": bson.A{"$$posting.account", in.Account}}}}
		} else if len(in.Accounts) > 0 {
			postings = bson.M{"$filter": bson.M{"input": "$postings", "as": "posting", "cond": bson.M{"$in": bson.A{"$$posting.account", in.Accounts}}}}
		}
		pipeline = append(pipeline, bson.M{"$set": bson.M{"_sort_posting": bson.M{"$arrayElemAt": bson.A{postings, 0}}}}, bson.M{"$set": bson.M{"_sort_amount": bson.M{"$ifNull": bson.A{"$_sort_posting.amount.v", nil}}}})
		direction, op := 1, "$gt"
		if in.Sort == "-amount" {
			direction, op = -1, "$lt"
		}
		sort = bson.D{{Key: "_sort_amount", Value: direction}, {Key: "seq", Value: direction}}
		if in.Cursor > 0 {
			var amount any
			if in.CursorAmount != "null" {
				value, _ := ParseDecimal(in.CursorAmount)
				amount = value.Decimal128
			}
			pipeline = append(pipeline, bson.M{"$match": bson.M{"$expr": bson.M{"$or": bson.A{bson.M{op: bson.A{"$_sort_amount", amount}}, bson.M{"$and": bson.A{bson.M{"$eq": bson.A{"$_sort_amount", amount}}, bson.M{op: bson.A{"$seq", in.Cursor}}}}}}}})
		}
	}
	page := bson.A{bson.M{"$sort": sort}, bson.M{"$limit": int64(limit + 1)}}
	if in.Tag != "" {
		// The tag filter needs the overlay first; otherwise page first so only one page is joined.
		pipeline = append(append(append(pipeline, overlay...), bson.M{"$match": bson.M{"tags": in.Tag}}), page...)
	} else {
		pipeline = append(append(pipeline, page...), overlay...)
	}
	cur, e := db.Collection("journal").Aggregate(ctx, pipeline, options.Aggregate().SetAllowDiskUse(amountSort))
	if e != nil {
		return EntriesPage{}, e
	}
	defer cur.Close(ctx)
	out := EntriesPage{Items: []Entry{}}
	e = cur.All(ctx, &out.Items)
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].Seq
		if in.Sort == "date" || in.Sort == "-date" {
			out.NextCursorDate = out.Items[limit-1].Date
		}
		if amountSort {
			out.NextCursorAmount = "null"
			for _, posting := range out.Items[limit-1].Postings {
				if (in.Account == "" && len(in.Accounts) == 0) || posting.Account == in.Account || slices.Contains(in.Accounts, posting.Account) {
					out.NextCursorAmount = posting.Amount.Value.String()
					break
				}
			}
		}
	}
	return out, e
}

// reversed returns original entries cancelled by an odd reversal chain.
func (r *MongoRepository) reversed(ctx context.Context, p principal.Principal) (map[string]bool, error) {
	return reqcache.Do(ctx, "reversed|"+p.UserID, func() (map[string]bool, error) {
		if dates, ok := combinedDates(ctx); ok {
			combined, err := r.combinedJournal(ctx, p, dates)
			if err == nil {
				var revs []Entry
				for i, doc := range combined.entries {
					if i%1000 == 0 && ctx.Err() != nil {
						return nil, ctx.Err()
					}
					kind, ok := doc.Lookup("kind").StringValueOK()
					if !ok || kind != "reversal" {
						continue
					}
					var item struct {
						ID    string `bson:"_id"`
						Links []Link `bson:"links"`
					}
					if err := bson.Unmarshal(doc, &item); err != nil {
						return nil, err
					}
					revs = append(revs, Entry{ID: item.ID, Links: item.Links})
				}
				if result, unique := reversalSet(revs); unique {
					return result, nil
				}
			} else if ctx.Err() != nil {
				return nil, err
			}
		}
		db, err := r.db(p)
		if err != nil {
			return nil, err
		}
		cur, err := db.Collection("journal").Find(ctx, bson.M{"user_id": p.UserID, "kind": "reversal", "status": "posted"}, options.Find().SetProjection(bson.M{"links": 1}))
		if err != nil {
			return nil, err
		}
		defer cur.Close(ctx)
		var revs []Entry
		if err := cur.All(ctx, &revs); err != nil {
			return nil, err
		}
		result, _ := reversalSet(revs)
		return result, nil
	})
}

func reversalSet(revs []Entry) (map[string]bool, bool) {
	reversedBy, isReversal := map[string]string{}, map[string]bool{}
	unique := true
	for _, r := range revs {
		isReversal[r.ID] = true
		for _, l := range r.Links {
			if l.Rel == "reverses" {
				if prior, exists := reversedBy[l.ID]; exists && prior != r.ID {
					unique = false
				}
				reversedBy[l.ID] = r.ID
			}
		}
	}
	reversed := map[string]bool{}
	for id := range reversedBy {
		if isReversal[id] {
			continue
		}
		n := 0
		for cur, ok := reversedBy[id]; ok && n <= len(revs); cur, ok = reversedBy[cur] {
			n++
		}
		if n%2 == 1 {
			reversed[id] = true
		}
	}
	return reversed, unique
}

type sharedPostedKey struct{}

// WithSharedPostedStream allows identical journal sources to reuse bounded raw documents for one read request.
func WithSharedPostedStream(ctx context.Context) context.Context {
	return context.WithValue(ctx, sharedPostedKey{}, true)
}

// PostedStream reads posted journal entries without paging or joining tags.
func (r *MongoRepository) PostedStream(ctx context.Context, p principal.Principal, from, to string, fn func(Entry) error) error {
	if dates, ok := combinedDates(ctx); ok {
		combined, err := r.combinedJournal(ctx, p, dates)
		if err == nil {
			return streamPostedRaw(ctx, combined.entries, from, to, fn)
		}
		if ctx.Err() != nil {
			return err
		}
	}
	if shared, _ := ctx.Value(sharedPostedKey{}).(bool); shared {
		db, err := r.db(p)
		if err != nil {
			return err
		}
		key := fmt.Sprintf("posted-raw|%p|%d:%s", db, len(p.UserID), p.UserID)
		raw, err := reqcache.Do(ctx, key, func() ([]bson.Raw, error) {
			cur, err := r.postedCursor(ctx, p, "", "")
			if err != nil {
				return nil, err
			}
			defer cur.Close(ctx)
			var rows []bson.Raw
			size := 0
			for cur.Next(ctx) {
				size += len(cur.Current)
				// shortcut: cache at most 32 MiB or 250,000 entries; fall back to streaming for larger journals.
				if size > 32<<20 || len(rows) >= 250000 {
					return nil, errors.New("shared journal exceeds cache limit")
				}
				rows = append(rows, append(bson.Raw(nil), cur.Current...))
			}
			return rows, cur.Err()
		})
		if err == nil {
			return streamPostedRaw(ctx, raw, from, to, fn)
		}
		if ctx.Err() != nil {
			return err
		}
	}
	cur, err := r.postedCursor(ctx, p, from, to)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var entry Entry
		if err := cur.Decode(&entry); err != nil {
			return err
		}
		if err := fn(entry); err != nil {
			return err
		}
	}
	return cur.Err()
}

func streamPostedRaw(ctx context.Context, raw []bson.Raw, from, to string, fn func(Entry) error) error {
	for _, doc := range raw {
		if err := ctx.Err(); err != nil {
			return err
		}
		if from != "" || to != "" {
			date, ok := doc.Lookup("date").StringValueOK()
			if !ok || from != "" && date < from || to != "" && date > to {
				continue
			}
		}
		var entry Entry
		if err := bson.Unmarshal(doc, &entry); err != nil {
			return err
		}
		if err := fn(entry); err != nil {
			return err
		}
	}
	return nil
}

func (r *MongoRepository) postedCursor(ctx context.Context, p principal.Principal, from, to string) (*mongo.Cursor, error) {
	db, err := r.db(p)
	if err != nil {
		return nil, err
	}
	filter := bson.M{"user_id": p.UserID, "status": "posted"}
	dates := bson.M{}
	if from != "" {
		dates["$gte"] = from
	}
	if to != "" {
		dates["$lte"] = to
	}
	if len(dates) > 0 {
		filter["date"] = dates
	}
	pipeline := bson.A{bson.M{"$match": filter}, bson.M{"$project": bson.M{"user_id": 1, "seq": 1, "date": 1, "kind": 1, "payee": 1, "postings": 1, "tags": 1, "links": 1, "status": 1, "actor": 1, "created_at": 1}}}
	return db.Collection("journal").Aggregate(ctx, pipeline, options.Aggregate().SetBatchSize(250000))
}

func (r *MongoRepository) tagOverlays(ctx context.Context, p principal.Principal) (map[string][]string, error) {
	return reqcache.Do(ctx, "entry-tags|"+p.UserID, func() (map[string][]string, error) {
		db, err := r.db(p)
		if err != nil {
			return nil, err
		}
		cur, err := db.Collection("entry_tags").Find(ctx, bson.M{"user_id": p.UserID}, options.Find().SetProjection(bson.M{"entry_id": 1, "tags": 1}).SetBatchSize(250000))
		if err != nil {
			return nil, err
		}
		defer cur.Close(ctx)
		tags := map[string][]string{}
		for cur.Next(ctx) {
			var item struct {
				ID   string   `bson:"entry_id"`
				Tags []string `bson:"tags"`
			}
			if err := cur.Decode(&item); err != nil {
				return nil, err
			}
			tags[item.ID] = item.Tags
		}
		return tags, cur.Err()
	})
}

func (r *MongoRepository) Accounts(ctx context.Context, p principal.Principal, in AccountsInput) (AccountsPage, error) {
	db, e := r.db(p)
	if e != nil {
		return AccountsPage{}, e
	}
	limit := in.Limit
	if limit == 0 {
		limit = 100
	}
	if all, ok, e := CachedAccounts(ctx, db, p); ok {
		out := AccountsPage{Items: []Account{}}
		for _, a := range all {
			if e != nil || !in.IncludeClosed && a.ClosedAt != nil || in.Type != "" && a.Type != in.Type || in.Pot != "" && a.Pot != in.Pot || a.ID <= in.Cursor {
				continue
			}
			if len(out.Items) == limit {
				out.NextCursor = out.Items[limit-1].ID
				break
			}
			out.Items = append(out.Items, a)
		}
		return out, e
	}
	f := bson.M{"user_id": p.UserID}
	if !in.IncludeClosed {
		f["closed_at"] = bson.M{"$exists": false}
	}
	if in.Type != "" {
		f["type"] = in.Type
	}
	if in.Pot != "" {
		f["pot"] = in.Pot
	}
	if in.Cursor != "" {
		f["id"] = bson.M{"$gt": in.Cursor}
	}
	cur, e := db.Collection("accounts").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "id", Value: 1}}).SetLimit(int64(limit+1)))
	if e != nil {
		return AccountsPage{}, e
	}
	defer cur.Close(ctx)
	out := AccountsPage{Items: []Account{}}
	e = cur.All(ctx, &out.Items)
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].ID
	}
	return out, e
}
func (r *MongoRepository) Init(ctx context.Context, p principal.Principal) error {
	db, e := r.db(p)
	if e != nil {
		return e
	}
	for name, models := range map[string][]mongo.IndexModel{
		"accounts":           {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "id", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"journal":            {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "seq", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "date", Value: 1}, {Key: "seq", Value: 1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "date", Value: 1}, {Key: "postings.account", Value: 1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "postings.account", Value: 1}, {Key: "date", Value: 1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "tags", Value: 1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "kind", Value: 1}, {Key: "status", Value: 1}}}},
		"entry_tags":         {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "entry_id", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"rows":               {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "table_id", Value: 1}, {Key: "id", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"tables":             {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "id", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"views":              {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "id", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"dashboards":         {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "id", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"budgets":            {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "id", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"scenarios":          {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "id", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"discrepancies":      {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "account", Value: 1}, {Key: "status", Value: 1}}}},
		"balance_assertions": {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "account", Value: 1}, {Key: "date", Value: -1}}}},
		"import_batches":     {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}}},
		"events":             {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "seq", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "target.id", Value: 1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "tool", Value: 1}, {Key: "seq", Value: -1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "actor.id", Value: 1}, {Key: "seq", Value: -1}}}},
		"import_rows":        {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "account", Value: 1}, {Key: "dedupe_key", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "batch_id", Value: 1}, {Key: "seq", Value: 1}}}},
		"prices":             {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "pair", Value: 1}, {Key: "at", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "at", Value: 1}}}},
		"rate_sync":          {{Keys: bson.D{{Key: "user_id", Value: 1}}}},
		"dashboard_versions": {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "dashboard_id", Value: 1}, {Key: "version", Value: 1}}, Options: options.Index().SetUnique(true)}},
	} {
		if _, e = db.Collection(name).Indexes().CreateMany(ctx, models); e != nil {
			return e
		}
	}
	return nil
}
