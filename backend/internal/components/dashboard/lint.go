package dashboard

import (
	"fmt"
	"regexp"
)

// Lint returns advisory issues for a spec. Each issue names a rule-specific fix, so an agent can apply it directly.
func Lint(spec Spec) []Issue {
	var out []Issue
	for i, c := range spec.Components {
		if (c.Type == "kpi" || c.Type == "chart") && c.Format == "" {
			out = append(out, Issue{Path: fmt.Sprintf("/components/%d/format", i), Msg: fmt.Sprintf("%s %q declares no unit", c.Type, c.ID), ComponentID: c.ID, Field: "format", Hint: "set format to usd, eur, pct, int or mo"})
		}
		for _, text := range []struct{ field, value string }{{"title", c.Title}, {"sub", c.Sub}, {"label", c.Label}} {
			if identifierPattern.MatchString(text.value) {
				out = append(out, Issue{Path: fmt.Sprintf("/components/%d/%s", i, text.field), Msg: fmt.Sprintf("%s %q shows an identifier in visible text", c.Type, c.ID), ComponentID: c.ID, Field: text.field, Hint: "use a human label instead of a row, data, computed or params path"})
			}
		}
	}
	return out
}

var identifierPattern = regexp.MustCompile(`\b(row|data|computed|params)\.`)
