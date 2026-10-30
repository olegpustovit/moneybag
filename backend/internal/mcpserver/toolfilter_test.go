package mcpserver

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

const testUser = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

func listTools(t *testing.T, p principal.Principal, opts Options) []*mcp.Tool {
	t.Helper()
	ctx := principal.With(context.Background(), p)
	ct, st := mcp.NewInMemoryTransports()
	ss, e := New(Services{}, opts).Connect(ctx, st, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ss.Close() })
	cs, e := mcp.NewClient(&mcp.Implementation{Name: "filter-test"}, nil).Connect(ctx, ct, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cs.Close() })
	res, e := cs.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	return res.Tools
}

func names(tools []*mcp.Tool) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	return out
}

func schemaBytes(t *testing.T, tools []*mcp.Tool) int {
	t.Helper()
	total := 0
	for _, tool := range tools {
		b, e := json.Marshal(tool)
		if e != nil {
			t.Fatal(e)
		}
		total += len(b)
	}
	return total
}

func TestAuthoringAgentStaysUnderTheToolBudget(t *testing.T) {
	agent := principal.Principal{UserID: testUser, Scopes: []string{"ledger:read", "dash:write"}, Actor: principal.Actor{Kind: "agent", ID: "client"}}
	for name, opts := range map[string]Options{"by scope": {}, "by toolset": {Toolsets: map[string]bool{"dashboard": true}}} {
		tools := listTools(t, agent, opts)
		size := schemaBytes(t, tools)
		t.Logf("%s: %d tools, %d bytes", name, len(tools), size)
		slices.SortFunc(tools, func(a, b *mcp.Tool) int { return schemaBytes(t, []*mcp.Tool{b}) - schemaBytes(t, []*mcp.Tool{a}) })
		for _, tool := range tools[:6] {
			t.Logf("  %s %d", tool.Name, schemaBytes(t, []*mcp.Tool{tool}))
		}
		if len(tools) >= 40 {
			t.Errorf("%s: %d tools: %v", name, len(tools), names(tools))
		}
		if size >= 60*1024 {
			t.Errorf("%s: schemas are %d bytes", name, size)
		}
		got := names(tools)
		for _, want := range []string{"metric_upsert", "metric_query", "dashboard_apply", "dashboard_lint", "dashboard_test", "dashboard_publish", "catalog_get", "field_values", "feedback_list"} {
			if !slices.Contains(got, want) {
				t.Errorf("%s: authoring agent cannot see %s", name, want)
			}
		}
		for hidden := range legacyEditTools {
			if slices.Contains(got, hidden) {
				t.Errorf("%s: legacy %s is listed", name, hidden)
			}
		}
		for _, admin := range []string{"user_list", "user_invite", "user_disable", "agent_list", "agent_revoke"} {
			if slices.Contains(got, admin) {
				t.Errorf("%s: dashboard agent sees %s", name, admin)
			}
		}
	}
}

func TestToolListFollowsGrantedScopes(t *testing.T) {
	reader := names(listTools(t, principal.Principal{UserID: testUser, Scopes: []string{"ledger:read"}}, Options{}))
	if !slices.Contains(reader, "entries_list") || !slices.Contains(reader, "admin_request") || slices.Contains(reader, "entry_post") || slices.Contains(reader, "metric_upsert") {
		t.Fatal("reader list", reader)
	}
	admin := names(listTools(t, principal.Principal{UserID: testUser, Scopes: append(slices.Clone(principal.Scopes), principal.ScopeAdminUsers)}, Options{}))
	for _, want := range []string{"user_list", "user_invite", "user_disable", "agent_list", "agent_revoke", "entry_post", "feed_upsert", "imports_list", "nav_set"} {
		if !slices.Contains(admin, want) {
			t.Error("admin cannot see", want)
		}
	}
	owner := names(listTools(t, principal.Principal{UserID: testUser, Scopes: principal.Scopes}, Options{}))
	if slices.Contains(owner, "user_list") {
		t.Fatal("full-scope agent without admin:users sees admin tools")
	}
	if slices.Contains(principal.Scopes, principal.ScopeAdminUsers) {
		t.Fatal("admin:users must stay off the consent screen's default scopes")
	}
}

func TestLegacyEditToolsStayCallable(t *testing.T) {
	ctx := principal.With(context.Background(), principal.Principal{UserID: testUser})
	ct, st := mcp.NewInMemoryTransports()
	ss, e := New(Services{}, Options{}).Connect(ctx, st, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer ss.Close()
	cs, e := mcp.NewClient(&mcp.Implementation{Name: "filter-test"}, nil).Connect(ctx, ct, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cs.Close()
	res, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "dashboard_patch", Arguments: map[string]any{"dashboard_id": "x", "ops": []any{}}})
	if e != nil || !res.IsError {
		t.Fatal("expected a scope error from the tool itself", res, e)
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "permission denied") {
		t.Fatalf("hidden tool is gone rather than refused: %s", raw)
	}
}

func TestSpecSchemaClosesItsVocabulary(t *testing.T) {
	full, e := schema[dashboard.Spec]()
	if e != nil {
		t.Fatal(e)
	}
	specEnums(full)
	b, _ := json.Marshal(full)
	for _, want := range []string{`"time_chart"`, `"stack"`, `"money"`, `"previous_year"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("schema misses %s: %s", want, b)
		}
	}
}
