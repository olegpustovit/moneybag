package identity

import (
	"context"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

// A cookie rename cannot exchange ID and legacy sessions: their stored hashes
// have different purposes. Existing rows remain valid for flag-off rollback.
func identitySessionHash(raw string) string {
	return Hash("moneybag Identity browser session v1\x00" + raw)
}

func (s *Service) StartIdentitySession(ctx context.Context, user User) (string, Session, error) {
	current, err := s.User(ctx, user.ID)
	if err != nil {
		return "", Session{}, err
	}
	if current.Disabled {
		return "", Session{}, principal.ErrForbidden
	}
	return s.startSession(ctx, current, true)
}
func (s *Service) IdentitySession(ctx context.Context, raw string) (principal.Principal, Session, error) {
	return s.session(ctx, identitySessionHash(raw))
}
func (s *Service) LogoutIdentity(ctx context.Context, raw string) error {
	return s.logout(ctx, identitySessionHash(raw))
}
