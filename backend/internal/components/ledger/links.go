package ledger

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// LinkedEntries follows corrections without changing immutable journal documents.
func (s *Service) LinkedEntries(ctx context.Context, p principal.Principal, id string) ([]Entry, error) {
	original, e := s.Get(ctx, p, id)
	if e != nil {
		return nil, e
	}
	db, e := s.repo.db(p)
	if e != nil {
		return nil, e
	}
	out := []Entry{original}
	seen := map[string]bool{id: true}
	frontier := []string{id}
	for len(frontier) > 0 {
		cur, e := db.Collection("journal").Find(ctx, bson.M{"user_id": p.UserID, "status": "posted", "links": bson.M{"$elemMatch": bson.M{"rel": bson.M{"$in": bson.A{"reverses", "reclassifies"}}, "id": bson.M{"$in": frontier}}}}, options.Find().SetLimit(1001))
		if e != nil {
			return nil, e
		}
		next := []string{}
		for cur.Next(ctx) {
			var entry Entry
			if e = cur.Decode(&entry); e != nil {
				cur.Close(ctx)
				return nil, e
			}
			if seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			out = append(out, entry)
			next = append(next, entry.ID)
			if len(out) > 1000 {
				cur.Close(ctx)
				return nil, fmt.Errorf("correction history exceeds 1000 entries")
			}
		}
		e = cur.Err()
		cur.Close(ctx)
		if e != nil {
			return nil, e
		}
		frontier = next
	}
	return out, nil
}
