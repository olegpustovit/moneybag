package principal

import (
	"context"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"slices"
)

var ErrForbidden = errors.New("permission denied")
var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("version conflict; reload and retry")

type Actor struct {
	Kind string `json:"kind" bson:"kind"`
	ID   string `json:"id" bson:"id"`
}
type Principal struct {
	UserID string   `json:"user_id"`
	Scopes []string `json:"scopes"`
	Actor  Actor    `json:"actor"`
	Admin  bool     `json:"is_admin"`
	// GrantID names the OAuth connection behind an agent principal, so it can ask for more on its own behalf.
	GrantID string `json:"-"`
}

var Scopes = []string{"ledger:read", "ledger:write", "import:write", "model:write", "dash:write", "dash:share"}

// ScopeAdminUsers is left out of Scopes on purpose: no consent screen offers it, and an agent gets it only through the owner's confirmation page.
const ScopeAdminUsers = "admin:users"

func (p Principal) Require(scope string) error {
	if p.Valid() != nil {
		return ErrForbidden
	}
	if !slices.Contains(p.Scopes, scope) {
		return fmt.Errorf("%w: this connection lacks the %s scope; reconnect and grant it", ErrForbidden, scope)
	}
	return nil
}
func (p Principal) Valid() error {
	if _, err := ulid.ParseStrict(p.UserID); err != nil {
		return ErrForbidden
	}
	return nil
}

type key struct{}

func With(ctx context.Context, p Principal) context.Context { return context.WithValue(ctx, key{}, p) }
func From(ctx context.Context) (Principal, error) {
	p, ok := ctx.Value(key{}).(Principal)
	if !ok {
		return p, ErrForbidden
	}
	return p, nil
}
