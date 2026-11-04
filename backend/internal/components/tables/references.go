package tables

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *Service) referenceSchemas(ctx context.Context, p principal.Principal, id string, columns []Column) error {
	seen := map[string]bool{id: true}
	for _, column := range columns {
		if column.Type != "ref" || seen[column.Table] {
			continue
		}
		if _, err := s.Get(ctx, p, column.Table); err != nil {
			return fmt.Errorf("column %s references unavailable table %s: %w", column.ID, column.Table, err)
		}
		seen[column.Table] = true
	}
	return nil
}

func (s *Service) columnIndexes(ctx context.Context, columns []Column, keys ...[]string) error {
	indexed := map[string]bool{}
	for _, key := range keys {
		for _, id := range key {
			indexed[id] = true
		}
	}
	for _, column := range columns {
		if column.Type != "entry" && column.Type != "account" && column.Type != "ref" && !indexed[column.ID] {
			continue
		}
		hash := sha256.Sum256([]byte(column.ID))
		_, err := s.repo.db.Collection("rows").Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys:    bson.D{{Key: "user_id", Value: 1}, {Key: "table_id", Value: 1}, {Key: "cells." + column.ID, Value: 1}},
			Options: options.Index().SetName(fmt.Sprintf("table_column_%x", hash[:8])),
		})
		if err != nil {
			return fmt.Errorf("index column %s: %w", column.ID, err)
		}
	}
	return nil
}

