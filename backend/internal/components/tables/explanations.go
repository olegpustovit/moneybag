package tables

import (
	"context"
	"fmt"
	"sort"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type Explanation struct {
	DB   string   `json:"db"`
	Rows []string `json:"rows"`
}

func entryColumn(t Table) string {
	if t.Ledger != nil {
		return t.Ledger.Entry
	}
	for _, c := range t.Columns {
		if c.Type == "entry" {
			return c.ID
		}
	}
	return ""
}
func (s *Service) ExplainedEntryIDs(ctx context.Context, p principal.Principal, db string) ([]string, error) {
	if err := p.Require("ledger:read"); err != nil {
		return nil, err
	}
	t, err := s.Get(ctx, p, db)
	if err != nil {
		return nil, err
	}
	column := entryColumn(t)
	if column == "" {
		return nil, fmt.Errorf("table %s has no entry column", db)
	}
	cursor, err := s.repo.db.Collection("rows").Find(ctx, bson.M{"user_id": p.UserID, "table_id": db, "archived": bson.M{"$ne": true}}, options.Find().SetProjection(bson.M{"cells." + column: 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	ids := []string{}
	seen := map[string]bool{}
	for cursor.Next(ctx) {
		var row Row
		if err := cursor.Decode(&row); err != nil {
			return nil, err
		}
		id, _ := row.Cells[column].(string)
		if id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
		if len(ids) > 250000 {
			return nil, fmt.Errorf("explanation filter exceeds 250000 entry ids")
		}
	}
	sort.Strings(ids)
	return ids, cursor.Err()
}
func (s *Service) Explanations(ctx context.Context, p principal.Principal, entry string) ([]Explanation, error) {
	tables, err := s.List(ctx, p)
	if err != nil {
		return nil, err
	}
	out := []Explanation{}
	for _, t := range tables {
		column := entryColumn(t)
		if column == "" {
			continue
		}
		cursor, err := s.repo.db.Collection("rows").Find(ctx, bson.M{"user_id": p.UserID, "table_id": t.ID, "archived": bson.M{"$ne": true}, "cells." + column: entry}, options.Find().SetProjection(bson.M{"id": 1}).SetSort(bson.D{{Key: "id", Value: 1}}))
		if err != nil {
			return nil, err
		}
		explanation := Explanation{DB: t.ID, Rows: []string{}}
		for cursor.Next(ctx) {
			var row Row
			if err = cursor.Decode(&row); err != nil {
				break
			}
			explanation.Rows = append(explanation.Rows, row.ID)
		}
		ce := cursor.Err()
		_ = cursor.Close(ctx)
		if err != nil {
			return nil, err
		}
		if ce != nil {
			return nil, ce
		}
		if len(explanation.Rows) > 0 {
			out = append(out, explanation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DB < out[j].DB })
	return out, nil
}
