package tables

import (
	"context"
	"fmt"
	"slices"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// ConstraintIssues detects incomplete receipt metadata after interrupted writes.
func (s *Service) ConstraintIssues(ctx context.Context, p principal.Principal) ([]Violation, error) {
	if err := p.Require("ledger:read"); err != nil {
		return nil, err
	}
	definitions, err := s.allTables(ctx, p)
	if err != nil {
		return nil, err
	}
	issues := []Violation{}
	for _, table := range definitions {
		if len(table.Keys) == 0 && len(table.Checks) == 0 && table.Ledger == nil {
			continue
		}
		cursor, err := s.repo.db.Collection("rows").Find(ctx, bson.M{"user_id": p.UserID, "table_id": table.ID, "archived": bson.M{"$ne": true}})
		if err != nil {
			return nil, err
		}
		rows := []Row{}
		bytes := 0
		for cursor.Next(ctx) {
			bytes += len(cursor.Current)
			if len(rows) >= 250000 || bytes > 64*1024*1024 {
				err = fmt.Errorf("table %s exceeds the consistency scan budget", table.ID)
				break
			}
			var row Row
			if err = cursor.Decode(&row); err != nil {
				break
			}
			rows = append(rows, row)
		}
		ce := cursor.Err()
		_ = cursor.Close(ctx)
		if err != nil {
			return nil, err
		}
		if ce != nil {
			return nil, ce
		}
		after := BatchState{Tables: map[string][]Row{table.ID: slices.Clone(rows)}}
		for _, row := range rows {
			if err := validateCells(table.Columns, row.Cells); err != nil {
				issues = append(issues, Violation{DB: table.ID, RowID: row.ID, Message: err.Error()})
			}
			hashes, err := keyHashes(table, row.Cells)
			if err != nil {
				issues = append(issues, Violation{DB: table.ID, RowID: row.ID, Message: err.Error()})
			} else if !slices.Equal(hashes, row.KeyHashes) {
				issues = append(issues, Violation{DB: table.ID, RowID: row.ID, Message: "stored key metadata is incomplete; rewrite this row to rebuild its keys"})
			}
		}
		found, err := s.stateConstraints(ctx, p, BatchState{Tables: map[string][]Row{table.ID: rows}}, &after, map[string]Table{table.ID: table})
		if err != nil {
			return nil, err
		}
		issues = append(issues, found...)

	}
	return issues, nil
}
