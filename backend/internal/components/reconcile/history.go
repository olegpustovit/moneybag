package reconcile

import (
	"context"
	"errors"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
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
		if _, e := s.repo.db.Collection("balance_assertions").UpdateOne(tx, bson.M{"_id": id, "user_id": p.UserID}, bson.M{"$set": bson.M{"archived": true}}); e != nil {
			return e
		}
		_, e := s.repo.db.Collection("discrepancies").UpdateOne(tx, bson.M{"_id": id, "user_id": p.UserID}, bson.M{"$set": bson.M{"status": "withdrawn"}})
		return e
	})
	return e
}
func (s *Service) Assertion(ctx context.Context, p principal.Principal, id string) (Assertion, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Assertion{}, e
	}
	var a Assertion
	e := s.repo.db.Collection("balance_assertions").FindOne(ctx, bson.M{"_id": id, "user_id": p.UserID}).Decode(&a)
	return a, e
}
