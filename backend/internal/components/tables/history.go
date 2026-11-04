package tables

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maps"
)

func (s *Service) Archive(ctx context.Context, p principal.Principal, id string, archived bool, reason string) error {
	if e := p.Require("model:write"); e != nil {
		return e
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		if archived {
			if err := s.guardTableDrop(tx, p, id); err != nil {
				return err
			}
		} else {
			if err := s.guardTableRestore(tx, p, id); err != nil {
				return err
			}
		}
		var table Table
		filter := bson.M{"user_id": p.UserID, "id": id}
		if err := s.repo.db.Collection("tables").FindOne(tx, filter).Decode(&table); err != nil {
			return err
		}
		old := table
		table.Archived = archived
		_, e := s.audit.Write(tx, p, "db_archive", audit.Target{Collection: "tables", ID: id}, reason, old, table, func(tx context.Context) error {
			_, err := s.repo.db.Collection("tables").UpdateOne(tx, filter, bson.M{"$set": bson.M{"archived": archived}})
			return err
		})
		return e
	})
}
func (s *Service) RestoreRows(ctx context.Context, p principal.Principal, tableID string, rows []Row, reason string) error {
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		return s.restoreRows(tx, p, tableID, rows, reason)
	})
}

func (s *Service) restoreRows(ctx context.Context, p principal.Principal, tableID string, rows []Row, reason string) error {
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
		return s.audit.Workflow(tx, func(tx context.Context) error {
			before = []Row{}
			after = []Row{}
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
					if e = normalizeCells(table.Columns, next.Cells); e != nil {
						return e
					}
					if e = s.formulas(tx, table.Columns, next.Cells); e != nil {
						return e
					}
					if e = validateCells(table.Columns, next.Cells); e != nil {
						return fmt.Errorf("row %s: %w", old.ID, e)
					}
				}
				next.Version++
				after = append(after, next)
			}
			state := BatchState{Tables: map[string][]Row{tableID: after}}
			issues, err := s.stateConstraints(tx, p, BatchState{Tables: map[string][]Row{tableID: before}}, &state, map[string]Table{tableID: table})
			if err != nil {
				return err
			}
			if len(issues) > 0 {
				return fmt.Errorf("%s", batchMessage(issues))
			}
			after = state.Tables[tableID]
			if err = s.writeBatch(tx, p, state); err != nil {
				return err
			}

			return nil
		})
	})
	return e
}

type DeleteInput struct {
	DB     string   `json:"db"`
	RowIDs []string `json:"row_ids,omitempty"`
	Where  string   `json:"where,omitempty"`
}

// DeleteRows archives rows, so db_update_cells can recreate one and undo restores it.
func (s *Service) DeleteRows(ctx context.Context, p principal.Principal, in DeleteInput) (result UpdateResult, err error) {
	if err = p.Require("model:write"); err != nil {
		return result, err
	}
	if (in.Where == "") == (len(in.RowIDs) == 0) {
		return result, fmt.Errorf("give exactly one of row_ids or where")
	}
	if len(in.RowIDs) > 500 {
		return result, fmt.Errorf("at most 500 rows required")
	}
	err = s.audit.Workflow(ctx, func(locked context.Context) error {
		result, err = s.changeRows(locked, p, in.DB, []BatchOp{{DB: in.DB, DeleteWhere: in.Where, deleteIDs: in.RowIDs}})
		return err
	})
	return result, err
}

type AlterInput struct {
	DB            string            `json:"db"`
	AddColumns    []Column          `json:"add_columns,omitempty"`
	RemoveColumns []string          `json:"remove_columns,omitempty"`
	Description   *string           `json:"description,omitempty"`
	Keys          *[][]string       `json:"keys,omitempty"`
	Checks        *[]RowCheck       `json:"checks,omitempty"`
	Ledger        *LedgerConstraint `json:"ledger,omitempty"`
}

type ColumnsState struct {
	Columns       []Column          `json:"columns"`
	Description   string            `json:"description,omitempty"`
	Keys          [][]string        `json:"keys,omitempty"`
	Checks        []RowCheck        `json:"checks,omitempty"`
	Ledger        *LedgerConstraint `json:"ledger,omitempty"`
	SchemaVersion int               `json:"schema_version,omitempty"`
}

func schemaState(table Table) ColumnsState {
	return ColumnsState{Columns: table.Columns, Description: table.Description, Keys: table.Keys, Checks: table.Checks, Ledger: table.Ledger, SchemaVersion: 1}
}

