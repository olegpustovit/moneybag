package tables

import (
	"context"
	"fmt"
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
