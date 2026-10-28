package model

import (
	"context"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (s *Service) Archive(ctx context.Context, p principal.Principal, id string, archived bool, reason string) error {
	if e := p.Require("model:write"); e != nil {
		return e
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		var old struct {
			Archived bool `json:"archived" bson:"archived"`
		}
		filter := bson.M{"user_id": p.UserID, "id": id}
		if e := s.repo.db.Collection("scenarios").FindOne(tx, filter).Decode(&old); e != nil {
			return e
		}
		next := old
		next.Archived = archived
		_, e := s.audit.Write(tx, p, "scenario_archive", audit.Target{Collection: "scenarios", ID: id}, reason, old, next, func(tx context.Context) error {
			_, e := s.repo.db.Collection("scenarios").UpdateOne(tx, filter, bson.M{"$set": bson.M{"archived": archived}})
			return e
		})
		return e
	})
}
