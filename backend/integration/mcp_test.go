package integration

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/consistency"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"strings"
	"testing"
)

func mcpSession(t *testing.T, ctx context.Context, p principal.Principal, s mcpserver.Services, readonly bool) *mcp.ClientSession {
	t.Helper()
	server := mcpserver.New(s, mcpserver.Options{ReadOnly: readonly, Version: "integration"})
	st, ct := mcp.NewInMemoryTransports()
	ss, e := server.Connect(principal.With(ctx, p), st, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "moneybag-integration", Version: "1"}, nil)
	cs, e := client.Connect(ctx, ct, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}
func schemaExample(value any, root map[string]any) any {
	schema, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		var v any = root
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			v = v.(map[string]any)[part]
		}
		return schemaExample(v, root)
	}
	if variants, ok := schema["anyOf"].([]any); ok {
		return schemaExample(variants[0], root)
	}
	kind := schema["type"]
	if types, ok := kind.([]any); ok {
		kind = types[0]
	}
	switch kind {
	case "object":
		out := map[string]any{}
		props, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]any)
		for _, key := range required {
			out[key.(string)] = schemaExample(props[key.(string)], root)
		}
		return out
	case "array":
		return []any{}
	case "string":
		if _, ok := schema["pattern"]; ok {
			return "1"
		}
		if schema["format"] == "date-time" {
			return "2026-10-01T00:00:00Z"
		}
		return "fixture"
	case "number", "integer":
		return 1
	case "boolean":
		return false
	default:
		return nil
	}
}
func testMCPWorkflows(t *testing.T, ctx context.Context, pa, pb principal.Principal, s mcpserver.Services, db *mongo.Database) {
	t.Helper()
	blocked := pa
	blocked.Scopes = nil
	denied := mcpSession(t, ctx, blocked, s, false)
	tools, e := denied.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(tools.Tools) != 68 {
		t.Fatal("MCP tool catalog", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		t.Run("scope/"+tool.Name, func(t *testing.T) {
			if tool.Description == "" || tool.OutputSchema == nil || tool.Annotations == nil {
				t.Fatal("incomplete contract", tool.Name)
			}
			b, e := json.Marshal(tool.InputSchema)
			if e != nil {
				t.Fatal(e)
			}
			var schema map[string]any
			if e = json.Unmarshal(b, &schema); e != nil {
				t.Fatal(e)
			}
			result, e := denied.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: schemaExample(schema, schema)})
			if e != nil || !result.IsError {
				t.Fatal("missing scope did not return a tool error", result, e)
			}
			raw, _ := json.Marshal(result.Content)
			if !strings.Contains(string(raw), principal.ErrForbidden.Error()) {
				t.Fatalf("request did not reach scope guard: %s", raw)
			}
		})
	}
	resources, e := denied.ListResources(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	templates, e := denied.ListResourceTemplates(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(resources.Resources)+len(templates.ResourceTemplates) != 8 {
		t.Fatal("resource catalog", len(resources.Resources), len(templates.ResourceTemplates))
	}
	prompts, e := denied.ListPrompts(ctx, nil)
	if e != nil || len(prompts.Prompts) != 5 {
		t.Fatal("prompt catalog", prompts, e)
	}
	ro := mcpSession(t, ctx, pa, s, true)
	list, e := ro.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, tool := range list.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Fatal("write tool exposed by readonly transport", tool.Name)
		}
	}
	if result, e := ro.CallTool(ctx, &mcp.CallToolParams{Name: "entry_post", Arguments: map[string]any{}}); e == nil && !result.IsError {
		t.Fatal("readonly tool call accepted")
	}
	own := mcpSession(t, ctx, pa, s, false)
	result, e := own.CallTool(ctx, &mcp.CallToolParams{Name: "expr_eval", Arguments: map[string]any{"expr": "data.rows[0].total", "vars": map[string]any{"data": map[string]any{"rows": map[string]any{"from": "table:margin", "where": "row.qty == 2.0"}}}}})
	if e != nil || result.IsError {
		t.Fatal("MCP expression workflow", result, e)
	}
	result, e = own.CallTool(ctx, &mcp.CallToolParams{Name: "consistency_check", Arguments: map[string]any{}})
	if e != nil || result.IsError {
		t.Fatal("consistency_check", result.Content[0].(*mcp.TextContent).Text, e)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	var report consistency.Result
	if json.Unmarshal(raw, &report) != nil || len(report.Checks) != 11 || report.Checks[0].ID != "trial_balance" || !report.Checks[0].OK {
		t.Fatal("trial balance must balance on the fixture", string(raw))
	}
	result, e = own.CallTool(ctx, &mcp.CallToolParams{Name: "dashboard_get", Arguments: map[string]any{"id": "fixed"}})
	if e != nil || result.IsError {
		t.Fatal("typed dashboard output", result, e)
	}
	testHTTPAndStdio(t, ctx, db, pa, s)
	other := mcpSession(t, ctx, pb, s, false)
	result, e = other.CallTool(ctx, &mcp.CallToolParams{Name: "dashboard_get", Arguments: map[string]any{"id": "fixed"}})
	if e != nil || !result.IsError {
		t.Fatal("MCP cross-user read accepted", result, e)
	}
}
