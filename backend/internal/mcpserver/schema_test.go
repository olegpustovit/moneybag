package mcpserver

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
)

// A client validates results against the output schema it cached at connect time; closed objects break it whenever a field is added.
func TestOutputSchemasAcceptNewFields(t *testing.T) {
	s, e := schema[dashboard.Rendered]()
	if e != nil {
		t.Fatal(e)
	}
	openObjects(s)
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), `"additionalProperties":false`) {
		t.Fatal(string(b))
	}
}

// Some connectors silently drop a tool when a property schema is the bare boolean `true`.
func TestNoBareTrueSchemas(t *testing.T) {
	srv := New(Services{}, Options{})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, e := srv.Connect(ctx, st, nil); e != nil {
		t.Fatal(e)
	}
	cs, e := mcp.NewClient(&mcp.Implementation{Name: "schema-test"}, nil).Connect(ctx, ct, nil)
	if e != nil {
		t.Fatal(e)
	}
	res, e := cs.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	bare := regexp.MustCompile(`"([A-Za-z_]+)":true`)
	for _, tl := range res.Tools {
		for _, sch := range []any{tl.InputSchema, tl.OutputSchema} {
			b, _ := json.Marshal(sch)
			for _, m := range bare.FindAllStringSubmatch(string(b), -1) {
				if m[1] != "additionalProperties" && m[1] != "readOnly" && m[1] != "uniqueItems" {
					t.Errorf("%s: property %s has a bare true schema", tl.Name, m[1])
				}
			}
		}
	}
}
