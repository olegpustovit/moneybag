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

// Revisions counts a user's writes: Data all of them, Rates those that only touched rates.
type Revisions struct {
	Data  int64 `bson:"n"`
	Rates int64 `bson:"rates"`
}

// Ledger counts writes other than rate-only ones; while it holds, a render differs only in valuation.
func (r Revisions) Ledger() int64 { return r.Data - r.Rates }

type rateOnlyKey struct{}

// RateOnly marks audited writes under ctx as changing only rates or rate provider status.
func RateOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, rateOnlyKey{}, true)
}

// Revision reads the user's durable data revision once per read request.
func (s *Service) Revision(ctx context.Context, p principal.Principal) (int64, error) {
	r, err := s.Revisions(ctx, p)
	return r.Data, err
}

// Revisions reads the user's durable write counters once per read request.
func (s *Service) Revisions(ctx context.Context, p principal.Principal) (Revisions, error) {
	if err := p.Valid(); err != nil {
		return Revisions{}, err
	}
	return reqcache.Do(ctx, "revision|"+p.UserID, func() (Revisions, error) {
		var counter Revisions
		err := s.repo.DB.Collection("counters").FindOne(ctx, bson.M{"_id": "rev:" + p.UserID}).Decode(&counter)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Revisions{}, nil
		}
		if err != nil {
			return Revisions{}, fmt.Errorf("read data revision: %w", err)
		}
		return counter, nil
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
		inc := bson.M{"n": int64(1)}
		if ctx.Value(rateOnlyKey{}) != nil {
			inc["rates"] = int64(1)
		}
		_, err := s.repo.DB.Collection("counters").UpdateOne(finish, bson.M{"_id": "rev:" + p.UserID}, bson.M{"$inc": inc, "$setOnInsert": bson.M{"user_id": p.UserID}}, options.UpdateOne().SetUpsert(true))
		if s.OnChange != nil {
			s.OnChange(p.UserID, true)
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("advance data revision: %w", err))
		}
	}()
	return fn(ctx)
}
