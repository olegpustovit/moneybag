package dashboard

import "fmt"

// Lint returns advisory issues for a spec. Each issue names a rule-specific fix, so an agent can apply it directly.
func Lint(spec Spec) []Issue {
	var out []Issue
	for i, c := range spec.Components {
		if (c.Type == "kpi" || c.Type == "chart") && c.Format == "" {
			out = append(out, Issue{Path: fmt.Sprintf("/components/%d/format", i), Msg: fmt.Sprintf("%s %q declares no unit", c.Type, c.ID), ComponentID: c.ID, Field: "format", Hint: "set format to usd, eur, pct, int or mo"})
		}
	}
	return out
}
