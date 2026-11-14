package rates

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
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
		const loadSQL = `SELECT ` + quoteColumns + ` FROM prices WHERE user_id=@user AND pair=@pair AND archived=false ORDER BY at`
		rows, e := s.repo.db.Query(ctx, loadSQL, pgx.NamedArgs{"user": p.UserID, "pair": pair})
		var all []Quote
		if e == nil {
			all, e = pgx.CollectRows(rows, pgx.RowToStructByName[Quote])
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
		return Quote{}, pgx.ErrNoRows
	}
	return list[i-1], nil
}
