package rates

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func (s *Service) Archive(ctx context.Context, p principal.Principal, id string, archived bool, reason string) error {
	if e := p.Require("ledger:write"); e != nil {
		return e
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		var old struct {
			Archived bool `json:"archived" db:"archived"`
		}
		args := pgx.NamedArgs{"user": p.UserID, "id": id}
		if e := s.repo.db.QueryRow(tx, `SELECT archived FROM prices WHERE user_id=@user AND id=@id`, args).Scan(&old.Archived); e != nil {
			return e
		}
		next := old
		next.Archived = archived
		_, e := s.audit.Write(audit.RateOnly(tx), p, "rate_archive", audit.Target{Collection: "prices", ID: id}, reason, old, next, func(tx context.Context) error {
			_, e := s.repo.db.Exec(tx, `UPDATE prices SET archived=@archived WHERE user_id=@user AND id=@id`, pgx.NamedArgs{"user": p.UserID, "id": id, "archived": archived})
			return e
		})
		return e
	})
}
