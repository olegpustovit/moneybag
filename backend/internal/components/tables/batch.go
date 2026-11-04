package tables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type RowUpsert struct {
	RowID   string         `json:"row_id"`
	Cells   map[string]any `json:"cells"`
	Version *int           `json:"version,omitempty"`
}
type BatchOp struct {
	DB          string     `json:"db"`
	Upsert      *RowUpsert `json:"upsert,omitempty"`
	DeleteWhere string     `json:"delete_where,omitempty"`
	deleteIDs   []string
}
type BatchInput struct {
	DryRun bool      `json:"dry_run,omitempty"`
	Ops    []BatchOp `json:"ops"`
}
type Violation struct {
	DB      string `json:"db"`
	RowID   string `json:"row_id,omitempty"`
	Column  string `json:"column,omitempty"`
	Message string `json:"message"`
}
type BatchResult struct {
	RowsWritten int         `json:"rows_written"`
	Partial     bool        `json:"partial,omitempty"`
	Warnings    []Violation `json:"warnings"`
	Violations  []Violation `json:"violations"`
	EventID     string      `json:"event_id,omitempty"`
}
type BatchState struct {
	warnings []Violation
	Tables   map[string][]Row `json:"tables"`
}
type batchInvalid struct{}

func (batchInvalid) Error() string { return "batch validation failed" }

// Batch validates a final overlay before any writes, then writes rows in order on standalone MongoDB.
func (s *Service) Batch(ctx context.Context, p principal.Principal, in BatchInput) (result BatchResult, err error) {
	if err = p.Require("model:write"); err != nil {
		return result, err
	}
	if len(in.Ops) < 1 || len(in.Ops) > 500 {
		return result, fmt.Errorf("1..500 batch operations required")
	}
	encoded, err := json.Marshal(in)
	if err != nil || len(encoded) > 8*1024*1024 {
		return result, fmt.Errorf("batch must be JSON within 8 MiB")
	}
	result = BatchResult{Warnings: []Violation{}, Violations: []Violation{}}
	err = s.audit.Workflow(ctx, func(locked context.Context) error {
		before, after := BatchState{}, BatchState{}
		apply := func(tx context.Context) error {
			return s.audit.Workflow(tx, func(snapshot context.Context) error {
				var prepareErr error
				before, after, result.Violations, prepareErr = s.prepareBatch(snapshot, p, in.Ops)
				result.Warnings = after.warnings
				if prepareErr != nil {
					return prepareErr
				}
				if len(result.Violations) > 0 {
					before, after = BatchState{}, BatchState{}
					return batchInvalid{}
				}
				if in.DryRun {
					return nil
				}
				if err := s.writeBatch(snapshot, p, after); err != nil {
					return err
				}
				return nil
			})
		}
		if in.DryRun {
			return apply(locked)
		}
		note := audit.WithSummary(locked, fmt.Sprintf("Updated data tables in one batch (%d operations)", len(in.Ops)))
		var writeErr error
		result.EventID, writeErr = s.audit.Write(note, p, "db_batch", audit.Target{Collection: "tables", ID: "*"}, "", &before, &after, apply)
		if writeErr == nil {
			for _, rows := range after.Tables {
				result.RowsWritten += len(rows)
			}
		}
		return writeErr
	})
	var partial *partialWriteError
	if errors.As(err, &partial) {
		result.RowsWritten = partial.written
		result.Partial = partial.written > 0
		partial.eventID = result.EventID
	}
	if err != nil && result.EventID != "" {
		err = fmt.Errorf("batch event %s: %w", result.EventID, err)
	}
	var invalid batchInvalid
	if errors.As(err, &invalid) {
		err = nil
	}
	return result, err
}

