package ledger

import (
	"context"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sync"
)

type accountCacheKey struct{}

// accountCache holds each user's chart of accounts, sorted by id, for one read request.
type accountCache struct {
	mu    sync.Mutex
	users map[string][]Account
}

// WithAccountCache makes account reads under ctx load each user's accounts once.
// Use it only around reads: accounts written under the same ctx are not seen.
func WithAccountCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(accountCacheKey{}).(*accountCache); ok {
		return ctx
	}
	return context.WithValue(ctx, accountCacheKey{}, &accountCache{users: map[string][]Account{}})
}

// CachedAccounts returns every account of the user (closed included) when ctx carries an account cache; ok is false otherwise.
func CachedAccounts(ctx context.Context, db *mongo.Database, p principal.Principal) (all []Account, ok bool, e error) {
	c, ok := ctx.Value(accountCacheKey{}).(*accountCache)
	if !ok {
		return nil, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if all, ok := c.users[p.UserID]; ok {
		return all, true, nil
	}
	cur, e := db.Collection("accounts").Find(ctx, bson.M{"user_id": p.UserID}, options.Find().SetSort(bson.D{{Key: "id", Value: 1}}))
	all = []Account{}
	if e == nil {
		e = cur.All(ctx, &all)
	}
	if e != nil {
		return nil, true, e
	}
	c.users[p.UserID] = all
	return all, true, nil
}
