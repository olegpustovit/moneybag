package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func testValidatedTables(t *testing.T, ctx context.Context, p principal.Principal, s mcpserver.Services, db *mongo.Database) {
	t.Helper()
	t.Run("validated table batches", func(t *testing.T) {
		client := mcpSession(t, ctx, p, s, false)
		call := func(name string, args any) (*mcp.CallToolResult, tables.BatchResult) {
			t.Helper()
			response, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil {
				t.Fatal(err)
			}
			var result tables.BatchResult
			if !response.IsError {
				encoded, err := json.Marshal(response.StructuredContent)
				if err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(encoded, &result); err != nil {
					t.Fatal(err)
				}
			}
			return response, result
		}
		for id, columns := range map[string][]tables.Column{
			"batch-a": {{ID: "label", Type: "string", Required: true}, {ID: "amount", Type: "decimal", Required: true}},
			"batch-b": {{ID: "label", Type: "string", Required: true}, {ID: "blocked", Type: "bool"}},
		} {
			if _, err := s.Tables.CreateNamed(ctx, p, id, tables.CreateInput{Name: id, Columns: columns}); err != nil {
				t.Fatal(err)
			}
		}
		ops := []tables.BatchOp{{DB: "batch-a", Upsert: &tables.RowUpsert{RowID: "one", Cells: map[string]any{"label": "a", "amount": "1.20"}}}, {DB: "batch-b", Upsert: &tables.RowUpsert{RowID: "two", Cells: map[string]any{"label": "b"}}}}
		countEvents := func() int64 {
			t.Helper()
			count, err := db.Collection("events").CountDocuments(ctx, bson.M{"user_id": p.UserID})
			if err != nil {
				t.Fatal(err)
			}
			return count
		}
		rows := func(id string) []tables.Row {
			t.Helper()
			result, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: id})
			if err != nil {
				t.Fatal(err)
			}
			return result.Rows
		}
		before := countEvents()
		response, dry := call("db_batch", tables.BatchInput{DryRun: true, Ops: ops})
		if response.IsError || len(dry.Violations) != 0 || dry.RowsWritten != 0 || dry.EventID != "" || len(rows("batch-a")) != 0 || len(rows("batch-b")) != 0 || countEvents() != before {
			t.Fatalf("dry run wrote data or failed: %#v", dry)
		}
		response, committed := call("db_batch", tables.BatchInput{Ops: ops})
		if response.IsError || len(committed.Violations) != 0 || committed.RowsWritten != 2 || committed.EventID == "" || countEvents() != before+1 {
			t.Fatalf("batch not one audited write: %#v", committed)
		}
		response, invalid := call("db_batch", tables.BatchInput{Ops: []tables.BatchOp{
			{DB: "batch-a", Upsert: &tables.RowUpsert{RowID: "bad-a", Cells: map[string]any{"amount": "not money"}}},
			{DB: "batch-b", Upsert: &tables.RowUpsert{RowID: "bad-b", Cells: map[string]any{"unknown": "x"}}},
		}})
		if response.IsError || len(invalid.Violations) < 4 || invalid.RowsWritten != 0 || len(rows("batch-a")) != 1 || len(rows("batch-b")) != 1 {
			t.Fatalf("validation was partial: %#v", invalid)
		}
		response, _ = call("undo", map[string]any{"event_id": committed.EventID, "reason": "test"})
		if response.IsError || len(rows("batch-a")) != 0 || len(rows("batch-b")) != 0 {
			t.Fatal("one undo did not reverse both tables")
		}
		response, recreated := call("db_batch", tables.BatchInput{Ops: ops})
		if response.IsError || recreated.RowsWritten != 2 {
			t.Fatal("cannot recreate archived rows")
		}
		response, deleted := call("db_batch", tables.BatchInput{Ops: []tables.BatchOp{{DB: "batch-a", DeleteWhere: "row.label=='a'"}, {DB: "batch-b", DeleteWhere: "row.label=='b'"}}})
		if response.IsError || deleted.RowsWritten != 2 || len(rows("batch-a")) != 0 || len(rows("batch-b")) != 0 {
			t.Fatal("batch delete_where did not archive both tables")
		}
		response, _ = call("undo", map[string]any{"event_id": deleted.EventID, "reason": "test"})
		if response.IsError || len(rows("batch-a")) != 1 || len(rows("batch-b")) != 1 {
			t.Fatal("batch deletion undo did not restore both tables")
		}
		if _, err := s.Tables.Update(ctx, p, tables.UpdateInput{DB: "batch-a", Updates: []tables.CellUpdate{{RowID: "one", Column: "amount", Value: "3"}}}); err != nil {
			t.Fatal(err)
		}
		response, _ = call("undo", map[string]any{"event_id": recreated.EventID, "reason": "test"})
		if !response.IsError || len(rows("batch-a")) != 1 || len(rows("batch-b")) != 1 {
			t.Fatal("batch undo overwrote a later edit")
		}
		priorVersion := rows("batch-a")[0].Version
		if err := db.RunCommand(ctx, bson.D{{Key: "collMod", Value: "rows"}, {Key: "validator", Value: bson.M{"cells.blocked": bson.M{"$ne": true}}}}).Err(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = db.RunCommand(context.Background(), bson.D{{Key: "collMod", Value: "rows"}, {Key: "validator", Value: bson.M{}}}).Err()
		})
		response, _ = call("db_batch", tables.BatchInput{Ops: []tables.BatchOp{
			{DB: "batch-a", Upsert: &tables.RowUpsert{RowID: "one", Cells: map[string]any{"amount": "2.00"}}},
			{DB: "batch-b", Upsert: &tables.RowUpsert{RowID: "reject", Cells: map[string]any{"label": "reject", "blocked": true}}},
		}})
		if !response.IsError {
			t.Fatal("storage rejection accepted")
		}
		rawError, _ := json.Marshal(response.Content)
		if !strings.Contains(string(rawError), "after 1 of 2 rows") || !strings.Contains(string(rawError), "Batch event") {
			t.Fatalf("partial-write recovery details missing: %s", rawError)
		}
		if err := db.RunCommand(ctx, bson.D{{Key: "collMod", Value: "rows"}, {Key: "validator", Value: bson.M{}}}).Err(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(rawError), "Document failed validation") || strings.Contains(string(rawError), "moneybag_test_") {
			t.Fatalf("storage internals leaked in recovery message: %s", rawError)
		}
		encoded, err := json.Marshal(rows("batch-a")[0].Cells["amount"])
		if err != nil || string(encoded) != `"2.00"` || rows("batch-a")[0].Version != priorVersion+1 || len(rows("batch-b")) != 1 {
			t.Fatalf("partial write was hidden or earlier rows were rolled back: %s %v", encoded, err)
		}

	})
}
