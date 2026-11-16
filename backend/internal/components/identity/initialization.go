package identity

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func (s *Service) IdentityNeedsInitialization(ctx context.Context, userID string) (bool, error) {
	const sql = `SELECT EXISTS(SELECT 1 FROM user_identities WHERE user_id=@user AND needs_initialization)`
	var pending bool
	err := s.repo.db.QueryRow(ctx, sql, pgx.NamedArgs{"user": userID}).Scan(&pending)
	return pending, err
}
func (s *Service) IdentityInitialized(ctx context.Context, userID string) error {
	const sql = `UPDATE user_identities SET needs_initialization=false WHERE user_id=@user`
	tag, err := s.repo.db.Exec(ctx, sql, pgx.NamedArgs{"user": userID})
	if err == nil && tag.RowsAffected() != 1 {
		return principal.ErrNotFound
	}
	return err
}
