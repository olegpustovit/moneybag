package tables

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJoinedRowJSON(t *testing.T) {
	for _, tc := range []struct {
		joined bool
		want   bool
	}{{false, false}, {true, true}} {
		raw, err := json.Marshal(Row{ID: "one", Cells: map[string]any{"entry": "missing"}, joined: tc.joined})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"entry":null`) != tc.want {
			t.Fatalf("joined %v: %s", tc.joined, raw)
		}
		if !strings.Contains(string(raw), `"cells":{"entry":"missing"}`) {
			t.Fatalf("raw ID changed: %s", raw)
		}
	}
}
