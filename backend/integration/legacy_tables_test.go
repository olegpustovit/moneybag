package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
)

// The 14 production schemas captured on 2026-11-04, with table names anonymized and no row data.
func testLegacyTableSchemas(t *testing.T, ctx context.Context, p principal.Principal, services mcpserver.Services) {
	t.Helper()
	encoded, err := os.ReadFile("testdata/legacy-table-schemas.json")
	if err != nil {
		t.Fatal(err)
	}
	var schemas []tables.CreateInput
	if err = json.Unmarshal(encoded, &schemas); err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 14 {
		t.Fatalf("expected 14 legacy schemas, got %d", len(schemas))
	}
	client := mcpSession(t, ctx, p, services, false)
	call := func(t *testing.T, name string, args any, output any) {
		t.Helper()
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(name, err)
		}
		if result.IsError {
			t.Fatalf("%s: %v", name, result.Content)
		}
		body, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(body, output); err != nil {
			t.Fatal(err)
		}
	}
	for index, schema := range schemas {
		t.Run(fmt.Sprintf("legacy schema %02d", index+1), func(t *testing.T) {
			id := fmt.Sprintf("legacy-schema-%02d", index+1)
			if _, err := services.Tables.CreateNamed(ctx, p, id, schema); err != nil {
				t.Fatal(err)
			}
			updates := []tables.CellUpdate{}
			cells := map[string]any{}
			for _, column := range schema.Columns {
				var value any = "000123.4500"
				if column.Type == "number" {
					value = float64(5.25)
				}
				updates = append(updates, tables.CellUpdate{RowID: "one", Column: column.ID, Value: value})
				cells[column.ID] = value
			}
			var written tables.UpdateResult
			call(t, "db_update_cells", tables.UpdateInput{DB: id, Updates: updates}, &written)
			if len(written.Rows) != 1 || written.Rows[0].Version != 1 || !reflect.DeepEqual(written.Rows[0].Cells, cells) || len(written.DependentsRecomputed) != 0 || written.EventID == "" {
				t.Fatalf("legacy update contract changed: %#v", written)
			}
			var queried tables.QueryResult
			call(t, "db_query", tables.QueryInput{DB: id}, &queried)
			expected := tables.QueryResult{Columns: schema.Columns, Rows: []tables.Row{{ID: "one", Cells: cells, Version: 1}}}
			if !reflect.DeepEqual(queried, expected) {
				t.Fatalf("legacy query contract changed: got %#v; want %#v", queried, expected)
			}
			version := 1
			call(t, "db_update_cells", tables.UpdateInput{DB: id, Updates: []tables.CellUpdate{{RowID: "one", Column: schema.Columns[0].ID, Value: cells[schema.Columns[0].ID], Version: &version}}}, &written)
			if len(written.Rows) != 1 || written.Rows[0].Version != 2 || !reflect.DeepEqual(written.Rows[0].Cells, cells) {
				t.Fatalf("legacy versioned update changed: %#v", written)
			}
		})
	}
}
