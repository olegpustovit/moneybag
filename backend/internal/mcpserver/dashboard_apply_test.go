package mcpserver

import (
	"encoding/json"
	"testing"
)

func TestWithVersionGuardPrependsTestOp(t *testing.T) {
	for _, ops := range []json.RawMessage{json.RawMessage(`[{"op":"replace","path":"/title","value":"x"}]`), json.RawMessage(`"[{\"op\":\"replace\",\"path\":\"/title\",\"value\":\"x\"}]"`)} {
		out, err := withVersionGuard(ops, 4)
		if err != nil {
			t.Fatal(err)
		}
		var list []map[string]any
		if err := json.Unmarshal(out, &list); err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 || list[0]["op"] != "test" || list[0]["path"] != "/version" || list[0]["value"] != float64(4) || list[1]["op"] != "replace" {
			t.Fatalf("guarded ops: %s", out)
		}
	}
	if _, err := withVersionGuard(json.RawMessage(`{"op":"replace"}`), 4); err == nil {
		t.Fatal("non-array ops accepted")
	}
}
