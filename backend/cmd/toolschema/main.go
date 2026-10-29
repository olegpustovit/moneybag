// Command toolschema emits the schemas registered by the MCP server as one JSON Schema.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
)

func namespaceRefs(v any, prefix string) {
	switch value := v.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "$ref" {
				if ref, ok := child.(string); ok && strings.HasPrefix(ref, "#/$defs/") {
					value[key] = "#/$defs/" + prefix + "/$defs/" + strings.TrimPrefix(ref, "#/$defs/")
				}
				continue
			}
			namespaceRefs(child, prefix)
		}
	case []any:
		for _, child := range value {
			namespaceRefs(child, prefix)
		}
	}
}

func run(ctx context.Context) error {
	server := mcpserver.New(mcpserver.Services{}, mcpserver.Options{Version: "schema"})
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return err
	}
	defer serverSession.Close()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "toolschema", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		return err
	}
	defer client.Close()
	defs := map[string]any{}
	properties := map[string]any{}
	required := []string{}
	for tool, err := range client.Tools(ctx, nil) {
		if err != nil {
			return err
		}
		fields := map[string]any{}
		for _, item := range []struct {
			name   string
			schema any
		}{{"input", tool.InputSchema}, {"output", tool.OutputSchema}} {
			if item.schema == nil {
				return fmt.Errorf("%s has no %s schema", tool.Name, item.name)
			}
			encoded, err := json.Marshal(item.schema)
			if err != nil {
				return err
			}
			var schema any
			if err := json.Unmarshal(encoded, &schema); err != nil {
				return err
			}
			key := tool.Name + "_" + item.name
			namespaceRefs(schema, key)
			defs[key] = schema
			fields[item.name] = map[string]any{"$ref": "#/$defs/" + key}
		}
		properties[tool.Name] = map[string]any{"type": "object", "properties": fields, "required": []string{"input", "output"}, "additionalProperties": false}
		required = append(required, tool.Name)
	}
	root := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "title": "MoneybagTools", "type": "object", "properties": properties, "required": required, "additionalProperties": false, "$defs": defs}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(root)
}
func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
