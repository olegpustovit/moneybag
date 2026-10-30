package rates

import (
	"context"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"sort"
	"sync"
)

type cacheKey struct{}

// quoteCache holds requested live quote pairs, sorted by time, for one read request.
type quoteCache struct {
	mu    sync.Mutex
	users map[string]map[string][]Quote
}

// WithQuoteCache loads each requested user/pair once and answers "at or before" in memory.
// Use it only around reads: quotes written under the same ctx are not seen.
func WithQuoteCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(cacheKey{}).(*quoteCache); ok {
		return ctx
	}
	return context.WithValue(ctx, cacheKey{}, &quoteCache{users: map[string]map[string][]Quote{}})
}

func (s *Service) cachedQuote(ctx context.Context, c *quoteCache, p principal.Principal, pair, at string) (Quote, error) {
	c.mu.Lock()
	byPair := c.users[p.UserID]
	if byPair == nil {
		byPair = map[string][]Quote{}
		c.users[p.UserID] = byPair
	}
	list, ok := byPair[pair]
	if !ok {
		cur, e := s.repo.db.Collection("prices").Find(ctx, bson.M{"user_id": p.UserID, "pair": pair, "archived": bson.M{"$ne": true}}, options.Find().SetSort(bson.D{{Key: "at", Value: 1}}))
		var all []Quote
		if e == nil {
			e = cur.All(ctx, &all)
			_ = cur.Close(ctx)
		}
		if e != nil {
			c.mu.Unlock()
			return Quote{}, e
		}
		list = all
		byPair[pair] = list
	}
	c.mu.Unlock()
	i := sort.Search(len(list), func(i int) bool { return list[i].At > at })
	if i == 0 {
		return Quote{}, mongo.ErrNoDocuments
	}
	return list[i-1], nil
}