func (s *Service) prepareBatch(ctx context.Context, p principal.Principal, ops []BatchOp) (BatchState, BatchState, []Violation, error) {
	before, after := BatchState{Tables: map[string][]Row{}}, BatchState{Tables: map[string][]Row{}}
	issues := []Violation{}
	tables := map[string]Table{}
	original := map[string]map[string]Row{}
	pending := map[string]map[string]*Row{}
	add := func(db, row, column, message string) { issues = append(issues, Violation{db, row, column, message}) }
	load := func(db, id string) (*Row, error) {
		if row := pending[db][id]; row != nil {
			return row, nil
		}
		var row Row
		err := s.repo.db.Collection("rows").FindOne(ctx, bson.M{"user_id": p.UserID, "table_id": db, "id": id}).Decode(&row)
		if errors.Is(err, mongo.ErrNoDocuments) {
			row = Row{Key: p.UserID + ":" + db + ":" + id, UserID: p.UserID, TableID: db, ID: id, Cells: map[string]any{}}
		} else if err != nil {
			return nil, err
		}
		original[db][id] = row
		row.Cells = maps.Clone(row.Cells)
		if row.Cells == nil {
			row.Cells = map[string]any{}
		}
		pending[db][id] = &row
		return &row, nil
	}
	for _, op := range ops {
		if _, ok := tables[op.DB]; !ok {
			table, err := s.Get(ctx, p, op.DB)
			if errors.Is(err, principal.ErrNotFound) {
				add(op.DB, "", "", "table does not exist")
				continue
			}
			if err != nil {
				return before, after, issues, err
			}
			tables[op.DB] = table
			original[op.DB] = map[string]Row{}
			pending[op.DB] = map[string]*Row{}
		}
		operators := 0
		if op.Upsert != nil {
			operators++
		}
		if op.DeleteWhere != "" {
			operators++
		}
		if len(op.deleteIDs) > 0 {
			operators++
		}
		if operators != 1 {
			add(op.DB, "", "", "give exactly one of upsert or delete_where")
			continue
		}
		if op.Upsert != nil {
			upsert := op.Upsert
			if len(upsert.RowID) < 1 || len(upsert.RowID) > 100 {
				add(op.DB, upsert.RowID, "", "row_id must be 1..100 bytes")
				continue
			}
			row, err := load(op.DB, upsert.RowID)
			if err != nil {
				return before, after, issues, err
			}
			if upsert.Version != nil && *upsert.Version != row.Version {
				add(op.DB, row.ID, "", "version conflict; read the row and retry")
				continue
			}
			columns := map[string]Column{}
			for _, column := range tables[op.DB].Columns {
				columns[column.ID] = column
			}
			for id, value := range upsert.Cells {
				column, ok := columns[id]
				if !ok || column.Formula != "" {
					add(op.DB, row.ID, id, "unknown column or attempted formula edit")
					continue
				}
				row.Cells[id] = value
			}
			row.Archived = false
			continue
		}
		if len(op.deleteIDs) > 0 {
			for _, id := range op.deleteIDs {
				if len(id) < 1 || len(id) > 100 {
					add(op.DB, id, "", "row_id must be 1..100 bytes")
					continue
				}
				row, err := load(op.DB, id)
				if err != nil {
					return before, after, issues, err
				}
				if row.Version == 0 || row.Archived {
					add(op.DB, id, "", "row does not exist")
					continue
				}
				row.Archived = true
			}
			continue
		}
		check := s.expr.Check(op.DeleteWhere)
		if !check.OK {
			add(op.DB, "", "", "delete_where does not compile")
			continue
		}
		if check.Type != "bool" && check.Type != "dyn" {
			add(op.DB, "", "", "delete_where must return boolean")
			continue
		}
		rows, err := s.Query(ctx, p, QueryInput{DB: op.DB, Where: op.DeleteWhere, Limit: 501})
		if err != nil {
			return before, after, issues, err
		}
		if rows.Truncated || len(rows.Rows) > 500 {
			add(op.DB, "", "", "delete_where matches more than 500 rows; narrow the filter")
			continue
		}
		candidates := map[string]Row{}
		for _, row := range rows.Rows {
			candidates[row.ID] = row
		}
		for id, row := range pending[op.DB] {
			candidates[id] = *row
		}
		for _, id := range slices.Sorted(maps.Keys(candidates)) {
			row := candidates[id]
			if row.Archived {
				continue
			}
			match, err := s.expr.Eval(ctx, op.DeleteWhere, map[string]any{"row": row.Cells})
			if err != nil {
				add(op.DB, id, "", "delete_where: "+err.Error())
				break
			}
			selected, ok := match.Value.(bool)
			if !ok {
				add(op.DB, id, "", "delete_where must return boolean")
				break
			}
			if selected {
				target, err := load(op.DB, id)
				if err != nil {
					return before, after, issues, err
				}
				target.Archived = true
			}
		}
	}
	count := 0
	for _, db := range slices.Sorted(maps.Keys(pending)) {
		for _, id := range slices.Sorted(maps.Keys(pending[db])) {
			row := pending[db][id]
			count++
			if !row.Archived {
				invalid := false
				for _, column := range tables[db].Columns {
					if column.Formula != "" {
						continue
					}
					if err := validateCells([]Column{column}, row.Cells); err != nil {
						add(db, id, column.ID, err.Error())
						invalid = true
						continue
					}
					if err := normalizeCells([]Column{column}, row.Cells); err != nil {
						add(db, id, column.ID, err.Error())
						invalid = true
					}
				}
				if !invalid {
					if err := s.formulas(ctx, tables[db].Columns, row.Cells); err != nil {
						add(db, id, "", err.Error())
					} else {
						for _, column := range tables[db].Columns {
							if column.Formula != "" {
								if err := validateCells([]Column{column}, row.Cells); err != nil {
									add(db, id, column.ID, err.Error())
								}
							}
						}
					}
				}
			}
			row.Version++
			before.Tables[db] = append(before.Tables[db], original[db][id])
			after.Tables[db] = append(after.Tables[db], *row)
		}
	}
	if count > 500 {
		add("", "", "", "batch touches more than 500 rows")
	}
	for _, state := range []BatchState{before, after} {
		encoded, err := json.Marshal(state)
		if err != nil || len(encoded) > 8*1024*1024 {
			add("", "", "", "batch audit snapshot exceeds 8 MiB")
		}
	}
	constraints, err := s.stateConstraints(ctx, p, before, &after, tables)
	if err != nil {
		return before, after, issues, err
	}
	issues = append(issues, constraints...)

	return before, after, issues, nil
}

