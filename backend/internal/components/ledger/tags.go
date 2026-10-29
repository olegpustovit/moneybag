package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"slices"
	"strings"
)

type TagInput struct {
	EntryIDs []string `json:"entry_ids,omitempty"`
	// Where is a CEL expression over the entry row (as entries_list returns it) selecting up to 10000 posted entries; use instead of entry_ids.
	Where  string   `json:"where,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
}
type TagResult struct {
	Matched int    `json:"matched"`
	Updated int    `json:"updated"`
	EventID string `json:"event_id"`
}

func (s *Service) SetTags(ctx context.Context, p principal.Principal, in TagInput) (TagResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return TagResult{}, e
	}
	if in.Where != "" {
		if len(in.EntryIDs) > 0 {
			return TagResult{}, fmt.Errorf("use entry_ids or where, not both")
		}
		ids, e := s.matchIDs(ctx, p, in.Where)
		if e != nil {
			return TagResult{}, e
		}
		if in.DryRun {
			return TagResult{Matched: len(ids)}, nil
		}
		in.EntryIDs = ids
	} else if len(in.EntryIDs) < 1 || len(in.EntryIDs) > 100 {
		return TagResult{}, fmt.Errorf("1..100 entries required, or a where expression")
	}
	if len(in.Add)+len(in.Remove) > 100 || in.DryRun && in.Where == "" {
		return TagResult{}, fmt.Errorf("at most 100 tag changes; dry_run needs where")
	}
	for _, tag := range append(slices.Clone(in.Add), in.Remove...) {
		if strings.TrimSpace(tag) == "" || len(tag) > 100 {
			return TagResult{}, fmt.Errorf("tags must be 1..100 characters")
		}
	}
	result := TagResult{Matched: len(in.EntryIDs)}
	db, _ := s.repo.db(p)
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		ids := []string{}
		before, after := map[string][]string{}, map[string][]string{}
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
			ids = append(ids, id)
			before[id], after[id] = entry.Tags, tags
		}
		if len(ids) == 0 {
			return nil
		}
		// One event per run so a bulk run undoes as a unit; a single entry keeps the plain tag-list format.
		var b, a any = before, after
		if len(ids) == 1 {
			b, a = before[ids[0]], after[ids[0]]
		}
		event, e := s.audit.Write(tx, p, "tags_set", audit.Target{Collection: "journal", ID: ids[0]}, "", b, a, func(ctx context.Context) error {
			for _, id := range ids {
				if _, e := db.Collection("entry_tags").ReplaceOne(ctx, bson.M{"_id": p.UserID + ":" + id, "user_id": p.UserID}, bson.M{"_id": p.UserID + ":" + id, "user_id": p.UserID, "entry_id": id, "tags": after[id]}, options.Replace().SetUpsert(true)); e != nil {
					return e
				}
			}
			return nil
		})
		result.Updated, result.EventID = len(ids), event
		return e
	})
	return result, e
}

// matchIDs returns the ids of posted entries for which where is true; at most 10000.
func (s *Service) matchIDs(ctx context.Context, p principal.Principal, where string) ([]string, error) {
	if s.Expr == nil {
		return nil, fmt.Errorf("where expressions are unavailable")
	}
	ids := []string{}
	in := EntriesInput{Status: "posted", Limit: 500}
	for {
		page, e := s.repo.Entries(ctx, p, in)
		if e != nil {
			return nil, e
		}
		for _, entry := range page.Items {
			b, e := json.Marshal(entry)
			if e != nil {
				return nil, e
			}
			var row map[string]any
			if e = json.Unmarshal(b, &row); e != nil {
				return nil, e
			}
			r, e := s.Expr.Eval(ctx, where, map[string]any{"row": row})
			if e != nil {
				return nil, fmt.Errorf("where: %w", e)
			}
			ok, isBool := r.Value.(bool)
			if !isBool {
				return nil, fmt.Errorf("where must evaluate to a boolean")
			}
			if ok {
				if len(ids) == 10000 {
					return nil, fmt.Errorf("where matches more than 10000 entries")
				}
				ids = append(ids, entry.ID)
			}
		}
		if page.NextCursor == 0 {
			return ids, nil
		}
		in.Cursor = page.NextCursor
	}
}
