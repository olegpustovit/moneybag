package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
)

// Drives the receipts workflow as an agent would, through MCP only.
func testReceiptAgentWorkflow(t *testing.T, ctx context.Context, owner principal.Principal, s mcpserver.Services) {
	t.Run("receipt agent workflow", func(t *testing.T) {
		p := owner
		p.UserID = ulid.Make().String()
		client := mcpSession(t, ctx, p, s, false)
		call := func(name string, args any) map[string]any {
			t.Helper()
			result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				raw, _ := json.Marshal(result.Content)
				t.Fatalf("%s: %s", name, raw)
			}
			raw, _ := json.Marshal(result.StructuredContent)
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			return out
		}
		call("account_create", map[string]any{"id": "assets:bank", "name": "Bank", "type": "asset", "commodities": []string{"EUR"}})
		call("account_create", map[string]any{"id": "expenses:food", "name": "Food", "type": "expense", "commodities": []string{"EUR"}})
		posted := call("entry_post", map[string]any{"date": "2026-11-03", "payee": "Konzum", "commit": true, "postings": []map[string]any{
			{"account": "expenses:food", "amount": map[string]any{"v": "22.62", "c": "EUR"}}, {"account": "assets:bank", "amount": map[string]any{"v": "-22.62", "c": "EUR"}}}})
		entry, _ := posted["entry"].(map[string]any)
		id, _ := entry["id"].(string)
		if id == "" {
			t.Fatalf("no entry id: %v", posted)
		}
		call("db_create", map[string]any{"template": "receipts"})
		base := []map[string]any{
			{"db": "merchants", "upsert": map[string]any{"row_id": "HR:12345678903", "cells": map[string]any{"country": "HR", "tax_id": "12345678903", "brand": "Konzum"}}},
			{"db": "stores", "upsert": map[string]any{"row_id": "HR:12345678903:P1232", "cells": map[string]any{"merchant": "HR:12345678903", "store_code": "P1232", "label": "Konzum Ilica", "country": "HR"}}},
			{"db": "receipts", "upsert": map[string]any{"row_id": id, "cells": map[string]any{"entry": id, "store": "HR:12345678903:P1232", "issued_at": "2026-11-03T18:42:00+01:00", "currency": "EUR", "total": "22.62", "source": "photo"}}},
			{"db": "products", "upsert": map[string]any{"row_id": "gtin:4006381333931", "cells": map[string]any{"gtin": "4006381333931", "name": "Milk"}}},
		}
		line := func(n int, cells map[string]any) map[string]any {
			cells["entry"], cells["line"] = id, n
			return map[string]any{"db": "receipt_items", "upsert": map[string]any{"row_id": id + ":" + string(rune('0'+n)), "cells": cells}}
		}
		// Agents often send numbers for decimals, and a wrong sum.
		ops := append(base, line(1, map[string]any{"raw_name": "MLIJEKO", "qty": 2, "unit_price": "1.26", "amount": "2.52", "kind": "item", "product": "gtin:4006381333931"}),
			line(2, map[string]any{"raw_name": "KRUH", "amount": "20.00", "kind": "item"}))
		dry := call("db_batch", map[string]any{"dry_run": true, "ops": ops})
		if raw, _ := json.Marshal(dry); !strings.Contains(string(raw), `expects a decimal string such as \"2.52\"`) || !strings.Contains(string(raw), "difference 0.10 EUR") {
			t.Fatalf("dry run should name the decimal format and the missing amount: %s", raw)
		}
		if refused := call("db_batch", map[string]any{"ops": ops}); refused["event_id"] != nil || len(refused["violations"].([]any)) == 0 {
			t.Fatalf("a refused batch must not report an event: %v", refused)
		}
		ops = append(base, line(1, map[string]any{"raw_name": "MLIJEKO", "qty": "2", "unit_price": "1.26", "amount": "2.52", "kind": "item", "product": "gtin:4006381333931"}),
			line(2, map[string]any{"raw_name": "KRUH", "amount": "20.10", "kind": "item"}))
		call("db_batch", map[string]any{"ops": ops})
		// Re-itemise: delete then rewrite the same row ids without the product ref.
		call("db_batch", map[string]any{"ops": []map[string]any{
			{"db": "receipt_items", "delete_where": "row.entry == '" + id + "'"},
			line(1, map[string]any{"raw_name": "MLIJEKO 2", "amount": "2.52", "kind": "item"}),
			line(2, map[string]any{"raw_name": "KRUH", "amount": "20.10", "kind": "item"}),
		}})
		call("db_query", map[string]any{"db": "receipt_items", "join_entry": true, "expand": []string{"product"}})
		call("db_query", map[string]any{"db": "receipts", "where": "row.store == 'HR:12345678903:P1232'", "join_entry": true, "expand": []string{"store"}})
		call("entries_list", map[string]any{"explained_by": "!receipt_items"})
		call("entry_get", map[string]any{"id": id})
		call("db_batch", map[string]any{"ops": []map[string]any{{"db": "products", "delete_where": "row.gtin == '4006381333931'"}, {"db": "stores", "delete_where": "true"}}})
		call("db_list", map[string]any{})
	})
}
