package ledger

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

// IsEffective shares the posted-stream reversal-chain semantics with ledger-aware tables.
func (s *Service) IsEffective(ctx context.Context, p principal.Principal, entry Entry) (bool, error) {
	if err := p.Require("ledger:read"); err != nil {
		return false, err
	}
	if entry.UserID != p.UserID {
		return false, principal.ErrForbidden
	}
	if entry.Status != "posted" || entry.Kind == "reversal" {
		return false, nil
	}
	reversed, err := s.repo.reversed(ctx, p)
	return !reversed[entry.ID] && err == nil, err
}

func (s *Service) Reversal(ctx context.Context, p principal.Principal, id string) (Entry, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Entry{}, e
	}
	db, e := s.repo.db(p)
	if e != nil {
		return Entry{}, e
	}
	const reversalSQL = `SELECT ` + entryColumns + ` FROM journal j WHERE user_id=@user AND status='posted' AND links @> jsonb_build_array(jsonb_build_object('rel','reverses','id',@id::text)) LIMIT 1`
	rows, e := db.Query(ctx, reversalSQL, pgx.NamedArgs{"user": p.UserID, "id": id})
	if e != nil {
		return Entry{}, e
	}
	v, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[Entry])
	if errors.Is(e, pgx.ErrNoRows) {
		e = principal.ErrNotFound
	}
	return v, e
}
