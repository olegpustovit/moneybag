package ledger

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func entryTagOverlay(p principal.Principal) bson.A {
	return bson.A{bson.M{"$lookup": bson.M{"from": "entry_tags", "let": bson.M{"key": bson.M{"$concat": bson.A{p.UserID + ":", "$_id"}}}, "pipeline": bson.A{bson.M{"$match": bson.M{"user_id": p.UserID, "$expr": bson.M{"$eq": bson.A{"$_id", "$$key"}}}}}, "as": "tag_overlay"}}, bson.M{"$set": bson.M{"tags": bson.M{"$ifNull": bson.A{bson.M{"$arrayElemAt": bson.A{"$tag_overlay.tags", 0}}, "$tags"}}}}, bson.M{"$unset": "tag_overlay"}}
}

// GetMany resolves ledger references in bounded batches, including tag overlays.
func (s *Service) GetMany(ctx context.Context, p principal.Principal, ids []string) (map[string]Entry, error) {
	if err := p.Require("ledger:read"); err != nil {
		return nil, err
	}
	if len(ids) > 250000 {
		return nil, fmt.Errorf("at most 250000 entry references")
	}
	db, err := s.repo.db(p)
	if err != nil {
		return nil, err
	}
	out := map[string]Entry{}
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		pipeline := append(bson.A{bson.M{"$match": bson.M{"user_id": p.UserID, "_id": bson.M{"$in": chunk}}}}, entryTagOverlay(p)...)
		cursor, err := db.Collection("journal").Aggregate(ctx, pipeline)
		if err != nil {
			return nil, err
		}
		for cursor.Next(ctx) {
			var entry Entry
			if err = cursor.Decode(&entry); err != nil {
				break
			}
			out[entry.ID] = entry
		}
		cursorErr := cursor.Err()
		_ = cursor.Close(ctx)
		if err != nil {
			return nil, err
		}
		if cursorErr != nil {
			return nil, cursorErr
		}
	}
	return out, nil
}
