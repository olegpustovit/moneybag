package mongodb

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"strings"
)

func Connect(ctx context.Context, uri string) (*mongo.Client, error) {
	c, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("connect MongoDB: %w", err)
	}
	if err = c.Ping(ctx, nil); err != nil {
		_ = c.Disconnect(ctx)
		return nil, fmt.Errorf("ping MongoDB: %w", err)
	}
	return c, nil
}
func Shared(db *mongo.Database, p principal.Principal) (*mongo.Database, error) {
	if err := p.Valid(); err != nil {
		return nil, err
	}
	return db, nil
}
func Next(ctx context.Context, db *mongo.Database, name string) (int64, error) {
	owner, key, ok := strings.Cut(name, ":")
	if !ok || owner == "" || key == "" {
		return 0, fmt.Errorf("counter requires owner and name")
	}
	var out struct {
		N int64 `bson:"n"`
	}
	err := db.Collection("counters").FindOneAndUpdate(ctx, bson.M{"_id": name}, bson.M{"$inc": bson.M{"n": 1}, "$setOnInsert": bson.M{"user_id": owner, "name": key}}, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&out)
	return out.N, err
}

type workflowKey struct{}

var workflowLock = make(chan struct{}, 1)

// shortcut: standalone writes are serialized in one API process; coordinate writers before adding API replicas.
func Workflow(ctx context.Context, fn func(context.Context) error) error {
	if ctx.Value(workflowKey{}) != nil {
		return fn(ctx)
	}
	select {
	case workflowLock <- struct{}{}:
		defer func() { <-workflowLock }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return fn(context.WithValue(ctx, workflowKey{}, true))
}
