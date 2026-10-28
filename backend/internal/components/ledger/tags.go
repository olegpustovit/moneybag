package ledger

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"slices"
	"strings"
)

type TagInput struct {
	EntryIDs []string `json:"entry_ids"`
	Add      []string `json:"add,omitempty"`
	Remove   []string `json:"remove,omitempty"`
}
type TagResult struct {
	Updated int    `json:"updated"`
	EventID string `json:"event_id"`
}

func (s *Service) SetTags(ctx context.Context, p principal.Principal, in TagInput) (TagResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return TagResult{}, e
	}
	if len(in.EntryIDs) < 1 || len(in.EntryIDs) > 100 || len(in.Add)+len(in.Remove) > 100 {
		return TagResult{}, fmt.Errorf("1..100 entries and at most 100 tag changes required")
	}
	for _, tag := range append(slices.Clone(in.Add), in.Remove...) {
		if strings.TrimSpace(tag) == "" || len(tag) > 100 {
			return TagResult{}, fmt.Errorf("tags must be 1..100 characters")
		}
	}
	result := TagResult{}
	db, _ := s.repo.db(p)
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		for _, id := range in.EntryIDs {
			entry, e := s.Get(tx, p, id)
			if e != nil {
				return e
			}
			tags := []string{}
			for _, tag := range entry.Tags {
				if !slices.Contains(in.Remove, tag) {
					tags = append(tags, tag)
				}
			}
			for _, tag := range in.Add {
				if !slices.Contains(tags, tag) {
					tags = append(tags, tag)
				}
			}
			if len(tags) > 50 {
				return fmt.Errorf("entry exceeds 50 tags")
			}
			if slices.Equal(tags, entry.Tags) {
				continue
			}
			event, e := s.audit.Write(tx, p, "tags_set", audit.Target{Collection: "journal", ID: id}, "", entry.Tags, tags, func(ctx context.Context) error {
				_, e := db.Collection("entry_tags").ReplaceOne(ctx, bson.M{"_id": p.UserID + ":" + id, "user_id": p.UserID}, bson.M{"_id": p.UserID + ":" + id, "user_id": p.UserID, "entry_id": id, "tags": tags}, options.Replace().SetUpsert(true))
				return e
			})
			if e != nil {
				return e
			}
			result.Updated++
			result.EventID = event
		}
		return nil
	})
	return result, e
}
