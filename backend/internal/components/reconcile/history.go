package reconcile

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func (s *Service) Reopen(ctx context.Context, p principal.Principal, id, reason string) error {
	if e := p.Require("ledger:write"); e != nil {
		return e
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		d, a, e := s.discrepancy(tx, p, id)
		if e != nil {
			return e
		}
		if d.AdjustmentID != "" {
			_, e = s.ledger.Reversal(tx, p, d.AdjustmentID)
			if errors.Is(e, principal.ErrNotFound) {
				_, e = s.ledger.Reverse(tx, p, ledger.ReverseInput{ID: d.AdjustmentID, Reason: reason})
			}
			if e != nil {
				return e
			}
		}
		_, e = s.Assert(tx, p, AssertInput{Account: a.Account, Date: a.Date, Amount: a.Amount, Source: a.Source})
		return e
	})
}
func (s *Service) RestoreAssertion(ctx context.Context, p principal.Principal, id string, old Assertion, reason string) error {
	if e := p.Require("ledger:write"); e != nil {
		return e
	}
	if old.ID != "" {
		_, e := s.Assert(ctx, p, AssertInput{Account: old.Account, Date: old.Date, Amount: old.Amount, Source: old.Source})
		return e
	}
	_, e := s.audit.Write(ctx, p, "assertion_archive", audit.Target{Collection: "balance_assertions", ID: id}, reason, struct {
		Archived bool `json:"archived"`
	}{false}, struct {
		Archived bool `json:"archived"`
	}{true}, func(tx context.Context) error {
		if _, e := s.repo.db.Exec(tx, `UPDATE balance_assertions SET archived=true WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"id": id, "user": p.UserID}); e != nil {
			return e
		}
		_, e := s.repo.db.Exec(tx, `UPDATE discrepancies SET status='withdrawn' WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"id": id, "user": p.UserID})
		return e
	})
	return e
}
func (s *Service) Assertion(ctx context.Context, p principal.Principal, id string) (Assertion, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Assertion{}, e
	}
	var a Assertion
	rows, e := s.repo.db.Query(ctx, `SELECT id,user_id,account,date,amount,source,ok,diff::text FROM balance_assertions WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"id": id, "user": p.UserID})
	if e != nil {
		return a, e
	}
	a, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[Assertion])
	return a, e
}
