package tables

import (
	"context"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
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
		if e := s.repo.db.Collection("tables").FindOne(tx, filter).Decode(&old); e != nil {
			return e
		}
		next := old
		next.Archived = archived
		_, e := s.audit.Write(tx, p, "db_archive", audit.Target{Collection: "tables", ID: id}, reason, old, next, func(tx context.Context) error {
			_, e := s.repo.db.Collection("tables").UpdateOne(tx, filter, bson.M{"$set": bson.M{"archived": archived}})
			return e
		})
		return e
	})
}
func (s *Service) RestoreRows(ctx context.Context, p principal.Principal, tableID string, rows []Row, reason string) error {
	if e := p.Require("model:write"); e != nil {
		return e
	}
	if len(rows) > 500 {
		return fmt.Errorf("at most 500 rows can be restored")
	}
	table, e := s.Get(ctx, p, tableID)
	if e != nil {
		return e
	}
	before := []Row{}
	after := []Row{}
	_, e = s.audit.Write(ctx, p, "db_update_cells", audit.Target{Collection: "tables", ID: tableID}, reason, &before, &after, func(tx context.Context) error {
		for _, old := range rows {
			var current Row
			e := s.repo.db.Collection("rows").FindOne(tx, bson.M{"user_id": p.UserID, "table_id": tableID, "id": old.ID}).Decode(&current)
			if e != nil {
				return e
			}
			before = append(before, current)
			next := current
			next.Archived = old.Version == 0 || old.Archived
			if !next.Archived {
				next.Cells = old.Cells
				if e = s.formulas(tx, table.Columns, next.Cells); e != nil {
					return e
				}
			}
			next.Version++
			if _, e = s.repo.db.Collection("rows").ReplaceOne(tx, bson.M{"user_id": p.UserID, "table_id": tableID, "id": old.ID}, next); e != nil {
				return e
			}
			after = append(after, next)
		}
		return nil
	})
	return e
}

type DeleteInput struct {
	DB     string   `json:"db"`
	RowIDs []string `json:"row_ids"`
}

// DeleteRows archives rows, so db_update_cells can recreate one and undo restores it.
func (s *Service) DeleteRows(ctx context.Context, p principal.Principal, in DeleteInput) (UpdateResult, error) {
	if e := p.Require("model:write"); e != nil {
		return UpdateResult{}, e
	}
	if len(in.RowIDs) < 1 || len(in.RowIDs) > 500 {
		return UpdateResult{}, fmt.Errorf("1..500 row ids required")
	}
	if _, e := s.Get(ctx, p, in.DB); e != nil {
		return UpdateResult{}, e
	}
	result := UpdateResult{Rows: []Row{}, DependentsRecomputed: []string{}}
	before := []Row{}
	id, e := s.audit.Write(ctx, p, "db_update_cells", audit.Target{Collection: "tables", ID: in.DB}, "", &before, &result.Rows, func(tx context.Context) error {
		before, result.Rows = []Row{}, []Row{}
		for _, rowID := range in.RowIDs {
			var r Row
			filter := bson.M{"user_id": p.UserID, "table_id": in.DB, "id": rowID, "archived": bson.M{"$ne": true}}
			if e := s.repo.db.Collection("rows").FindOne(tx, filter).Decode(&r); e != nil {
				if errors.Is(e, mongo.ErrNoDocuments) {
					return principal.ErrNotFound
				}
				return e
			}
			before = append(before, r)
			r.Archived = true
			r.Version++
			if _, e := s.repo.db.Collection("rows").ReplaceOne(tx, bson.M{"user_id": p.UserID, "table_id": in.DB, "id": rowID}, r); e != nil {
				return e
			}
			result.Rows = append(result.Rows, r)
		}
		return nil
	})
	result.EventID = id
	return result, e
}