func (s *Service) allTables(ctx context.Context, p principal.Principal) ([]Table, error) {
	if err := p.Valid(); err != nil {
		return nil, err
	}
	cursor, err := s.repo.db.Collection("tables").Find(ctx, bson.M{"user_id": p.UserID, "archived": bson.M{"$ne": true}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var out []Table
	err = cursor.All(ctx, &out)
	return out, err
}

func (s *Service) rowReference(ctx context.Context, p principal.Principal, table, id string, state BatchState) (bool, error) {
	for _, row := range state.Tables[table] {
		if row.ID == id {
			return !row.Archived, nil
		}
	}
	if _, present := state.Tables[table]; !present {
		if _, err := s.Get(ctx, p, table); err != nil {
			if errors.Is(err, principal.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
	}
	err := s.repo.db.Collection("rows").FindOne(ctx, bson.M{"user_id": p.UserID, "table_id": table, "id": id, "archived": bson.M{"$ne": true}}).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	return err == nil, err
}

// Reference validation sees the complete overlay, so repointing and deleting in
// one batch is allowed while deleting a still-referenced row is refused.
func (s *Service) validateReferences(ctx context.Context, p principal.Principal, state BatchState, definitions map[string]Table) ([]Violation, error) {
	issues := []Violation{}
	deleted := map[string][]string{}
	type referenceKey struct{ kind, table, id string }
	cache := map[referenceKey]bool{}
	for _, db := range slices.Sorted(maps.Keys(state.Tables)) {
		definition, ok := definitions[db]
		if !ok {
			var err error
			definition, err = s.Get(ctx, p, db)
			if err != nil {
				return issues, err
			}
		}
		for _, row := range state.Tables[db] {
			if row.Archived {
				deleted[db] = append(deleted[db], row.ID)
				continue
			}
			for _, column := range definition.Columns {
				if column.Type != "entry" && column.Type != "account" && column.Type != "ref" {
					continue
				}
				value := row.Cells[column.ID]
				if value == nil {
					continue
				}
				id, ok := value.(string)
				if !ok {
					issues = append(issues, Violation{db, row.ID, column.ID, "reference must be an id string"})
					continue
				}
				if id == "" {
					continue
				}
				key := referenceKey{column.Type, column.Table, id}
				exists, known := cache[key]
				if !known {
					var err error
					switch column.Type {
					case "ref":
						exists, err = s.rowReference(ctx, p, column.Table, id, state)
					case "entry":
						if s.Ledger == nil {
							return issues, fmt.Errorf("ledger references are not configured")
						}
						_, err = s.Ledger.Get(ctx, p, id)
						exists = err == nil
					case "account":
						if s.Ledger == nil {
							return issues, fmt.Errorf("ledger references are not configured")
						}
						_, err = s.Ledger.Account(ctx, p, id)
						exists = err == nil
					}
					if err != nil && !errors.Is(err, principal.ErrNotFound) {
						return issues, err
					}
					cache[key] = exists
				}
				if !exists {
					issues = append(issues, Violation{db, row.ID, column.ID, fmt.Sprintf("%s reference %s does not exist in your data", column.Type, id)})
				}
			}
		}
	}
	if len(deleted) == 0 {
		return issues, nil
	}
	definitionsList, err := s.allTables(ctx, p)
	if err != nil {
		return issues, err
	}
	for _, definition := range definitionsList {
		for _, column := range definition.Columns {
			ids := deleted[column.Table]
			if column.Type != "ref" || len(ids) == 0 {
				continue
			}
			cursor, err := s.repo.db.Collection("rows").Find(ctx, bson.M{"user_id": p.UserID, "table_id": definition.ID, "archived": bson.M{"$ne": true}, "cells." + column.ID: bson.M{"$in": ids}})
			if err != nil {
				return issues, err
			}
			for cursor.Next(ctx) {
				var row Row
				if err = cursor.Decode(&row); err != nil {
					break
				}
				for _, changed := range state.Tables[definition.ID] {
					if changed.ID == row.ID {
						row = changed
						break
					}
				}
				if row.Archived {
					continue
				}
				id, _ := row.Cells[column.ID].(string)
				if slices.Contains(ids, id) {
					issues = append(issues, Violation{definition.ID, row.ID, column.ID, fmt.Sprintf("still references %s/%s; repoint or delete this row in the same batch", column.Table, id)})
				}
			}
			cursorErr := cursor.Err()
			_ = cursor.Close(ctx)
			if err != nil {
				return issues, err
			}
			if cursorErr != nil {
				return issues, cursorErr
			}
		}
	}
	return issues, nil
}

func (s *Service) guardTableDrop(ctx context.Context, p principal.Principal, id string) error {
	definitions, err := s.allTables(ctx, p)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if definition.ID == id {
			continue
		}
		for _, column := range definition.Columns {
			if column.Type == "ref" && column.Table == id {
				return fmt.Errorf("table %s column %s references %s; remove that reference before dropping the table", definition.ID, column.ID, id)
			}
		}
	}
	return nil
}

func (s *Service) guardTableRestore(ctx context.Context, p principal.Principal, id string) error {
	var table Table
	if err := s.repo.db.Collection("tables").FindOne(ctx, bson.M{"user_id": p.UserID, "id": id}).Decode(&table); err != nil {
		return err
	}
	if err := s.referenceSchemas(ctx, p, id, table.Columns); err != nil {
		return err
	}
	// Ledger/import undo can leave entry/account orphans; restoring visibility preserves them.
	linked := table
	linked.Columns = nil
	for _, column := range table.Columns {
		if column.Type == "ref" {
			linked.Columns = append(linked.Columns, column)
		}
	}
	if len(linked.Columns) == 0 {
		return nil
	}
	cursor, err := s.repo.db.Collection("rows").Find(ctx, bson.M{"user_id": p.UserID, "table_id": id, "archived": bson.M{"$ne": true}})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var row Row
		if err = cursor.Decode(&row); err != nil {
			return err
		}
		issues, err := s.validateReferences(ctx, p, BatchState{Tables: map[string][]Row{id: {row}}}, map[string]Table{id: linked})
		if err != nil {
			return err
		}
		if len(issues) > 0 {
			return fmt.Errorf("%s", batchMessage(issues))
		}
	}
	return cursor.Err()
}

// BrokenReferences reports orphans without changing rows after a ledger/import undo.
func (s *Service) BrokenReferences(ctx context.Context, p principal.Principal) ([]Violation, error) {
	if err := p.Require("ledger:read"); err != nil {
		return nil, err
	}
	definitions, err := s.allTables(ctx, p)
	if err != nil {
		return nil, err
	}
	issues := []Violation{}
	for _, definition := range definitions {
		linked := false
		for _, column := range definition.Columns {
			if column.Type == "ref" || column.Type == "entry" || column.Type == "account" {
				linked = true
			}
		}
		if !linked {
			continue
		}
		cursor, err := s.repo.db.Collection("rows").Find(ctx, bson.M{"user_id": p.UserID, "table_id": definition.ID, "archived": bson.M{"$ne": true}})
		if err != nil {
			return issues, err
		}
		for cursor.Next(ctx) {
			var row Row
			if err = cursor.Decode(&row); err != nil {
				break
			}
			var found []Violation
			found, err = s.validateReferences(ctx, p, BatchState{Tables: map[string][]Row{definition.ID: {row}}}, map[string]Table{definition.ID: definition})
			if err != nil {
				break
			}
			issues = append(issues, found...)
		}
		cursorErr := cursor.Err()
		_ = cursor.Close(ctx)
		if err != nil {
			return issues, err
		}
		if cursorErr != nil {
			return issues, cursorErr
		}
	}
	return issues, nil
}
