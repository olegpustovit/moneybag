package ledger

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (s *Service) UpdateDraft(ctx context.Context, p principal.Principal, in EntryInput) (EntryResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return EntryResult{}, e
	}
	if in.Commit {
		return EntryResult{}, fmt.Errorf("post a saved draft separately with id and commit=true")
	}
	if e := Validate(in); e != nil {
		return EntryResult{}, e
	}
	var out EntryResult
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		old, e := s.Get(tx, p, in.ID)
		if e != nil {
			return e
		}
		if old.Status != "draft" {
			return fmt.Errorf("only drafts can be edited; reverse posted entries")
		}
		if e = s.checkAccounts(tx, p, in, false); e != nil {
			return e
		}
		entry := old
		entry.Date = in.Date
		entry.Payee = in.Payee
		entry.Postings = in.Postings
		if in.Tags == nil {
			in.Tags = []string{}
		}
		entry.Tags = in.Tags
		if old.Kind == "normal" || old.Kind == "transfer" {
			entry.Kind = inferKind(in.Postings)
		}
		db, e := s.repo.db(p)
		if e != nil {
			return e
		}
		event, e := s.audit.Write(tx, p, "entry_update", audit.Target{Collection: "journal", ID: in.ID}, "", old, entry, func(tx context.Context) error {
			r, e := db.Collection("journal").ReplaceOne(tx, bson.M{"_id": in.ID, "user_id": p.UserID, "status": "draft"}, entry)
			if e != nil {
				return e
			}
			if r.MatchedCount != 1 {
				return principal.ErrConflict
			}
			_, e = db.Collection("entry_tags").UpdateOne(tx, bson.M{"_id": p.UserID + ":" + in.ID, "user_id": p.UserID}, bson.M{"$set": bson.M{"tags": in.Tags}})
			return e
		})
		out = EntryResult{entry, true, event}
		return e
	})
	return out, e
}
