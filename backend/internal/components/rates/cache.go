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

// quoteCache holds every live quote per user, sorted by time, for one read request.
type quoteCache struct {
	mu    sync.Mutex
	users map[string]map[string][]Quote
}

// WithQuoteCache makes rate lookups under ctx load each user's quotes once and answer "at or before" in memory.
// Use it only around reads: quotes written under the same ctx are not seen.
func WithQuoteCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(cacheKey{}).(*quoteCache); ok {
		return ctx
	}
	return context.WithValue(ctx, cacheKey{}, &quoteCache{users: map[string]map[string][]Quote{}})
}

func (s *Service) cachedQuote(ctx context.Context, c *quoteCache, p principal.Principal, pair, at string) (Quote, error) {
	c.mu.Lock()
	byPair, ok := c.users[p.UserID]
	if !ok {
		cur, e := s.repo.db.Collection("prices").Find(ctx, bson.M{"user_id": p.UserID, "archived": bson.M{"$ne": true}}, options.Find().SetSort(bson.D{{Key: "at", Value: 1}}))
		var all []Quote
		if e == nil {
			e = cur.All(ctx, &all)
		}
		if e != nil {
			c.mu.Unlock()
			return Quote{}, e
		}
		byPair = map[string][]Quote{}
		for _, q := range all {
			byPair[q.Pair] = append(byPair[q.Pair], q)
		}
		c.users[p.UserID] = byPair
	}
	c.mu.Unlock()
	list := byPair[pair]
	i := sort.Search(len(list), func(i int) bool { return list[i].At > at })
	if i == 0 {
		return Quote{}, mongo.ErrNoDocuments
	}
	return list[i-1], nil
}