// Alter retains hidden cells; new constraints are validated against every existing active row.
func (s *Service) Alter(ctx context.Context, p principal.Principal, in AlterInput) (table Table, err error) {
	err = s.audit.Workflow(ctx, func(locked context.Context) error {
		table, err = s.Get(locked, p, in.DB)
		if err != nil {
			return err
		}
		remove := map[string]bool{}
		for _, id := range in.RemoveColumns {
			remove[id] = true
		}
		columns := []Column{}
		for _, column := range table.Columns {
			if remove[column.ID] {
				delete(remove, column.ID)
			} else {
				columns = append(columns, column)
			}
		}
		if len(remove) > 0 {
			return fmt.Errorf("remove_columns not found: %v", remove)
		}
		table.Columns = append(columns, in.AddColumns...)
		if in.Description != nil {
			table.Description = *in.Description
		}
		if in.Keys != nil {
			table.Keys = *in.Keys
		}
		if in.Checks != nil {
			table.Checks = *in.Checks
		}
		if in.Ledger != nil {
			table.Ledger = in.Ledger
			if in.Ledger.Entry == "" && in.Ledger.Amount == "" && len(in.Ledger.Accounts) == 0 {
				table.Ledger = nil
			}
		}
		return s.setDefinition(locked, p, table, "")
	})
	return table, err
}

func (s *Service) SetColumns(ctx context.Context, p principal.Principal, id string, columns []Column, reason string) error {
	return s.RestoreSchema(ctx, p, id, ColumnsState{Columns: columns}, reason)
}

// Historical schema events without a version only restore columns, preserving newer metadata.
func (s *Service) RestoreSchema(ctx context.Context, p principal.Principal, id string, state ColumnsState, reason string) error {
	return s.audit.Workflow(ctx, func(locked context.Context) error {
		table, err := s.Get(locked, p, id)
		if err != nil {
			return err
		}
		table.Columns = state.Columns
		if state.SchemaVersion > 0 {
			table.Description = state.Description
			table.Keys = state.Keys
			table.Checks = state.Checks
			table.Ledger = state.Ledger
		}
		return s.setDefinition(locked, p, table, reason)
	})
}

func (s *Service) setDefinition(ctx context.Context, p principal.Principal, table Table, reason string) error {
	if err := p.Require("model:write"); err != nil {
		return err
	}
	old, err := s.Get(ctx, p, table.ID)
	if err != nil {
		return err
	}
	if len(table.Description) > 10000 {
		return fmt.Errorf("table description exceeds 10000 bytes")
	}
	if err = s.validateDefinition(table); err != nil {
		return err
	}
	if err = s.referenceSchemas(ctx, p, table.ID, table.Columns); err != nil {
		return err
	}
	if err = s.columnIndexes(ctx, table.Columns, table.Keys...); err != nil {
		return err
	}
	if err := s.uniqueIndex(ctx); err != nil {
		return err
	}
	oldTypes := map[string]string{}
	for _, column := range old.Columns {
		oldTypes[column.ID] = column.Type
	}
	for _, column := range table.Columns {
		if typ, exists := oldTypes[column.ID]; exists && typ != column.Type {
			return fmt.Errorf("column %s: changing its type is not supported", column.ID)
		}
	}
	migrate := len(old.Keys) > 0 || len(table.Keys) > 0 || len(table.Checks) > 0 || table.Ledger != nil
	_, err = s.audit.Write(ctx, p, "db_alter", audit.Target{Collection: "tables", ID: table.ID}, reason, schemaState(old), schemaState(table), func(tx context.Context) error {
		return s.audit.Workflow(tx, func(snapshot context.Context) error {
			cursor, err := s.repo.db.Collection("rows").Find(snapshot, bson.M{"user_id": p.UserID, "table_id": table.ID, "archived": bson.M{"$ne": true}})
			if err != nil {
				return err
			}
			before, after := BatchState{Tables: map[string][]Row{}}, BatchState{Tables: map[string][]Row{}}
			violations := []Violation{}
			for cursor.Next(snapshot) {
				var row Row
				if err = cursor.Decode(&row); err != nil {
					break
				}
				prior := row
				row.Cells = maps.Clone(row.Cells)
				if migrate {
					if err = s.formulas(snapshot, table.Columns, row.Cells); err != nil {
						break
					}
				}
				if err = validateCells(table.Columns, row.Cells); err != nil {
					break
				}
				before.Tables[table.ID] = append(before.Tables[table.ID], prior)
				if migrate {
					row.Version++
				}
				after.Tables[table.ID] = append(after.Tables[table.ID], row)
			}
			cursorErr := cursor.Err()
			_ = cursor.Close(snapshot)
			if err != nil {
				return err
			}
			if cursorErr != nil {
				return cursorErr
			}
			violations, err = s.stateConstraints(snapshot, p, before, &after, map[string]Table{table.ID: table})
			if err != nil {
				return err
			}
			if len(violations) > 0 {
				return fmt.Errorf("%s", batchMessage(violations))
			}
			if migrate {
				if err = s.writeBatch(snapshot, p, after); err != nil {
					return err
				}
			}
			_, err = s.repo.db.Collection("tables").UpdateOne(snapshot, bson.M{"user_id": p.UserID, "id": table.ID}, bson.M{"$set": bson.M{"columns": table.Columns, "description": table.Description, "keys": table.Keys, "checks": table.Checks, "ledger": table.Ledger}})
			return err
		})
	})
	return err
}
