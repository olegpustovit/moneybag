package ledger

import (
	"context"
	"errors"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

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
