package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Revision reads the user's durable data revision once per read request.
func (s *Service) Revision(ctx context.Context, p principal.Principal) (int64, error) {
	if err := p.Valid(); err != nil {
		return 0, err
	}
	return reqcache.Do(ctx, "revision|"+p.UserID, func() (int64, error) {
		var counter struct {
			N int64 `bson:"n"`
		}
		err := s.repo.DB.Collection("counters").FindOne(ctx, bson.M{"_id": "rev:" + p.UserID}).Decode(&counter)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, nil
		}
		if err != nil {
			return 0, fmt.Errorf("read data revision: %w", err)
		}
		return counter.N, nil
	})
}

// Mutation invalidates even failed writes, which may have changed part of a standalone workflow.
func (s *Service) Mutation(ctx context.Context, p principal.Principal, fn func(context.Context) error) (result error) {
	if _, err := mongodb.Shared(s.repo.DB, p); err != nil {
		return err
	}
	if s.OnChange != nil {
		s.OnChange(p.UserID, false)
	}
	defer func() {
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, err := s.repo.DB.Collection("counters").UpdateOne(finish, bson.M{"_id": "rev:" + p.UserID}, bson.M{"$inc": bson.M{"n": int64(1)}, "$setOnInsert": bson.M{"user_id": p.UserID}}, options.UpdateOne().SetUpsert(true))
		if s.OnChange != nil {
			s.OnChange(p.UserID, true)
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("advance data revision: %w", err))
		}
	}()
	return fn(ctx)
}
