package integration

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func testTableConstraints(t *testing.T, ctx context.Context, p principal.Principal, s mcpserver.Services, db *mongo.Database) {
	t.Helper()
	t.Run("table constraints", func(t *testing.T) {
		if _, err := s.Tables.CreateNamed(ctx, p, "row-checks", tables.CreateInput{Name: "Checks", Columns: []tables.Column{{ID: "ok", Type: "bool"}}, Checks: []tables.RowCheck{{Expr: "row.ok", Message: "Must be true"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Tables.Update(ctx, p, tables.UpdateInput{DB: "row-checks", Updates: []tables.CellUpdate{{RowID: "one", Column: "ok", Value: false}}}); err == nil {
			t.Fatal("error check accepted false")
		}
		if _, err := s.Tables.Update(ctx, p, tables.UpdateInput{DB: "row-checks", Updates: []tables.CellUpdate{{RowID: "one", Column: "ok", Value: true}}}); err != nil {
			t.Fatal(err)
		}
		client := mcpSession(t, ctx, p, s, false)
		batch := func(input tables.BatchInput) tables.BatchResult {
			t.Helper()
			response, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "db_batch", Arguments: input})
			if err != nil || response.IsError {
				t.Fatalf("batch tool: %v %v", response, err)
			}
			encoded, err := json.Marshal(response.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var out tables.BatchResult
			if err = json.Unmarshal(encoded, &out); err != nil {
				t.Fatal(err)
			}
			return out
		}
		if _, err := s.Tables.CreateNamed(ctx, p, "unique-codes", tables.CreateInput{Name: "Codes", Columns: []tables.Column{{ID: "code", Type: "string", Required: true}}, Keys: [][]string{{"code"}}}); err != nil {
			t.Fatal(err)
		}
		created := batch(tables.BatchInput{Ops: []tables.BatchOp{{DB: "unique-codes", Upsert: &tables.RowUpsert{RowID: "a", Cells: map[string]any{"code": "A"}}}, {DB: "unique-codes", Upsert: &tables.RowUpsert{RowID: "b", Cells: map[string]any{"code": "B"}}}}})
		if created.RowsWritten != 2 || len(created.Violations) != 0 {
			t.Fatalf("keyed insert: %#v", created)
		}
		swapped := batch(tables.BatchInput{Ops: []tables.BatchOp{{DB: "unique-codes", Upsert: &tables.RowUpsert{RowID: "a", Cells: map[string]any{"code": "B"}}}, {DB: "unique-codes", Upsert: &tables.RowUpsert{RowID: "b", Cells: map[string]any{"code": "A"}}}}})
		if swapped.RowsWritten != 0 || len(swapped.Violations) == 0 {
			t.Fatalf("key swap was not refused before writes: %#v", swapped)
		}
		duplicates := batch(tables.BatchInput{Ops: []tables.BatchOp{{DB: "unique-codes", Upsert: &tables.RowUpsert{RowID: "duplicate", Cells: map[string]any{"code": "A"}}}}})
		if duplicates.RowsWritten != 0 || len(duplicates.Violations) == 0 {
			t.Fatal("duplicate key accepted")
		}
		rows, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: "unique-codes"})
		if err != nil {
			t.Fatal(err)
		}
		raw := rows.Rows[0]
		raw.Key = p.UserID + ":unique-codes:raw-duplicate"
		raw.ID = "raw-duplicate"
		if _, err = db.Collection("rows").InsertOne(ctx, raw); !mongo.IsDuplicateKeyError(err) {
			t.Fatalf("MongoDB unique index did not enforce the key: %v", err)
		}
		var workers sync.WaitGroup
		failures := make(chan error, 2)
		for range 2 {
			workers.Go(func() {
				_, err := s.Tables.Update(ctx, p, tables.UpdateInput{DB: "unique-codes", Updates: []tables.CellUpdate{{RowID: "natural", Column: "code", Value: "same-store"}}})
				failures <- err
			})
		}
		workers.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		rows, err = s.Tables.Query(ctx, p, tables.QueryInput{DB: "unique-codes", Where: "row.code=='same-store'"})
		if err != nil || len(rows.Rows) != 1 {
			t.Fatalf("concurrent natural row upserts: %v %v", rows, err)
		}
		badChecks := []tables.RowCheck{{Expr: "false", Message: "Existing rows do not pass"}}
		if _, err = s.Tables.Alter(ctx, p, tables.AlterInput{DB: "unique-codes", Checks: &badChecks}); err == nil {
			t.Fatal("invalidating check accepted")
		}
		validChecks := []tables.RowCheck{{Expr: "row.code!=''", Message: "Code required"}}
		if _, err = s.Tables.Alter(ctx, p, tables.AlterInput{DB: "unique-codes", Checks: &validChecks}); err != nil {
			t.Fatal(err)
		}

		emptyKeys := [][]string{}
		if _, err = s.Tables.Alter(ctx, p, tables.AlterInput{DB: "unique-codes", Keys: &emptyKeys}); err != nil {
			t.Fatal(err)
		}
		events, err := s.Audit.List(ctx, p, audit.ListInput{Target: "unique-codes", Limit: 1})
		if err != nil || len(events.Events) != 1 {
			t.Fatal("schema event", err)
		}
		undoSchema, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "undo", Arguments: map[string]any{"event_id": events.Events[0].ID, "reason": "test"}})
		if err != nil || undoSchema.IsError {
			t.Fatalf("schema constraint undo failed: %v %v", undoSchema, err)
		}
		restoredSchema, err := s.Tables.Get(ctx, p, "unique-codes")
		if err != nil || len(restoredSchema.Keys) != 1 || len(restoredSchema.Checks) != 1 {
			t.Fatal("schema metadata was not restored", restoredSchema, err)
		}
		if _, err = s.Tables.CreateNamed(ctx, p, "unkeyed-codes", tables.CreateInput{Name: "Existing duplicates", Columns: []tables.Column{{ID: "code", Type: "string"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Tables.Update(ctx, p, tables.UpdateInput{DB: "unkeyed-codes", Updates: []tables.CellUpdate{{RowID: "a", Column: "code", Value: "same"}, {RowID: "b", Column: "code", Value: "same"}}}); err != nil {
			t.Fatal(err)
		}
		keys := [][]string{{"code"}}
		if _, err = s.Tables.Alter(ctx, p, tables.AlterInput{DB: "unkeyed-codes", Keys: &keys}); err == nil {
			t.Fatal("key accepted over existing duplicates")
		}
		schema, err := s.Tables.Get(ctx, p, "unkeyed-codes")
		if err != nil || len(schema.Keys) != 0 {
			t.Fatal("failed key migration changed schema", schema, err)
		}
		entry, err := s.Ledger.Post(ctx, p, ledger.EntryInput{Date: "2026-11-04", Payee: "Synthetic receipt", Commit: true, Postings: []ledger.Posting{{Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal("22.62"), Commodity: "EUR"}}, {Account: "assets:bank", Amount: ledger.Amount{Value: ledger.MustDecimal("-22.62"), Commodity: "EUR"}}}})
		if err != nil {
			t.Fatal(err)
		}
		constraint := &tables.LedgerConstraint{Entry: "entry", Amount: "amount", Accounts: []string{"expenses:"}}
		if _, err = s.Tables.CreateNamed(ctx, p, "constraint-items", tables.CreateInput{Name: "Items", Columns: []tables.Column{{ID: "entry", Type: "entry", Required: true}, {ID: "line", Type: "number", Required: true}, {ID: "amount", Type: "decimal", Required: true}, {ID: "qty", Type: "decimal"}, {ID: "unit_price", Type: "decimal"}}, Keys: [][]string{{"entry", "line"}}, Ledger: constraint, Checks: []tables.RowCheck{{Expr: "row.qty==null || row.unit_price==null || row.qty*row.unit_price==row.amount", Message: "Quantity times unit price differs from amount", Level: "warn"}}}); err != nil {
			t.Fatal(err)
		}
		ops := []tables.BatchOp{{DB: "constraint-items", Upsert: &tables.RowUpsert{RowID: "one", Cells: map[string]any{"entry": entry.Entry.ID, "line": float64(1), "amount": "10"}}}, {DB: "constraint-items", Upsert: &tables.RowUpsert{RowID: "two", Cells: map[string]any{"entry": entry.Entry.ID, "line": float64(2), "amount": "12.52", "qty": "1", "unit_price": "12.52"}}}}
		invalid := batch(tables.BatchInput{Ops: ops})
		if invalid.RowsWritten != 0 || len(invalid.Violations) != 1 || !strings.Contains(invalid.Violations[0].Message, "0.10 EUR") {
			t.Fatalf("ledger sum accepted or difference missing: %#v", invalid)
		}
		ops[1].Upsert.Cells["amount"] = "12.62"
		valid := batch(tables.BatchInput{Ops: ops})
		if valid.RowsWritten != 2 || len(valid.Violations) != 0 || len(valid.Warnings) != 1 {
			t.Fatalf("valid receipt/warning rejected: %#v", valid)
		}
		response, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "undo", Arguments: map[string]any{"event_id": valid.EventID, "reason": "test"}})
		if err != nil || response.IsError {
			t.Fatalf("cannot undo whole explanation: %v %v", response, err)
		}
		valid = batch(tables.BatchInput{Ops: ops})
		if valid.RowsWritten != 2 {
			t.Fatal(valid)
		}

		if _, err = s.Tables.CreateNamed(ctx, p, "unconstrained-items", tables.CreateInput{Name: "Unconstrained", Columns: []tables.Column{{ID: "entry", Type: "entry"}, {ID: "amount", Type: "decimal"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Tables.Update(ctx, p, tables.UpdateInput{DB: "unconstrained-items", Updates: []tables.CellUpdate{{RowID: "one", Column: "entry", Value: entry.Entry.ID}, {RowID: "one", Column: "amount", Value: "1"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Tables.Alter(ctx, p, tables.AlterInput{DB: "unconstrained-items", Ledger: constraint}); err == nil {
			t.Fatal("ledger constraint accepted existing incomplete explanation")
		}
		rejectedSchema, err := s.Tables.Get(ctx, p, "unconstrained-items")
		if err != nil || rejectedSchema.Ledger != nil {
			t.Fatal("failed ledger migration changed schema", err)
		}
		for _, account := range []ledger.Account{{ID: "assets:constraint_usd", Type: "asset", Name: "Synthetic USD bank", Commodities: []string{"USD"}}, {ID: "expenses:constraint_usd", Type: "expense", Name: "Synthetic USD expense", Commodities: []string{"USD"}}} {
			if _, err = s.Ledger.CreateAccount(ctx, p, account); err != nil {
				t.Fatal(err)
			}
		}
		mixed, err := s.Ledger.Post(ctx, p, ledger.EntryInput{Date: "2026-10-01", Payee: "Synthetic mixed receipt", Commit: true, Postings: []ledger.Posting{{Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal("10"), Commodity: "EUR"}}, {Account: "assets:bank", Amount: ledger.Amount{Value: ledger.MustDecimal("-10"), Commodity: "EUR"}}, {Account: "expenses:constraint_usd", Amount: ledger.Amount{Value: ledger.MustDecimal("20"), Commodity: "USD"}}, {Account: "assets:constraint_usd", Amount: ledger.Amount{Value: ledger.MustDecimal("-20"), Commodity: "USD"}}}})
		if err != nil {
			t.Fatal(err)
		}
		mixedResult := batch(tables.BatchInput{Ops: []tables.BatchOp{{DB: "constraint-items", Upsert: &tables.RowUpsert{RowID: "mixed", Cells: map[string]any{"entry": mixed.Entry.ID, "line": float64(1), "amount": "30"}}}}})
		if mixedResult.RowsWritten != 0 || len(mixedResult.Violations) == 0 || !strings.Contains(mixedResult.Violations[0].Message, "exactly one commodity") {
			t.Fatal("summed mixed commodities", mixedResult)
		}
		if _, err = s.Ledger.Reverse(ctx, p, ledger.ReverseInput{ID: mixed.Entry.ID, Reason: "test"}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Ledger.Reverse(ctx, p, ledger.ReverseInput{ID: entry.Entry.ID, Reason: "test"}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Tables.Update(ctx, p, tables.UpdateInput{DB: "constraint-items", Updates: []tables.CellUpdate{{RowID: "one", Column: "amount", Value: "999"}}}); err != nil {
			t.Fatalf("reversed entry sum was enforced: %v", err)
		}
		partialEntry, err := s.Ledger.Post(ctx, p, ledger.EntryInput{Date: "2026-11-04", Payee: "Interrupted metadata fixture", Commit: true, Postings: []ledger.Posting{{Account: "expenses:food", Amount: ledger.Amount{Value: ledger.MustDecimal("12.50"), Commodity: "EUR"}}, {Account: "assets:bank", Amount: ledger.Amount{Value: ledger.MustDecimal("-12.50"), Commodity: "EUR"}}}})
		if err != nil {
			t.Fatal(err)
		}
		ledgerBefore, err := s.Ledger.Get(ctx, p, partialEntry.Entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.RunCommand(ctx, bson.D{{Key: "collMod", Value: "rows"}, {Key: "validator", Value: bson.M{"id": bson.M{"$ne": "partial-two"}}}}).Err(); err != nil {
			t.Fatal(err)
		}
		partial, writeErr := s.Tables.Batch(ctx, p, tables.BatchInput{Ops: []tables.BatchOp{
			{DB: "constraint-items", Upsert: &tables.RowUpsert{RowID: "partial-one", Cells: map[string]any{"entry": partialEntry.Entry.ID, "line": float64(1), "amount": "10"}}},
			{DB: "constraint-items", Upsert: &tables.RowUpsert{RowID: "partial-two", Cells: map[string]any{"entry": partialEntry.Entry.ID, "line": float64(2), "amount": "2.50"}}},
		}})
		if err := db.RunCommand(ctx, bson.D{{Key: "collMod", Value: "rows"}, {Key: "validator", Value: bson.M{}}}).Err(); err != nil {
			t.Fatal(err)
		}
		if writeErr == nil || partial.RowsWritten != 1 || !partial.Partial || partial.EventID == "" {
			t.Fatalf("partial metadata write not reported: %#v %v", partial, writeErr)
		}
		found, err := s.Tables.ConstraintIssues(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		reported := false
		for _, issue := range found {
			if issue.DB == "constraint-items" && strings.Contains(issue.Message, partialEntry.Entry.ID) && strings.Contains(issue.Message, "2.50 EUR") {
				reported = true
			}
		}
		if !reported {
			t.Fatalf("partial receipt sum missing from consistency report: %#v", found)
		}
		rawReport, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "consistency_check", Arguments: map[string]any{}})
		if err != nil || rawReport.IsError {
			t.Fatalf("consistency tool: %v %v", rawReport, err)
		}
		encoded, _ := json.Marshal(rawReport.StructuredContent)
		if !strings.Contains(string(encoded), "table_constraints") || !strings.Contains(string(encoded), partialEntry.Entry.ID) {
			t.Fatalf("partial receipt not exposed by consistency tool: %s", encoded)
		}
		repaired := batch(tables.BatchInput{Ops: []tables.BatchOp{{DB: "constraint-items", Upsert: &tables.RowUpsert{RowID: "partial-two", Cells: map[string]any{"entry": partialEntry.Entry.ID, "line": float64(2), "amount": "2.50"}}}}})
		if repaired.RowsWritten != 1 {
			t.Fatal(repaired)
		}
		ledgerAfter, err := s.Ledger.Get(ctx, p, partialEntry.Entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		beforeJSON, _ := json.Marshal(ledgerBefore)
		afterJSON, _ := json.Marshal(ledgerAfter)
		if string(beforeJSON) != string(afterJSON) {
			t.Fatal("receipt metadata changed the ledger")
		}

	})
}
