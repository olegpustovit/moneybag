package ledger

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

// GetMany resolves ledger references in bounded batches, including tag overlays.
func (s *Service) GetMany(ctx context.Context, p principal.Principal, ids []string) (map[string]Entry, error) {
	if err := p.Require("ledger:read"); err != nil {
		return nil, err
	}
	if len(ids) > 250000 {
		return nil, fmt.Errorf("at most 250000 entry references")
	}
	db, err := s.repo.db(p)
	if err != nil {
		return nil, err
	}
	out := map[string]Entry{}
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		const manySQL = `SELECT ` + overlaidEntryColumns + ` FROM journal j ` + entryTagJoin + ` WHERE j.user_id=@user AND j.id=ANY(@ids::text[])`
		rows, err := db.Query(ctx, manySQL, pgx.NamedArgs{"user": p.UserID, "ids": chunk})
		if err != nil {
			return nil, err
		}
		entries, err := pgx.CollectRows(rows, pgx.RowToStructByName[Entry])
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			out[entry.ID] = entry
		}
	}
	return out, nil
}
