package tables

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
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
	cursor, err := s.repo.db.Query(ctx, `SELECT COALESCE(cells->>@column,'') FROM rows WHERE user_id=@user AND table_id=@table AND NOT archived`, pgx.NamedArgs{"column": column, "user": p.UserID, "table": db})
	if err != nil {
		return nil, err
	}
	defer cursor.Close()
	ids := []string{}
	seen := map[string]bool{}
	for cursor.Next() {
		var id string
		if err := cursor.Scan(&id); err != nil {
			return nil, err
		}
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
		cursor, err := s.repo.db.Query(ctx, `SELECT id FROM rows WHERE user_id=@user AND table_id=@table AND NOT archived AND cells->>@column=@entry ORDER BY id`, pgx.NamedArgs{"user": p.UserID, "table": t.ID, "column": column, "entry": entry})
		if err != nil {
			return nil, err
		}
		explanation := Explanation{DB: t.ID, Rows: []string{}}
		for cursor.Next() {
			var id string
			if err = cursor.Scan(&id); err != nil {
				break
			}
			explanation.Rows = append(explanation.Rows, id)
		}
		ce := cursor.Err()
		cursor.Close()
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
