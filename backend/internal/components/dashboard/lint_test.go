package dashboard

import "testing"

func TestLintFlagsComponentsWithoutUnit(t *testing.T) {
	spec := Spec{Components: []Component{{ID: "k", Type: "kpi"}, {ID: "c", Type: "chart", Format: "usd"}, {ID: "t", Type: "table"}}}
	issues := Lint(spec)
	if len(issues) != 1 || issues[0].Path != "/components/0/format" || issues[0].ComponentID != "k" || issues[0].Hint == "" {
		t.Fatalf("lint: %+v", issues)
	}
}

func TestLintFlagsIdentifiersInVisibleText(t *testing.T) {
	spec := Spec{Components: []Component{{ID: "k", Type: "kpi", Format: "usd", Title: "Net (data.total)"}, {ID: "c", Type: "chart", Format: "usd", Title: "Monthly spend"}}}
	issues := Lint(spec)
	if len(issues) != 1 || issues[0].Field != "title" || issues[0].ComponentID != "k" {
		t.Fatalf("lint: %+v", issues)
	}
}
