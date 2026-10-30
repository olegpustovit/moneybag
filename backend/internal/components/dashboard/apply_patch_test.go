package dashboard

import (
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

func TestApplyPatchErrorsSayWhatToDo(t *testing.T) {
	base := Spec{ID: "d", Title: "Cash", Version: 2}
	cases := []struct {
		name string
		ops  string
		want string
	}{
		{"missing path", `[{"op":"replace","path":"/nope","value":1}]`, "not found; read valid paths"},
		{"failed test", `[{"op":"test","path":"/version","value":1}]`, "the draft changed"},
		{"unknown field", `[{"op":"add","path":"/bogus","value":1}]`, "unknown fields are rejected"},
	}
	for _, c := range cases {
		patch, err := jsonpatch.DecodePatch([]byte(c.ops))
		if err != nil {
			t.Fatal(err)
		}
		spec := base
		err = applyPatch(&spec, patch)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}
