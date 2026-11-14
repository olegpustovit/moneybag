package ledger

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
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
			r, e := db.Exec(tx, `UPDATE journal SET date=@date,payee=@payee,postings=@postings,tags=@tags,kind=@kind WHERE id=@id AND user_id=@user AND status='draft'`, pgx.NamedArgs{"date": entry.Date, "payee": entry.Payee, "postings": entry.Postings, "tags": entry.Tags, "kind": entry.Kind, "id": in.ID, "user": p.UserID})
			if e != nil {
				return e
			}
			if r.RowsAffected() != 1 {
				return principal.ErrConflict
			}
			_, e = db.Exec(tx, `UPDATE entry_tags SET tags=@tags WHERE id=@key AND user_id=@user`, pgx.NamedArgs{"tags": in.Tags, "key": p.UserID + ":" + in.ID, "user": p.UserID})
			return e
		})
		out = EntryResult{entry, true, event}
		return e
	})
	return out, e
}
