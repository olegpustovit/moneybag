package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"strings"
)

func resourceResult(uri string, v any) (*mcp.ReadResourceResult, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(b)}}}, nil
}
func registerReference(server *mcp.Server, s Services) {
	ledgerSchema, e := schema[struct {
		Accounts []ledger.Account `json:"accounts"`
		Entries  []ledger.Entry   `json:"entries"`
	}]()
	if e != nil {
		panic(e)
	}
	dashboardSchema, e := schema[dashboard.Spec]()
	if e != nil {
		panic(e)
	}
	for uri, v := range map[string]any{"moneybag://schema/ledger": ledgerSchema, "moneybag://schema/dashboard": dashboardSchema, "moneybag://dsl/cel": map[string]any{"variables": []string{"row", "data", "params", "computed", "rates"}, "functions": []string{"sum(list)", "min(list)", "max(list)", "round(number, decimals)", "abs(number)", "pct(number, total)", "eur(number)", "usd(number)", "month(ISO date)", "size(value)", "int(value)", "double(value)", "string(value)", "list.map/filter/exists/all"}, "limits": map[string]int{"expression_characters": 2000, "expressions_per_spec": 200, "cost_per_evaluation": 10000, "query_rows": 5000, "page_milliseconds": 1000}, "example": "sum(data.lines.map(l, l.limit))"}} {
		uri, v := uri, v
		server.AddResource(&mcp.Resource{URI: uri, Name: strings.TrimPrefix(uri, "moneybag://"), MIMEType: "application/json"}, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			p, e := principal.From(ctx)
			if e != nil {
				return nil, e
			}
			if e = p.Require("ledger:read"); e != nil {
				return nil, e
			}
			return resourceResult(uri, v)
		})
	}
	server.AddResource(&mcp.Resource{URI: "moneybag://accounts", Name: "Chart of accounts", MIMEType: "application/json"}, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		p, e := principal.From(ctx)
		if e != nil {
			return nil, e
		}
		v, e := s.Ledger.Accounts(ctx, p, ledger.AccountsInput{Limit: 500})
		if e != nil {
			return nil, e
		}
		return resourceResult("moneybag://accounts", v)
	})
	for _, pattern := range []string{"moneybag://dashboards/{id}", "moneybag://dashboards/{id}/draft", "moneybag://budget/{month}", "moneybag://imports/{batch_id}"} {
		pattern := pattern
		server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: pattern, Name: strings.TrimPrefix(pattern, "moneybag://"), MIMEType: "application/json"}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			p, e := principal.From(ctx)
			if e != nil {
				return nil, e
			}
			uri := req.Params.URI
			if strings.HasPrefix(uri, "moneybag://imports/") {
				v, e := s.Imports.Preview(ctx, p, strings.TrimPrefix(uri, "moneybag://imports/"), 0)
				if e != nil {
					return nil, e
				}
				return resourceResult(uri, v)
			}
			if strings.HasPrefix(uri, "moneybag://budget/") {
				v, e := s.Budget.Get(ctx, p, strings.TrimPrefix(uri, "moneybag://budget/"))
				if e != nil {
					return nil, e
				}
				return resourceResult(uri, v)
			}
			id := strings.TrimPrefix(uri, "moneybag://dashboards/")
			version := "published"
			if strings.HasSuffix(id, "/draft") {
				id = strings.TrimSuffix(id, "/draft")
				version = "draft"
			}
			if id == "" || strings.Contains(id, "/") {
				return nil, fmt.Errorf("invalid dashboard URI")
			}
			access, e := s.Sharing.Access(ctx, p, id, false)
			if e != nil {
				return nil, e
			}
			v, e := access.Dashboard.Get(ctx, access.Principal, access.ID, version)
			if e != nil {
				return nil, e
			}
			return resourceResult(uri, v)
		})
	}
	prompts := map[string]string{
		"monthly_close":     "Review the current user's accounts and dates. Sync configured feeds or request a statement. Stage import_start, examine import_preview and duplicate candidates, correct rows and import_commit. Run reconcile_status and resolve only with evidence and a reason. Review budget_get and summarize changes with audit links. Never infer an unknown balance.",
		"import_statement":  "Identify the statement account using accounts_list. Detect supported format. Call import_start with the file, inspect import_preview, edit classifications and skip duplicate rows, then import_commit with a closing balance assertion when known. Retain provenance; use import_undo with a reason if the committed batch is wrong.",
		"build_dashboard":   "Ask what the dashboard should answer. Discover database schemas and sources. dashboard_create, add components, compose the layout, validate, preview and verify the numbers. A token with dash:write may publish immediately through dashboard_publish when the gate passes; no human approval is required. Report the live URL and version and explain rollback.",
		"review_spending":   "Read budget_get and entries_list for the requested period. Identify outliers and unmapped expenses. Propose classification rules with rule_preview; keep reclassifications as drafts unless asked to commit. Transfers between own accounts are not spending.",
		"scenario_question": "Use model_get to discover assumptions and scenarios. Translate the question into explicit assumption overrides and dated events, then scenario_upsert, forecast_run and scenario_compare. State assumptions and quote dates; a scenario is hypothetical and must not create ledger entries.",
	}
	for name, text := range prompts {
		name, text := name, text
		server.AddPrompt(&mcp.Prompt{Name: name, Description: "Moneybag " + strings.ReplaceAll(name, "_", " ")}, func(ctx context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			if _, e := principal.From(ctx); e != nil {
				return nil, e
			}
			return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}}}, nil
		})
	}
}
