package mcpserver

import (
	"context"
	"slices"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

type toolInfo struct{ set, scope string }

// legacyEditTools overlap dashboard_apply. They stay callable for one transition release but no longer cost every agent their schemas.
var legacyEditTools = map[string]bool{"dashboard_add_component": true, "dashboard_update_component": true, "dashboard_remove_component": true, "dashboard_compose": true, "dashboard_patch": true}

// authoringAgent is a connection that builds dashboards but cannot write money; its list stays under the authoring budget.
func authoringAgent(p principal.Principal) bool {
	return p.Require("dash:write") == nil && p.Require("ledger:write") != nil && p.Require("import:write") != nil && p.Require("model:write") != nil
}

func visibleTool(p principal.Principal, name string, tool toolInfo) bool {
	if legacyEditTools[name] || p.Require(tool.scope) != nil {
		return false
	}
	return !authoringAgent(p) || tool.set == "dashboard" || name == "whoami"
}

// filterToolList narrows tools/list to what this connection can use. It does not change what is callable: a call still meets its own scope check.
func filterToolList(tools map[string]toolInfo) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			list, isList := result.(*mcp.ListToolsResult)
			if err != nil || !isList {
				return result, err
			}
			// No principal means a local schema export, which needs the whole catalog.
			p, e := principal.From(ctx)
			if e != nil {
				return result, nil
			}
			out := *list
			out.Tools = slices.DeleteFunc(slices.Clone(list.Tools), func(t *mcp.Tool) bool { return !visibleTool(p, t.Name, tools[t.Name]) })
			return &out, nil
		}
	}
}

// specEnums closes the spec's vocabulary on input schemas only: stored specs may predate a value, and a closed output schema would stop a client reading them.
func specEnums(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	set := func(owner *jsonschema.Schema, property string, values []string) {
		if p := owner.Properties[property]; p != nil {
			p.Enum = make([]any, len(values))
			for i, value := range values {
				p.Enum[i] = value
			}
		}
	}
	if s.Properties != nil {
		if s.Properties["encoding"] != nil && s.Properties["columns"] != nil {
			set(s, "mark", dashboard.Marks)
			set(s, "format", dashboard.Formats)
			set(s, "type", dashboard.ComponentTypes)
			set(s, "estimates", dashboard.Policies)
			set(s, "compare", dashboard.Compares)
		}
		if s.Properties["label"] != nil && s.Properties["value"] != nil && s.Properties["total"] != nil {
			set(s, "format", dashboard.Formats)
			set(s, "type", dashboard.ColumnTypes)
		}
		if s.Properties["presets"] != nil || s.Properties["time"] != nil {
			set(s, "compare", dashboard.Compares)
		}
	}
	for _, c := range s.Properties {
		specEnums(c)
	}
	for _, c := range s.Defs {
		specEnums(c)
	}
	specEnums(s.Items)
	specEnums(s.AdditionalProperties)
	for _, list := range [][]*jsonschema.Schema{s.AnyOf, s.OneOf, s.AllOf} {
		for _, c := range list {
			specEnums(c)
		}
	}
}
