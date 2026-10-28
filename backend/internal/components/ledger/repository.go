package ledger

import (
	"context"
	"errors"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"regexp"
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
	if in.Cursor > 0 {
		f["seq"] = bson.M{"$lt": in.Cursor}
	}
	for k, v := range map[string]string{"postings.account": in.Account, "kind": in.Kind, "status": in.Status} {
		if v != "" {
			f[k] = v
		}
	}
	if in.Text != "" {
		f["payee"] = bson.M{"$regex": regexp.QuoteMeta(in.Text), "$options": "i"}
	}
	limit := in.Limit
	if limit == 0 {
		limit = 100
	}
	pipeline := bson.A{bson.M{"$match": f}, bson.M{"$lookup": bson.M{"from": "entry_tags", "let": bson.M{"key": bson.M{"$concat": bson.A{p.UserID + ":", "$_id"}}}, "pipeline": bson.A{bson.M{"$match": bson.M{"user_id": p.UserID, "$expr": bson.M{"$eq": bson.A{"$_id", "$$key"}}}}}, "as": "tag_overlay"}}, bson.M{"$set": bson.M{"tags": bson.M{"$ifNull": bson.A{bson.M{"$arrayElemAt": bson.A{"$tag_overlay.tags", 0}}, "$tags"}}}}, bson.M{"$unset": "tag_overlay"}}
	if in.Tag != "" {
		pipeline = append(pipeline, bson.M{"$match": bson.M{"tags": in.Tag}})
	}
	pipeline = append(pipeline, bson.M{"$sort": bson.D{{Key: "seq", Value: -1}}}, bson.M{"$limit": int64(limit + 1)})
	cur, e := db.Collection("journal").Aggregate(ctx, pipeline)
	if e != nil {
		return EntriesPage{}, e
	}
	defer cur.Close(ctx)
	out := EntriesPage{Items: []Entry{}}
	e = cur.All(ctx, &out.Items)
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].Seq
	}
	return out, e
}
func (r *MongoRepository) Accounts(ctx context.Context, p principal.Principal, in AccountsInput) (AccountsPage, error) {
	db, e := r.db(p)
	if e != nil {
		return AccountsPage{}, e
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
	limit := in.Limit
	if limit == 0 {
		limit = 100
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
		"journal":            {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "seq", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "date", Value: 1}, {Key: "postings.account", Value: 1}}}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "tags", Value: 1}}}},
		"events":             {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "seq", Value: 1}}, Options: options.Index().SetUnique(true)}, {Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "target.id", Value: 1}}}},
		"import_rows":        {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "account", Value: 1}, {Key: "dedupe_key", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"prices":             {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "pair", Value: 1}, {Key: "at", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"dashboard_versions": {{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "dashboard_id", Value: 1}, {Key: "version", Value: 1}}, Options: options.Index().SetUnique(true)}},
	} {
		if _, e = db.Collection(name).Indexes().CreateMany(ctx, models); e != nil {
			return e
		}
	}
	return nil
}
