package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func testLargeTableQuery(t *testing.T, ctx context.Context, p principal.Principal, s mcpserver.Services, db *mongo.Database) {
	t.Helper()
	t.Run("indexed table query beyond 5000 rows", func(t *testing.T) {
		_, err := s.Tables.CreateNamed(ctx, p, "query-large", tables.CreateInput{Name: "Synthetic query boundary", Columns: []tables.Column{{ID: "token", Type: "string"}}, Keys: [][]string{{"token"}}})
		if err != nil {
			t.Fatal(err)
		}
		documents := make([]any, 5102)
		for i := range documents {
			id := fmt.Sprintf("%05d", i)
			documents[i] = tables.Row{Key: p.UserID + ":query-large:" + id, UserID: p.UserID, TableID: "query-large", ID: id, Version: 1, Cells: map[string]any{"token": id}}
		}
		if _, err := db.Collection("rows").InsertMany(ctx, documents); err != nil {
			t.Fatal(err)
		}
		filtered, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: "query-large", Where: `row.token in ['05100','05101']`})
		if err != nil || len(filtered.Rows) != 2 || filtered.Truncated {
			t.Fatalf("indexed filter: %#v %v", filtered, err)
		}
		first, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: "query-large"})
		if err != nil || len(first.Rows) != 5000 || !first.Truncated || first.NextCursor != "04999" {
			t.Fatalf("first page: rows=%d cursor=%s truncated=%v %v", len(first.Rows), first.NextCursor, first.Truncated, err)
		}
		last, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: "query-large", Cursor: first.NextCursor})
		if err != nil || len(last.Rows) != 102 || last.Truncated {
			t.Fatalf("last page: rows=%d %v", len(last.Rows), err)
		}
		rows, err := s.Tables.SourceRows(ctx, p, "query-large")
		if err != nil || len(rows) != 5102 {
			t.Fatalf("analytic source silently truncated: rows=%d %v", len(rows), err)
		}
		if _, err := s.Tables.CreateNamed(ctx, p, "query-large-target", tables.CreateInput{Name: "Large expansion target", Columns: []tables.Column{{ID: "payload", Type: "json"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Tables.Update(ctx, p, tables.UpdateInput{DB: "query-large-target", Updates: []tables.CellUpdate{{RowID: "one", Column: "payload", Value: map[string]any{"text": strings.Repeat("x", 15000)}}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Tables.CreateNamed(ctx, p, "query-expanded", tables.CreateInput{Name: "Bounded expansion", Columns: []tables.Column{{ID: "target", Type: "ref", Table: "query-large-target"}}}); err != nil {
			t.Fatal(err)
		}
		expanded := make([]any, 600)
		for i := range expanded {
			id := fmt.Sprintf("%05d", i)
			expanded[i] = tables.Row{Key: p.UserID + ":query-expanded:" + id, UserID: p.UserID, TableID: "query-expanded", ID: id, Version: 1, Cells: map[string]any{"target": "one"}}
		}
		if _, err := db.Collection("rows").InsertMany(ctx, expanded); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: "query-expanded", Expand: []string{"target"}}); err == nil || !strings.Contains(err.Error(), "output budget") {
			t.Fatalf("unbounded joined output: %v", err)
		}
		small, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: "query-expanded", Expand: []string{"target"}, Limit: 1})
		if err != nil || len(small.Rows) != 1 || !small.Truncated {
			t.Fatalf("bounded expansion page: rows=%d %v", len(small.Rows), err)
		}

		deleted, err := s.Tables.DeleteRows(ctx, p, tables.DeleteInput{DB: "query-large", Where: `row.token == '05101'`})
		if err != nil || len(deleted.Rows) != 1 || deleted.Rows[0].ID != "05101" {
			t.Fatalf("delete_where ignored rows beyond 5000: %#v %v", deleted, err)
		}
		if _, err := s.Tables.DeleteRows(ctx, p, tables.DeleteInput{DB: "query-large", Where: "true"}); err == nil {
			t.Fatal("oversized deletion silently truncated")
		}

	})
}
