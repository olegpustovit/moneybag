package dashboard

import (
	"slices"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/semantic"
)

func rulesOf(issues []LintIssue) []string {
	out := []string{}
	for _, i := range issues {
		out = append(out, i.Rule)
	}
	return out
}

func scoped(c ...Component) Spec {
	return Spec{Title: "Spending", Scope: &Scope{Time: &ScopeTime{Default: "12mo"}, Compare: "previous_period"}, Pages: []Page{{ID: "main", Title: "Main"}}, Components: c}
}

func TestLintKnowsEveryRule(t *testing.T) {
	spec := Spec{Title: "Spending", Pages: []Page{{ID: "main", Title: "Main", Layout: []Row{{Cols: make([]Placement, 13)}}}},
		Data: map[string]Query{"a": {From: "postings", Where: `row.account.startsWith("expenses:") && row.date >= "2026-01-01"`, Agg: map[string]string{"t": "sum(row.value_eur)"}}, "b": {From: "postings", Where: `row.account.startsWith("expenses:") && row.date >= "2025-01-01"`, Agg: map[string]string{"t": "sum(row.value_eur)"}}},
		Components: []Component{
			{ID: "k", Type: "kpi", Title: "Net (data.total)"},
			{ID: "c", Type: "chart", Mark: "line"},
			{ID: "t", Type: "table", Columns: []Column{{Label: "Months", Type: "days", Total: true}}},
			{ID: "s", Type: "stat", Metric: "spending"},
			{ID: "tc", Type: "time_chart", Metric: "spending", X: &XAxis{Type: "category"}},
		}}
	rows := make([]map[string]any, 50)
	for i := range rows {
		rows[i] = map[string]any{"c0": "x"}
	}
	lc := LintContext{HasEstimated: true, Metrics: map[string]semantic.Metric{"spending": {ID: "spending"}}, Rendered: Rendered{Values: map[string]any{
		"c": map[string]any{"points": []chartPoint{{X: "2026-01"}, {X: "2026-02"}}},
		"t": map[string]any{"rows": rows},
	}}}
	got := rulesOf(Lint(spec, lc))
	for _, rule := range LintRules {
		if !slices.Contains(got, rule.Rule) {
			t.Errorf("no spec triggers rule %s: %v", rule.Rule, got)
		}
	}
	for _, issue := range Lint(spec, lc) {
		if issue.Fix == "" || issue.Msg == "" || issue.Path == "" || (issue.Severity != "error" && issue.Severity != "warning") {
			t.Errorf("unusable issue %+v", issue)
		}
	}
}

// A template is judged by what it renders to, so {{ data.x }} never counts as an identifier.
func TestLintEvaluatesTemplatesBeforeJudgingText(t *testing.T) {
	spec := scoped(Component{ID: "k", Type: "kpi", Format: "eur", Delta: "1", Title: "All-in ({{ data.bl[0].window_label }} average)"})
	clean := LintContext{Rendered: Rendered{Resolved: map[string]Component{"k": {ID: "k", Title: "All-in (May to Oct average)"}}}}
	if got := Lint(spec, clean); len(got) != 0 {
		t.Fatalf("template flagged after evaluating: %+v", got)
	}
	leaky := LintContext{Rendered: Rendered{Resolved: map[string]Component{"k": {ID: "k", Title: "All-in (card_core_median)"}}}}
	if got := rulesOf(Lint(spec, leaky)); !slices.Contains(got, "no_raw_ids") {
		t.Fatal("identifier in the evaluated text missed", got)
	}
	// A panel that never evaluated is reported by validation; its template still is not an identifier.
	if got := rulesOf(Lint(spec, LintContext{})); slices.Contains(got, "no_raw_ids") {
		t.Fatal("unevaluated template flagged", got)
	}
	// Literal text beside a template is still judged.
	spec.Components[0].Title = "{{ data.x }} card_trend"
	if got := rulesOf(Lint(spec, LintContext{})); !slices.Contains(got, "no_raw_ids") {
		t.Fatal("literal identifier missed", got)
	}
}

func TestLintNamesWhatLeaks(t *testing.T) {
	for text, want := range map[string]string{"Net (data.total)": "CEL path", "expenses:food total": "account id", "01ARZ3NDEKTSV4RRFFQ69G5FAV": "record id", "2022-02-11T16:00:00Z": "timestamp", "card_trend": "code name", "Hello {{ x }}": "template"} {
		if got := identifier(text); !strings.Contains(got, want) {
			t.Errorf("%s: %q", text, got)
		}
	}
	for _, prose := range []string{"Spending in May", "Everyday card (EUR)", "Gold price updated 6 times", ""} {
		if got := identifier(prose); got != "" {
			t.Errorf("%q flagged: %s", prose, got)
		}
	}
}

