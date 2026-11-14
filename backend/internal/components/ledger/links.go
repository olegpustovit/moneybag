package ledger

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

// LinkedEntries follows corrections without changing immutable journal documents.
func (s *Service) LinkedEntries(ctx context.Context, p principal.Principal, id string) ([]Entry, error) {
	original, e := s.Get(ctx, p, id)
	if e != nil {
		return nil, e
	}
	db, e := s.repo.db(p)
	if e != nil {
		return nil, e
	}
	out := []Entry{original}
	seen := map[string]bool{id: true}
	frontier := []string{id}
	for len(frontier) > 0 {
		const linksSQL = `SELECT ` + entryColumns + ` FROM journal j WHERE user_id=@user AND status='posted'
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(links,'null'::jsonb),'[]'::jsonb)) link WHERE link->>'rel' IN ('reverses','reclassifies') AND link->>'id'=ANY(@frontier::text[])) LIMIT 1001`
		cur, e := db.Query(ctx, linksSQL, pgx.NamedArgs{"user": p.UserID, "frontier": frontier})
		if e != nil {
			return nil, e
		}
		next := []string{}
		for cur.Next() {
			entry, err := pgx.RowToStructByName[Entry](cur)
			if err != nil {
				e = err
				cur.Close()
				return nil, e
			}
			if seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			out = append(out, entry)
			next = append(next, entry.ID)
			if len(out) > 1000 {
				cur.Close()
				return nil, fmt.Errorf("correction history exceeds 1000 entries")
			}
		}
		e = cur.Err()
		cur.Close()
		if e != nil {
			return nil, e
		}
		frontier = next
	}
	return out, nil
}
