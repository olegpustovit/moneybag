package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// These synthetic receipts exercise country-specific metadata without claiming OCR accuracy.
func testReceiptTemplate(t *testing.T, ctx context.Context, p, collisionOwner principal.Principal, s mcpserver.Services, db *mongo.Database) {
	t.Helper()
	t.Run("receipt template", func(t *testing.T) {
		client := mcpSession(t, ctx, p, s, false)
		call := func(name string, args any) []byte {
			t.Helper()
			result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil || result.IsError {
				t.Fatalf("%s: %v %v", name, result, err)
			}
			raw, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			return raw
		}
		catalog := call("db_templates", map[string]any{})
		if !strings.Contains(string(catalog), "receipt_items") {
			t.Fatal(string(catalog))
		}
		var created tables.CreateResult
		if err := json.Unmarshal(call("db_create", map[string]any{"template": "receipts"}), &created); err != nil || len(created.Tables) != 5 || created.EventID == "" {
			t.Fatalf("create: %#v %v", created, err)
		}
		var empty ledger.EntriesPage
		_ = json.Unmarshal(call("entries_list", map[string]any{"explained_by": "receipt_items"}), &empty)
		if len(empty.Items) != 0 {
			t.Fatal("empty explanation table matched entries")
		}
		if _, err := s.Tables.CreateNamed(ctx, collisionOwner, "stores", tables.CreateInput{Name: "Existing stores", Columns: []tables.Column{{ID: "label", Type: "string"}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Tables.CreateDatabase(ctx, collisionOwner, tables.CreateInput{Template: "receipts"}); err == nil {
			t.Fatal("partial template collision accepted")
		}
		if _, err := s.Tables.Get(ctx, collisionOwner, "merchants"); err == nil {
			t.Fatal("failed template creation left its first table")
		}
		if _, err := db.Collection("tables").DeleteOne(ctx, bson.M{"user_id": collisionOwner.UserID, "id": "stores"}); err != nil {
			t.Fatal(err)
		}
		validator := bson.D{{Key: "collMod", Value: "tables"}, {Key: "validator", Value: bson.M{"$nor": bson.A{bson.M{"user_id": collisionOwner.UserID, "id": "stores"}}}}}
		if err := db.RunCommand(ctx, validator).Err(); err != nil {
			t.Fatal(err)
		}
		_, storageErr := s.Tables.CreateDatabase(ctx, collisionOwner, tables.CreateInput{Template: "receipts"})
		if err := db.RunCommand(ctx, bson.D{{Key: "collMod", Value: "tables"}, {Key: "validator", Value: bson.M{}}}).Err(); err != nil {
			t.Fatal(err)
		}
		if storageErr == nil {
			t.Fatal("template accepted a storage failure")
		}
		if _, err := s.Tables.Get(ctx, collisionOwner, "merchants"); err != nil {
			t.Fatal("saved template table missing after partial failure")
		}
		if _, err := s.Tables.CreateDatabase(ctx, collisionOwner, tables.CreateInput{Template: "receipts"}); err != nil {
			t.Fatalf("cannot resume partial template: %v", err)
		}
		repeated := call("db_create", map[string]any{"template": "receipts"})
		var noChange tables.CreateResult
		_ = json.Unmarshal(repeated, &noChange)
		if noChange.EventID != "" {
			t.Fatal("unchanged template repeat wrote an event")
		}

		for _, account := range []ledger.Account{{ID: "assets:receipt_usd", Name: "Receipt USD bank", Type: "asset", Commodities: []string{"USD"}}, {ID: "expenses:receipt_usd", Name: "Receipt USD expense", Type: "expense", Commodities: []string{"USD"}}} {
			if _, err := s.Ledger.CreateAccount(ctx, p, account); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Rates.Set(ctx, p, rates.SetInput{Pair: "USD/EUR", Value: ledger.MustDecimal("0.8"), At: "2026-11-04T00:00:00Z", Source: "manual"}); err != nil {
			t.Fatal(err)
		}
		entries := []string{}
		for i, country := range []string{"HR", "DE", "US", "HR"} {
			currency, expense, bank := "EUR", "expenses:food", "assets:bank"
			if country == "US" {
				currency, expense, bank = "USD", "expenses:receipt_usd", "assets:receipt_usd"
			}
			entry, err := s.Ledger.Post(ctx, p, ledger.EntryInput{Date: "2026-11-04", Payee: "Synthetic " + country + " receipt", Commit: true, Postings: []ledger.Posting{{Account: expense, Amount: ledger.Amount{Value: ledger.MustDecimal("12.50"), Commodity: currency}}, {Account: bank, Amount: ledger.Amount{Value: ledger.MustDecimal("-12.50"), Commodity: currency}}}})
			if err != nil {
				t.Fatal(err)
			}
			entries = append(entries, entry.Entry.ID)
			merchant, store := country+":synthetic-tax", country+":synthetic-tax:001"
			attrs := map[string]any{"loyalty_savings": "0.50"}
			switch country {
			case "HR":
				attrs["jir"] = "synthetic-jir"
				attrs["zki"] = "synthetic-zki"
			case "DE":
				attrs["tse_signature"] = "synthetic-signature"
				attrs["tse_transaction"] = "42"
			case "US":
				attrs["sales_tax"] = "0.75"
				attrs["tip"] = "2.50"
			}
			kind := "fee"
			if country == "US" {
				kind = "tip"
			}
			ops := []tables.BatchOp{
				{DB: "merchants", Upsert: &tables.RowUpsert{RowID: merchant, Cells: map[string]any{"country": country, "tax_id": "synthetic-tax", "legal_name": "Synthetic " + country}}},
				{DB: "stores", Upsert: &tables.RowUpsert{RowID: store, Cells: map[string]any{"merchant": merchant, "store_code": "001", "label": "Synthetic " + country, "country": country}}},
				{DB: "products", Upsert: &tables.RowUpsert{RowID: "gtin:6291041500213", Cells: map[string]any{"gtin": "6291041500213", "name": "Synthetic goods"}}},
				{DB: "receipts", Upsert: &tables.RowUpsert{RowID: entry.Entry.ID, Cells: map[string]any{"entry": entry.Entry.ID, "store": store, "currency": currency, "total": "12.50", "receipt_ref": fmt.Sprint(i), "source": "manual", "attrs": attrs}}},
				{DB: "receipt_items", Upsert: &tables.RowUpsert{RowID: entry.Entry.ID + ":1", Cells: map[string]any{"entry": entry.Entry.ID, "line": float64(1), "raw_name": "Synthetic goods", "product": "gtin:6291041500213", "qty": "2", "unit_price": "5", "amount": "10", "kind": "item", "category": expense}}},
				{DB: "receipt_items", Upsert: &tables.RowUpsert{RowID: entry.Entry.ID + ":2", Cells: map[string]any{"entry": entry.Entry.ID, "line": float64(2), "raw_name": "Synthetic adjustment", "amount": "2.50", "kind": kind, "category": expense}}},
			}
			var dry tables.BatchResult
			_ = json.Unmarshal(call("db_batch", tables.BatchInput{DryRun: true, Ops: ops}), &dry)
			if len(dry.Violations) != 0 {
				t.Fatal(dry)
			}
			var written tables.BatchResult
			_ = json.Unmarshal(call("db_batch", tables.BatchInput{Ops: ops}), &written)
			if written.RowsWritten != len(ops) || len(written.Violations) > 0 {
				t.Fatal(written)
			}
			joined, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: "receipts", JoinEntry: true, Expand: []string{"store"}, Where: "row.entry=='" + entry.Entry.ID + "'"})
			if err != nil || len(joined.Rows) != 1 || joined.Rows[0].Entry == nil {
				t.Fatalf("joined: %#v %v", joined, err)
			}
			want := "12.5"
			if country == "US" {
				want = "10"
			}
			eur := joined.Rows[0].Entry.ValueEUR
			if eur == nil || eur.Rat().Cmp(ledger.MustDecimal(want).Rat()) != 0 {
				t.Fatalf("fixed EUR value: %#v", joined.Rows[0].Entry)
			}
			details := call("entry_get", map[string]any{"id": entry.Entry.ID})
			if !strings.Contains(string(details), `"db":"receipt_items"`) {
				t.Fatal(string(details))
			}
		}
		stores, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: "stores"})
		if err != nil || len(stores.Rows) != 3 {
			t.Fatalf("store reuse: %#v %v", stores, err)
		}
		for _, filter := range []string{"receipt_items", "!receipt_items"} {
			var page ledger.EntriesPage
			_ = json.Unmarshal(call("entries_list", map[string]any{"explained_by": filter, "limit": 500, "effective": true}), &page)
			seen := map[string]bool{}
			for _, entry := range page.Items {
				seen[entry.ID] = true
			}
			for _, id := range entries {
				if seen[id] != (filter == "receipt_items") {
					t.Fatalf("explanation filter %s missed/exposed %s", filter, id)
				}
			}
		}
		query := map[string]any{"from": "table:receipts", "derive": map[string]string{"store_label": "row.store.label", "weekday": "timestamp(row.entry.date + 'T00:00:00Z').getDayOfWeek()"}, "group": []string{"store_label", "weekday"}, "agg": map[string]string{"total": "sum(row.entry.value_eur)"}}
		call("view_upsert", map[string]any{"id": "receipt-by-store-day", "query": query})
		analytics := call("expr_eval", map[string]any{"expr": "data.rows", "vars": map[string]any{"data": map[string]any{"rows": map[string]any{"from": "view:receipt-by-store-day"}}}})
		if !strings.Contains(string(analytics), "Synthetic HR") {
			t.Fatal(string(analytics))
		}
		if _, err := s.Ledger.Reverse(ctx, p, ledger.ReverseInput{ID: entries[0], Reason: "Synthetic source reversal"}); err != nil {
			t.Fatal(err)
		}
		reversed, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: "receipts", JoinEntry: true, Where: "row.entry=='" + entries[0] + "'"})
		if err != nil || len(reversed.Rows) != 1 || reversed.Rows[0].Entry.Effective {
			t.Fatalf("reversed join: %#v %v", reversed, err)
		}
		orphan := tables.Row{Key: p.UserID + ":receipts:orphan", UserID: p.UserID, TableID: "receipts", ID: "orphan", Version: 1, Cells: map[string]any{"entry": "missing-entry", "store": "HR:synthetic-tax:001", "total": ledger.MustDecimal("1")}}
		if _, err := db.Collection("rows").InsertOne(ctx, orphan); err != nil {
			t.Fatal(err)
		}
		all, err := s.Tables.Query(ctx, p, tables.QueryInput{DB: "receipts", JoinEntry: true})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range all.Rows {
			if row.ID == "orphan" {
				raw, _ := json.Marshal(row)
				found = row.Entry == nil && strings.Contains(string(raw), `"entry":null`)
			}
		}
		if !found {
			t.Fatal("orphan join did not return null")
		}
		active, err := s.Tables.SourceRows(ctx, p, "receipts")
		if err != nil || len(active) != 3 {
			t.Fatalf("source includes reversed/orphan entries: rows=%d %v", len(active), err)
		}
		// Undo archives all five schemas in reverse reference order.
		call("undo", map[string]any{"event_id": created.EventID, "reason": "Synthetic template test"})
		for _, table := range created.Tables {
			if _, err := s.Tables.Get(ctx, p, table.ID); err == nil {
				t.Fatal("template undo left active table", table.ID)
			}
		}
	})
}
