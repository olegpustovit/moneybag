package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func testTableReferences(t *testing.T, ctx context.Context, pa, pb principal.Principal, s mcpserver.Services, db *mongo.Database) {
	t.Helper()
	t.Run("table references", func(t *testing.T) {
		own, err := s.Ledger.Entries(ctx, pa, ledger.EntriesInput{Limit: 1})
		if err != nil || len(own.Items) == 0 {
			t.Fatal("owner entry fixture", err)
		}
		foreign, err := s.Ledger.Entries(ctx, pb, ledger.EntriesInput{Limit: 1})
		if err != nil || len(foreign.Items) == 0 {
			t.Fatal("foreign entry fixture", err)
		}
		if _, err = s.Ledger.CreateAccount(ctx, pb, ledger.Account{ID: "assets:reference_foreign", Type: "asset", Name: "Other owner's account", Commodities: []string{"EUR"}}); err != nil {
			t.Fatal(err)
		}
		for _, p := range []principal.Principal{pa, pb} {
			if _, err = s.Tables.CreateNamed(ctx, p, "ref-target", tables.CreateInput{Name: "Targets", Columns: []tables.Column{{ID: "label", Type: "string"}}}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = s.Tables.CreateNamed(ctx, pa, "ref-child", tables.CreateInput{Name: "Linked facts", Columns: []tables.Column{
			{ID: "target", Type: "ref", Table: "ref-target", Required: true}, {ID: "entry", Type: "entry", Required: true}, {ID: "account", Type: "account", Required: true},
		}}); err != nil {
			t.Fatal(err)
		}
		client := mcpSession(t, ctx, pa, s, false)
		batch := func(input tables.BatchInput) tables.BatchResult {
			t.Helper()
			response, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "db_batch", Arguments: input})
			if err != nil || response.IsError {
				t.Fatalf("batch: %v %v", response, err)
			}
			encoded, err := json.Marshal(response.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var result tables.BatchResult
			if err = json.Unmarshal(encoded, &result); err != nil {
				t.Fatal(err)
			}
			return result
		}
		cells := map[string]any{"target": "old", "entry": own.Items[0].ID, "account": "expenses:food"}
		result := batch(tables.BatchInput{Ops: []tables.BatchOp{{DB: "ref-child", Upsert: &tables.RowUpsert{RowID: "one", Cells: cells}}, {DB: "ref-target", Upsert: &tables.RowUpsert{RowID: "old", Cells: map[string]any{"label": "old"}}}}})
		if len(result.Violations) > 0 || result.RowsWritten != 2 {
			t.Fatalf("final-state references rejected: %#v", result)
		}
		joined, queryErr := s.Tables.Query(ctx, pa, tables.QueryInput{DB: "ref-child", JoinEntry: true, Expand: []string{"target"}, Where: "row.entry == '" + own.Items[0].ID + "' && row.target.label == 'old'"})
		if queryErr != nil || len(joined.Rows) != 1 || joined.Rows[0].Entry == nil || joined.Rows[0].Entry.ID != own.Items[0].ID {
			t.Fatalf("joined query: %#v %v", joined, queryErr)
		}
		rawJoined, encodeErr := json.Marshal(joined)
		if encodeErr != nil || !strings.Contains(string(rawJoined), `"label":"old"`) {
			t.Fatalf("expanded JSON: %s %v", rawJoined, encodeErr)
		}

		for _, conflict := range []bool{false, true} {
			id := "entry-alias"
			columns := []tables.Column{{ID: "ledger_id", Type: "entry"}}
			updates := []tables.CellUpdate{{RowID: "one", Column: "ledger_id", Value: own.Items[0].ID}}
			if conflict {
				id += "-conflict"
				columns = append(columns, tables.Column{ID: "entry", Type: "string"})
				updates = append(updates, tables.CellUpdate{RowID: "one", Column: "entry", Value: "existing text"})
			}
			if _, err := s.Tables.CreateNamed(ctx, pa, id, tables.CreateInput{Name: id, Columns: columns}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Tables.Update(ctx, pa, tables.UpdateInput{DB: id, Updates: updates}); err != nil {
				t.Fatal(err)
			}
			query, err := s.Tables.Query(ctx, pa, tables.QueryInput{DB: id, JoinEntry: true, Where: "row.entry=='" + own.Items[0].ID + "'"})
			if conflict {
				if err == nil || !strings.Contains(err.Error(), "conflicts") {
					t.Fatalf("entry alias overwrote stored text: %#v %v", query, err)
				}
			} else if err != nil || len(query.Rows) != 1 || query.Rows[0].Entry == nil {
				t.Fatalf("custom entry column: %#v %v", query, err)
			}
			raw, err := s.Tables.Query(ctx, pa, tables.QueryInput{DB: id})
			if err != nil || len(raw.Rows) != 1 || raw.Rows[0].Cells["ledger_id"] != own.Items[0].ID {
				t.Fatalf("raw custom entry changed: %#v %v", raw, err)
			}
			if !conflict {
				catalog, err := s.Dashboard.Catalog(ctx, pa, s.Tables)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, source := range catalog.Sources {
					if source.Name == "table:"+id {
						found = true
						if source.Fields["ledger_id"] != "string" || source.Fields["entry"] != "map(string, dyn)" {
							t.Fatalf("custom entry catalog mismatches source: %#v", source.Fields)
						}
					}
				}
				if !found {
					t.Fatal("custom entry source missing from catalog")
				}
			}
			if conflict && raw.Rows[0].Cells["entry"] != "existing text" {
				t.Fatal("stored entry column changed")
			}
		}
		if _, err = s.Tables.DeleteRows(ctx, pa, tables.DeleteInput{DB: "ref-target", RowIDs: []string{"old"}}); err == nil || !strings.Contains(err.Error(), "ref-child") {
			t.Fatalf("referenced deletion accepted: %v", err)
		}
		if err = s.Tables.Archive(ctx, pa, "ref-target", true, "test"); err == nil {
			t.Fatal("referenced table drop accepted")
		}
		for column, value := range map[string]any{"entry": foreign.Items[0].ID, "account": "assets:reference_foreign", "target": "foreign-only"} {
			if column == "target" {
				if _, err = s.Tables.Update(ctx, pb, tables.UpdateInput{DB: "ref-target", Updates: []tables.CellUpdate{{RowID: "foreign-only", Column: "label", Value: "other"}}}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.Tables.Update(ctx, pa, tables.UpdateInput{DB: "ref-child", Updates: []tables.CellUpdate{{RowID: "one", Column: column, Value: value}}}); err == nil {
				t.Fatalf("cross-owner %s accepted", column)
			}
		}

		merged := batch(tables.BatchInput{Ops: []tables.BatchOp{{DB: "ref-target", Upsert: &tables.RowUpsert{RowID: "keep", Cells: map[string]any{"label": "keep"}}}, {DB: "ref-child", Upsert: &tables.RowUpsert{RowID: "one", Cells: map[string]any{"target": "keep"}}}, {DB: "ref-target", DeleteWhere: "row.label=='old'"}}})
		if len(merged.Violations) > 0 || merged.RowsWritten != 3 {
			t.Fatalf("merge refused: %#v", merged)
		}
		response, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "undo", Arguments: map[string]any{"event_id": merged.EventID, "reason": "test"}})
		if err != nil || response.IsError {
			t.Fatalf("merge undo: %v %v", response, err)
		}

		if err = s.Tables.Archive(ctx, pa, "ref-child", true, "test"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Tables.DeleteRows(ctx, pa, tables.DeleteInput{DB: "ref-target", RowIDs: []string{"old"}}); err != nil {
			t.Fatal(err)
		}
		if err = s.Tables.Archive(ctx, pa, "ref-child", false, "test"); err == nil {
			t.Fatal("table restore introduced a dangling ref")
		}
		if _, err = s.Tables.Update(ctx, pa, tables.UpdateInput{DB: "ref-target", Updates: []tables.CellUpdate{{RowID: "old", Column: "label", Value: "old"}}}); err != nil {
			t.Fatal(err)
		}
		if err = s.Tables.Archive(ctx, pa, "ref-child", false, "test"); err != nil {
			t.Fatal(err)
		}
		orphan := tables.Row{Key: pa.UserID + ":ref-child:orphan", UserID: pa.UserID, TableID: "ref-child", ID: "orphan", Version: 1, Cells: map[string]any{"entry": "missing-entry", "account": "assets:missing_account", "target": "missing-row"}}
		if _, err = db.Collection("rows").InsertOne(ctx, orphan); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.Collection("rows").DeleteOne(context.Background(), bson.M{"_id": orphan.Key, "user_id": pa.UserID})
		})
		broken, err := s.Tables.BrokenReferences(ctx, pa)
		if err != nil || len(broken) != 3 {
			t.Fatalf("broken references: %#v %v", broken, err)
		}
		report, err := s.Consistency.Run(ctx, pa)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, check := range report.Checks {
			if check.ID == "table_references" {
				found = true
				if check.OK {
					t.Fatal("consistency accepts orphan references")
				}
			}
		}
		if !found {
			t.Fatal("missing table reference consistency check")
		}
	})
}