type partialWriteError struct {
	written, total int
	eventID        string
	kind           string
	cause          error
}

func (e *partialWriteError) RecoveryMessage() string {
	kind := e.kind
	if kind == "" {
		kind = "rows"
	}
	action := "Reload the affected tables and run consistency_check before retrying."
	if kind == "table archives" {
		action = "Inspect audit history to restore earlier archives or finish archiving the remaining tables."
	}
	if kind == "tables" {
		action = "Retry db_create with the same template to create missing compatible tables."
	}
	event := ""
	if e.eventID != "" {
		event = " Batch event " + e.eventID + "."
	}
	return fmt.Sprintf("Table write stopped after %d of %d %s confirmed saved; earlier writes remain. The failing write may also have reached MongoDB.%s %s", e.written, e.total, kind, event, action)
}
func (e *partialWriteError) Error() string { return e.RecoveryMessage() + " Cause: " + e.cause.Error() }

func (e *partialWriteError) Unwrap() error { return e.cause }

func (s *Service) writeBatch(ctx context.Context, p principal.Principal, state BatchState) error {
	written, total := 0, 0
	for _, rows := range state.Tables {
		total += len(rows)
	}
	for _, db := range slices.Sorted(maps.Keys(state.Tables)) {
		for _, row := range state.Tables[db] {
			_, err := s.repo.db.Collection("rows").ReplaceOne(ctx, bson.M{"user_id": p.UserID, "table_id": db, "id": row.ID}, row, options.Replace().SetUpsert(true))
			if err != nil {
				if mongo.IsDuplicateKeyError(err) {
					err = fmt.Errorf("unique key conflict; reload the table: %w", principal.ErrConflict)
				}
				return &partialWriteError{written: written, total: total, cause: err}
			}
			written++
		}
	}
	return nil
}

func (s *Service) RestoreBatch(ctx context.Context, p principal.Principal, before, expected BatchState, reason string) error {
	if err := p.Require("model:write"); err != nil {
		return err
	}
	return s.audit.Workflow(ctx, func(locked context.Context) error {
		actual, restored := BatchState{}, BatchState{}
		_, err := s.audit.Write(locked, p, "db_batch", audit.Target{Collection: "tables", ID: "*"}, reason, &actual, &restored, func(tx context.Context) error {
			return s.audit.Workflow(tx, func(snapshot context.Context) error {
				actual, restored = BatchState{Tables: map[string][]Row{}}, BatchState{Tables: map[string][]Row{}}
				for _, db := range slices.Sorted(maps.Keys(before.Tables)) {
					table, err := s.Get(snapshot, p, db)
					if err != nil {
						return err
					}
					versions := map[string]int{}
					for _, row := range expected.Tables[db] {
						versions[row.ID] = row.Version
					}
					for _, old := range before.Tables[db] {
						var current Row
						if err = s.repo.db.Collection("rows").FindOne(snapshot, bson.M{"user_id": p.UserID, "table_id": db, "id": old.ID}).Decode(&current); err != nil {
							return err
						}
						if current.Version != versions[old.ID] {
							return principal.ErrConflict
						}
						next := current
						next.Cells = maps.Clone(old.Cells)
						next.Archived = old.Archived || old.Version == 0
						next.Version++
						if !next.Archived {
							if err = normalizeCells(table.Columns, next.Cells); err != nil {
								return err
							}
							if err = s.formulas(snapshot, table.Columns, next.Cells); err != nil {
								return err
							}
							if err = validateCells(table.Columns, next.Cells); err != nil {
								return err
							}
						}
						actual.Tables[db] = append(actual.Tables[db], current)
						restored.Tables[db] = append(restored.Tables[db], next)
					}
				}
				issues, err := s.stateConstraints(snapshot, p, actual, &restored, nil)
				if err != nil {
					return err
				}
				if len(issues) > 0 {
					return fmt.Errorf("%s", batchMessage(issues))
				}
				return s.writeBatch(snapshot, p, restored)
			})
		})
		return err
	})
}

func batchMessage(issues []Violation) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, fmt.Sprintf("%s/%s/%s: %s", issue.DB, issue.RowID, issue.Column, issue.Message))
	}
	return strings.Join(parts, "; ")
}
