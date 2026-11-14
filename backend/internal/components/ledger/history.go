package ledger

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func (s *Service) SetDraftStatus(ctx context.Context, p principal.Principal, id, status, reason string) error {
	if e := p.Require("ledger:write"); e != nil {
		return e
	}
	if status != "draft" && status != "cancelled" {
		return fmt.Errorf("status must be draft or cancelled")
	}
	old, e := s.Get(ctx, p, id)
	if e != nil {
		return e
	}
	if old.Status == "posted" {
		return fmt.Errorf("posted entries are immutable; append a reversal")
	}
	entry := old
	entry.Status = status
	db, _ := s.repo.db(p)
	_, e = s.audit.Write(ctx, p, "entry_draft_status", audit.Target{Collection: "journal", ID: id}, reason, old, entry, func(tx context.Context) error {
		r, e := db.Exec(tx, `UPDATE journal SET status=@status WHERE id=@id AND user_id=@user AND status=@old`, pgx.NamedArgs{"status": status, "id": id, "user": p.UserID, "old": old.Status})
		if e == nil && r.RowsAffected() != 1 {
			return principal.ErrConflict
		}
		return e
	})
	return e
}
