package ledger

import (
	"context"
	"errors"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
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
	var v Entry
	e = db.Collection("journal").FindOne(ctx, bson.M{"user_id": p.UserID, "status": "posted", "links": bson.M{"$elemMatch": bson.M{"rel": "reverses", "id": id}}}).Decode(&v)
	if errors.Is(e, mongo.ErrNoDocuments) {
		e = principal.ErrNotFound
	}
	return v, e
}