func TestLintScopeAndOptOuts(t *testing.T) {
	text := Spec{Title: "Note", Pages: []Page{{ID: "m"}}, Components: []Component{{ID: "n", Type: "text"}}}
	if got := Lint(text, LintContext{}); len(got) != 0 {
		t.Fatalf("a text-only page has no clock to respond to: %+v", got)
	}
	fixed := Spec{Title: "Rates", Pages: []Page{{ID: "m"}}, Components: []Component{{ID: "s", Type: "stat", Metric: "m", Compare: "none", Fixed: &Fixed{Reason: "Always the latest"}}}}
	if got := Lint(fixed, LintContext{}); len(got) != 0 {
		t.Fatalf("fixed panels with a reason opt out: %+v", got)
	}
	clock := Spec{Title: "Clock", Pages: []Page{{ID: "m"}}, Data: map[string]Query{"a": {From: "postings", Where: `row.date >= params.from`}}, Components: []Component{{ID: "t", Type: "table", Rows: "a"}}}
	if got := rulesOf(Lint(clock, LintContext{})); slices.Contains(got, "time_scope") {
		t.Fatal("reading params.from responds to the clock", got)
	}
	noted := scoped(Component{ID: "k", Type: "kpi", Format: "eur", Compare: "none"})
	if got := rulesOf(Lint(noted, LintContext{})); slices.Contains(got, "kpi_compare") {
		t.Fatal(`compare:"none" states why not`, got)
	}
}

func TestLintTotalsOnlyAdditiveColumns(t *testing.T) {
	spec := scoped(Component{ID: "t", Type: "table", Rows: "a", Columns: []Column{
		{Label: "Name", Type: "text"},
		{Label: "Amount", Type: "money", Format: "eur", Total: true},
		{Label: "Runway", Type: "number", Format: "mo", Total: true},
		{Label: "Share", Type: "pct", Total: true},
		{Label: "Bare", Total: true},
	}})
	issues := Lint(spec, LintContext{})
	if len(issues) != 3 {
		t.Fatalf("%+v", issues)
	}
	for _, i := range issues {
		if i.Rule != "additive_totals" || i.Path == "/components/0/columns/1/total" {
			t.Fatalf("%+v", i)
		}
	}
}

func TestCheckSpecNamesAllowedValues(t *testing.T) {
	spec := Spec{
		Scope:  &Scope{Time: &ScopeTime{Default: "whenever", Presets: []string{"30d", "nope"}}, Compare: "sideways"},
		Answer: &Answer{Metric: "m", Facts: []string{"vibes"}},
		Tests:  []SpecTest{{Ask: "q", Metric: "m", From: "2026-13-01", To: "2026-01-01"}},
		Components: []Component{
			{ID: "a", Type: "stat"},
			{ID: "b", Type: "time_chart", Metric: "m", Mark: "pie", Estimates: "maybe", Compare: "x", X: &XAxis{Type: "log"}, Color: &ColorRule{By: "rank"}, Drill: &Drill{To: "moon"}, Fixed: &Fixed{}},
			{ID: "c", Type: "table", Columns: []Column{{Type: "euros"}}},
		},
	}
	want := []string{"/scope/time/default", "/scope/time/presets/1", "/scope/compare", "/answer/facts/0", "/tests/0", "/tests/0/equals_ledger/accounts", "/components/0/metric", "/components/1/mark", "/components/1/estimates", "/components/1/compare", "/components/1/x/type", "/components/1/color/by", "/components/1/drill/to", "/components/1/fixed/reason", "/components/2/columns/0/type"}
	got := []string{}
	for _, i := range checkSpec(spec) {
		got = append(got, i.Path)
	}
	for _, path := range want {
		if !slices.Contains(got, path) {
			t.Errorf("missing %s in %v", path, got)
		}
	}
	if len(checkSpec(Spec{Components: []Component{{ID: "k", Type: "kpi"}}})) != 0 {
		t.Fatal("a legacy spec must keep validating")
	}
}

func TestComponentCatalogExamplesAreValid(t *testing.T) {
	for _, doc := range ComponentCatalog {
		if doc.Description == "" || len(doc.Fields) == 0 || doc.Example["type"] != doc.Type || len(doc.Lint) == 0 {
			t.Errorf("%s: incomplete catalog entry", doc.Type)
		}
		for _, rule := range doc.Lint {
			if !slices.ContainsFunc(LintRules, func(r LintRule) bool { return r.Rule == rule }) {
				t.Errorf("%s: unknown rule %s", doc.Type, rule)
			}
		}
	}
}
