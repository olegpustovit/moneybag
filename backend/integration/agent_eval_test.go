package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// testAgentEvalTasks runs the agent jobs the MCP tools exist for; each step must end in the stated state.
func testAgentEvalTasks(t *testing.T, ctx context.Context, pa principal.Principal, s mcpserver.Services, db *mongo.Database) {
	t.Helper()
	spec := dashboard.Spec{ID: "eval-agent", Title: "Eval", Version: 2, Pages: []dashboard.Page{{ID: "main", Title: "Main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 12, Component: "value"}}}}}}, Components: []dashboard.Component{{ID: "value", Type: "kpi", Title: "Value", Value: "1"}}}
	head := dashboard.Head{Key: pa.UserID + ":eval-agent", UserID: pa.UserID, ID: "eval-agent", Title: "Eval", PublishedVersion: 2, DraftVersion: 2, Revision: 2}
	if _, e := db.Collection("dashboards").InsertOne(ctx, head); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Collection("dashboard_versions").InsertOne(ctx, dashboard.Version{Key: pa.UserID + ":eval-agent:2", UserID: pa.UserID, DashboardID: "eval-agent", Version: 2, Spec: spec}); e != nil {
		t.Fatal(e)
	}
	cs := mcpSession(t, ctx, pa, s, false)
	call := func(name string, args map[string]any) (map[string]any, *mcp.CallToolResult) {
		t.Helper()
		result, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(name, e)
		}
		var out map[string]any
		if raw, _ := json.Marshal(result.StructuredContent); len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return out, result
	}
	text := func(result *mcp.CallToolResult) string {
		var b strings.Builder
		for _, c := range result.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				b.WriteString(tc.Text)
			}
		}
		return b.String()
	}

	t.Run("find fields before writing CEL", func(t *testing.T) {
		out, result := call("catalog_get", map[string]any{})
		if result.IsError || len(out["sources"].([]any)) == 0 {
			t.Fatal("catalog empty", text(result))
		}
	})

	t.Run("understand a dashboard without its full spec", func(t *testing.T) {
		out, result := call("dashboard_summary", map[string]any{"id": "eval-agent"})
		pages, _ := out["pages"].([]any)
		if result.IsError || len(pages) != 1 || !strings.Contains(text(result), "value") {
			t.Fatal("summary", out)
		}
	})

	t.Run("repair a bad edit before writing", func(t *testing.T) {
		out, result := call("dashboard_apply", map[string]any{"dashboard_id": "eval-agent", "ops": []map[string]any{{"op": "replace", "path": "/nope", "value": 1}}, "validate_only": true})
		issues, _ := out["validation"].(map[string]any)["errors"].([]any)
		if result.IsError || len(issues) != 1 || !strings.Contains(issues[0].(map[string]any)["hint"].(string), "read valid paths") {
			t.Fatal("dry run did not explain the bad path", out)
		}
		fixed, result := call("dashboard_apply", map[string]any{"dashboard_id": "eval-agent", "ops": []map[string]any{{"op": "replace", "path": "/title", "value": "Retitled"}}, "validate_only": true})
		if result.IsError || fixed["validation"].(map[string]any)["ok"] != true {
			t.Fatal("corrected dry run rejected", fixed)
		}
		draft, _ := s.Dashboard.Get(ctx, pa, "eval-agent", "")
		if draft.Spec.Title != "Eval" {
			t.Fatal("dry run wrote the draft", draft.Spec.Title)
		}
	})

	t.Run("recover from a stale write", func(t *testing.T) {
		_, result := call("dashboard_apply", map[string]any{"dashboard_id": "eval-agent", "ops": []map[string]any{{"op": "replace", "path": "/title", "value": "Stale"}}, "if_version": 1})
		if !result.IsError || !strings.Contains(text(result), "re-read it") {
			t.Fatal("stale write accepted", text(result))
		}
		_, result = call("dashboard_apply", map[string]any{"dashboard_id": "eval-agent", "ops": []map[string]any{{"op": "replace", "path": "/title", "value": "Retried"}}, "if_version": 2})
		draft, _ := s.Dashboard.Get(ctx, pa, "eval-agent", "")
		if result.IsError || draft.Spec.Title != "Retried" {
			t.Fatal("retry with the current version failed", text(result), draft.Spec.Title)
		}
	})

	t.Run("render or say why not", func(t *testing.T) {
		_, result := call("dashboard_render", map[string]any{"id": "eval-agent"})
		if result.IsError || !strings.Contains(text(result), "\"ok\":true") {
			t.Fatal("render reported invalid", text(result))
		}
	})
}
