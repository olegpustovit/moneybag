package dashboard

import "testing"

func TestLintFlagsComponentsWithoutUnit(t *testing.T) {
	spec := Spec{Components: []Component{{ID: "k", Type: "kpi"}, {ID: "c", Type: "chart", Format: "usd"}, {ID: "t", Type: "table"}}}
	issues := Lint(spec)
	if len(issues) != 1 || issues[0].Path != "/components/0/format" || issues[0].ComponentID != "k" || issues[0].Hint == "" {
		t.Fatalf("lint: %+v", issues)
	}
}
