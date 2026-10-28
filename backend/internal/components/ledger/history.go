package ledger

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
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
		r, e := db.Collection("journal").UpdateOne(tx, bson.M{"_id": id, "user_id": p.UserID, "status": old.Status}, bson.M{"$set": bson.M{"status": status}})
		if e == nil && r.MatchedCount != 1 {
			return principal.ErrConflict
		}
		return e
	})
	return e
}
