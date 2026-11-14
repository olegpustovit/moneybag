package ledger

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
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
func CachedAccounts(ctx context.Context, db *pgxpool.Pool, p principal.Principal) (all []Account, ok bool, e error) {
	c, ok := ctx.Value(accountCacheKey{}).(*accountCache)
	if !ok {
		return nil, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if all, ok := c.users[p.UserID]; ok {
		return all, true, nil
	}
	rows, e := db.Query(ctx, `SELECT * FROM accounts WHERE user_id=@user ORDER BY id`, pgx.NamedArgs{"user": p.UserID})
	all = []Account{}
	if e == nil {
		all, e = pgx.CollectRows(rows, pgx.RowToStructByName[Account])
	}
	if e != nil {
		return nil, true, e
	}
	c.users[p.UserID] = all
	return all, true, nil
}
