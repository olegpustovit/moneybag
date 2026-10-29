package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

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
