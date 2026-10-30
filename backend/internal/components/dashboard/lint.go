package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/semantic"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// LintIssue is one judged rule. Errors block publish; warnings do not.
type LintIssue struct {
	Rule        string `json:"rule"`
	Severity    string `json:"severity"`
	Path        string `json:"path"`
	ComponentID string `json:"component_id,omitempty"`
	Msg         string `json:"msg"`
	Fix         string `json:"fix"`
}

// LintRule is the catalog entry for one rule.
type LintRule struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Question string `json:"question"`
}

var LintRules = []LintRule{
	{"time_scope", "error", "Does the page respond to the time range?"},
	{"chart_unit", "error", "Does every chart and KPI declare a unit?"},
	{"kpi_compare", "error", "Does every KPI have a comparison, or say why not?"},
	{"time_axis", "error", "Is time on a time axis?"},
	{"max_panels", "error", "Are there at most 12 panels per page?"},
	{"inline_table_rows", "error", "Are inline tables under 50 rows?"},
	{"no_raw_ids", "error", "Is visible text free of identifiers once templates have run?"},
	{"additive_totals", "error", "Do only additive columns total?"},
	{"estimates_declared", "error", "Does each number say how it treats estimates?"},
	{"metric_logic_copied", "warning", "Is shared logic a named metric rather than copied CEL?"},
}

// LintContext is what the rules need beyond the spec; the evaluated page comes from Execute, so templates are judged by what they render to.
type LintContext struct {
	Rendered     Rendered
	Metrics      map[string]semantic.Metric
	HasEstimated bool
}

var (
	exprPath      = regexp.MustCompile(`\b(row|data|computed|params)\.[A-Za-z_]`)
	accountInText = regexp.MustCompile(`\b(assets|liabilities|income|expenses|equity):[a-z0-9_-]+`)
	ulidInText    = regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`)
	snakeInText   = regexp.MustCompile(`\b[a-z0-9]+(_[a-z0-9]+)+\b`)
	stampInText   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}`)
	dateLike      = regexp.MustCompile(`^\d{4}-\d{2}(-\d{2}(T.*)?)?$`)
	paramsClock   = regexp.MustCompile(`\bparams\.(from|to)\b`)
	literal       = regexp.MustCompile(`"[^"]*"|'[^']*'|\b\d+(\.\d+)?\b`)
)

// identifier names what a visible string leaks, or returns "" when it reads as prose.
func identifier(text string) string {
	for _, check := range []struct {
		pattern *regexp.Regexp
		kind    string
	}{{exprPath, "a CEL path"}, {accountInText, "an account id"}, {ulidInText, "a record id"}, {stampInText, "a raw timestamp"}, {snakeInText, "a code name"}} {
		if found := check.pattern.FindString(text); found != "" {
			return fmt.Sprintf("%s (%s)", check.kind, found)
		}
	}
	if strings.Contains(text, "{{") {
		return "an unevaluated template"
	}
	return ""
}

func placed(page Page) []string {
	out := []string{}
	for _, row := range page.Layout {
		for _, col := range row.Cols {
			out = append(out, col.Component)
		}
	}
	return out
}

// Lint applies the ten rules to a spec and its evaluated page.
func Lint(spec Spec, lc LintContext) []LintIssue {
	out := []LintIssue{}
	add := func(rule, path, component, msg, fix string) {
		severity := "error"
		for _, r := range LintRules {
			if r.Rule == rule {
				severity = r.Severity
			}
		}
		out = append(out, LintIssue{rule, severity, path, component, msg, fix})
	}
	lintTimeScope(spec, add)
	for i, page := range spec.Pages {
		if n := len(placed(page)); n > 12 {
			add("max_panels", fmt.Sprintf("/pages/%d/layout", i), "", fmt.Sprintf("page %q has %d panels", page.Title, n), "Split it into pages or dashboards, or move detail into a drill-through")
		}
	}
	textChecks := []struct{ path, text string }{{"/title", spec.Title}, {"/question", spec.Question}}
	if len(lc.Rendered.Pages) == len(spec.Pages) {
		for i, page := range lc.Rendered.Pages {
			textChecks = append(textChecks, struct{ path, text string }{fmt.Sprintf("/pages/%d/title", i), page.Title})
		}
	}
	for _, check := range textChecks {
		if found := identifier(check.text); found != "" {
			add("no_raw_ids", check.path, "", "visible text shows "+found, "Write it in plain words")
		}
	}
	for i, c := range spec.Components {
		lintComponent(spec, i, c, lc, add)
	}
	lintEstimates(spec, lc, add)
	lintCopies(spec, add)
	return out
}

func lintTimeScope(spec Spec, add func(rule, path, component, msg, fix string)) {
	if spec.Scope != nil && spec.Scope.Time != nil {
		return
	}
	// shortcut: a query or expression reading the clock counts for the whole page, without tracing which panels depend on it; trace with graph.go if a page mixes both.
	raw, _ := json.Marshal(struct {
		Data       map[string]Query
		Computed   map[string]string
		Components []Component
	}{spec.Data, spec.Computed, spec.Components})
	if paramsClock.Match(raw) {
		return
	}
	for _, c := range spec.Components {
		if c.Type != "text" && (c.Fixed == nil || c.Fixed.Reason == "") {
			add("time_scope", "/scope", "", "the page has no clock, so its panels ignore the time range", `Declare scope:{time:{default:"12mo"}}, or give each panel fixed:{reason}`)
			return
		}
	}
}

func lintComponent(spec Spec, index int, c Component, lc LintContext, add func(rule, path, component, msg, fix string)) {
	base := fmt.Sprintf("/components/%d", index)
	resolved, evaluated := lc.Rendered.Resolved[c.ID]
	if !evaluated {
		resolved = c
	}
	value, _ := lc.Rendered.Values[c.ID].(map[string]any)
	if (c.Type == "kpi" || c.Type == "chart") && c.Format == "" {
		add("chart_unit", base+"/format", c.ID, fmt.Sprintf("%s %q declares no unit", c.Type, c.ID), "Use stat or time_chart with a metric, which brings its unit, or set format: "+strings.Join(Formats[:9], ", "))
	}
	if c.Type == "kpi" && c.Delta == "" && c.Compare == "" {
		add("kpi_compare", base+"/delta", c.ID, fmt.Sprintf("KPI %q has no comparison", c.ID), `Add delta, or set compare:"none" to state that no comparison makes sense`)
	}
	if c.Type == "stat" && c.Compare == "" && (spec.Scope == nil || spec.Scope.Compare == "") {
		add("kpi_compare", base+"/compare", c.ID, fmt.Sprintf("stat %q has no comparison", c.ID), `Set scope.compare or compare, or compare:"none" to state that no comparison makes sense`)
	}
	lintTimeAxis(base, c, value, add)
	lintVisibleText(base, c, resolved, value, add)
	if rows, ok := value["rows"].([]map[string]any); ok && c.Type == "table" && len(rows) >= 50 {
		add("inline_table_rows", base+"/rows", c.ID, fmt.Sprintf("table %q shows %d rows inline", c.ID, len(rows)), "Use an entries panel, or link to Ledger with the filter through drill")
	}
	for j, col := range c.Columns {
		if !col.Total {
			continue
		}
		path := fmt.Sprintf("%s/columns/%d/total", base, j)
		switch {
		case col.Type == "" || !slices.Contains([]string{"money", "number", "count"}, col.Type):
			add("additive_totals", path, c.ID, fmt.Sprintf("column %q totals, but its type %q does not add up", col.Label, col.Type), "Set type to money, number or count, or remove total")
		case slices.Contains([]string{"pct", "pct0", "mo", "days", "date"}, col.Format):
			add("additive_totals", path, c.ID, fmt.Sprintf("column %q totals, but its format %q does not add up", col.Label, col.Format), "Remove total")
		}
	}
	if metricTypes[c.Type] {
		metric, known := lc.Metrics[c.Metric]
		if known && c.Estimates == "" && metric.Estimates == "" {
			add("estimates_declared", base+"/estimates", c.ID, fmt.Sprintf("%q does not say how it treats estimates", c.ID), "Set estimates to include, exclude or split on the metric or the panel")
		}
	}
}

func lintTimeAxis(base string, c Component, value map[string]any, add func(rule, path, component, msg, fix string)) {
	if c.Type == "time_chart" && c.X != nil && c.X.Type == "category" {
		add("time_axis", base+"/x/type", c.ID, fmt.Sprintf("time chart %q is on a category axis", c.ID), `Set x:{type:"time"} or use ranked_bars`)
	}
	if c.Type != "chart" || c.Mark == "donut" {
		return
	}
	points, _ := value["points"].([]chartPoint)
	if len(points) == 0 {
		return
	}
	dates := true
	for _, point := range points {
		dates = dates && dateLike.MatchString(point.X)
	}
	switch timeAxis := c.X != nil && c.X.Type == "time"; {
	case dates && !timeAxis:
		add("time_axis", base+"/x", c.ID, fmt.Sprintf("chart %q plots dates as text", c.ID), `Set x:{type:"time"}`)
	case !dates && timeAxis:
		add("time_axis", base+"/encoding/x", c.ID, fmt.Sprintf("chart %q has a time axis but its x values are not dates", c.ID), "Make the x field YYYY-MM or YYYY-MM-DD, or use a category axis")
	}
}

func lintVisibleText(base string, c, resolved Component, value map[string]any, add func(rule, path, component, msg, fix string)) {
	check := func(field, text string) {
		if found := identifier(text); found != "" {
			add("no_raw_ids", base+"/"+field, c.ID, fmt.Sprintf("%s %q shows %s", c.Type, c.ID, found), "Use a human label; name raw keys with label_set and use metric labels")
		}
	}
	check("title", resolved.Title)
	for j, col := range resolved.Columns {
		check(fmt.Sprintf("columns/%d/label", j), col.Label)
	}
	if sub, ok := value["sub"].(string); ok {
		check("sub", sub)
	}
	if html, ok := value["html"].(string); ok {
		check("md", html)
	}
	if series, ok := value["series"].([]map[string]any); ok {
		for _, item := range series {
			if name, ok := item["name"].(string); ok && name != "" {
				check("encoding/color", name)
			}
		}
	}
	// Cell text can be prose, so tables are only held to ids that are never prose.
	if rows, ok := value["rows"].([]map[string]any); ok {
		for _, row := range rows {
			for _, cell := range slices.Sorted(maps.Keys(row)) {
				text, ok := row[cell].(string)
				if !ok {
					continue
				}
				if found := accountInText.FindString(text) + ulidInText.FindString(text); found != "" {
					add("no_raw_ids", base+"/columns", c.ID, fmt.Sprintf("table %q shows the id %s", c.ID, found), "Show the name instead, or leave ids to Details")
					return
				}
			}
		}
	}
}

func lintEstimates(spec Spec, lc LintContext, add func(rule, path, component, msg, fix string)) {
	if !lc.HasEstimated {
		return
	}
	for _, name := range slices.Sorted(maps.Keys(spec.Data)) {
		q := spec.Data[name]
		if q.From != "postings" && q.From != "journal" && q.From != "effective_entries" {
			continue
		}
		raw, _ := json.Marshal(q)
		if !strings.Contains(string(raw), "estimated") {
			out := fmt.Sprintf("query %q may mix estimated accounts in silently", name)
			add("estimates_declared", "/data/"+name, "", out, "Use a metric with an estimates policy, or filter on estimated accounts in where")
		}
	}
}

// lintCopies flags near-identical CEL: the same shape with only its literals changed.
func lintCopies(spec Spec, add func(rule, path, component, msg, fix string)) {
	normalize := func(expr string) string {
		return strings.Join(strings.Fields(literal.ReplaceAllString(expr, "?")), "")
	}
	seen := map[string]string{}
	report := func(shape, path, component string) {
		if len(shape) < 24 {
			return
		}
		if first, ok := seen[shape]; ok {
			add("metric_logic_copied", path, component, "this logic repeats "+first, "Promote it to a named metric with metric_upsert")
			return
		}
		seen[shape] = path
	}
	for _, name := range slices.Sorted(maps.Keys(spec.Data)) {
		q := spec.Data[name]
		if q.Where == "" {
			continue
		}
		aggs := slices.Sorted(maps.Values(q.Agg))
		report(q.From+normalize(q.Where)+normalize(strings.Join(aggs, ",")), "/data/"+name+"/where", "")
	}
	for i, c := range spec.Components {
		for _, field := range []struct{ name, expr string }{{"value", c.Value}, {"delta", c.Delta}, {"sparkline", c.Sparkline}} {
			if field.expr != "" {
				report(normalize(field.expr), fmt.Sprintf("/components/%d/%s", i, field.name), c.ID)
			}
		}
	}
}

// LintSpec evaluates the spec and judges it.
func (s *Service) LintSpec(ctx context.Context, p principal.Principal, spec Spec) ([]LintIssue, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	rendered, e := s.engine.Execute(ctx, p, spec)
	if e != nil {
		return nil, e
	}
	lc := LintContext{Rendered: rendered, Metrics: map[string]semantic.Metric{}}
	if s.engine.Metrics != nil {
		metrics, e := s.engine.Metrics.Metrics(ctx, p)
		if e != nil {
			return nil, e
		}
		for _, m := range metrics {
			lc.Metrics[m.ID] = m
		}
		if lc.HasEstimated, e = s.engine.Metrics.HasEstimatedAccounts(ctx, p); e != nil {
			return nil, e
		}
	}
	return Lint(spec, lc), nil
}

// TestResult is one saved question checked against the ledger.
type TestResult struct {
	Ask    string `json:"ask"`
	OK     bool   `json:"ok"`
	Metric string `json:"metric"`
	Got    string `json:"got,omitempty"`
	Want   string `json:"want,omitempty"`
	Msg    string `json:"msg,omitempty"`
}
type TestReport struct {
	Passed  int          `json:"passed"`
	Failed  int          `json:"failed"`
	Results []TestResult `json:"results"`
}

// RunTests answers each spec.tests question with its metric and with the plain ledger; the two must match to the cent.
func (s *Service) RunTests(ctx context.Context, p principal.Principal, spec Spec) (TestReport, error) {
	if e := p.Require("ledger:read"); e != nil {
		return TestReport{}, e
	}
	report := TestReport{Results: []TestResult{}}
	for _, test := range spec.Tests {
		result := TestResult{Ask: test.Ask, Metric: test.Metric}
		if s.engine.Metrics == nil {
			return TestReport{}, fmt.Errorf("metrics are not available here")
		}
		got, want, e := s.engine.Metrics.LedgerCheck(ctx, p, test.Metric, test.From, test.To, test.EqualsLedger.Accounts, test.EqualsLedger.Exclude)
		switch {
		case e != nil:
			result.Msg = e.Error()
		case got.Text != want.Text:
			result.Got, result.Want = got.Text, want.Text
			result.Msg = fmt.Sprintf("the metric says %s and the ledger says %s; check the metric's accounts, exclude, where and estimates, or the test's equals_ledger", got.Text, want.Text)
		default:
			result.Got, result.Want, result.OK = got.Text, want.Text, true
		}
		if result.OK {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Results = append(report.Results, result)
	}
	return report, nil
}

// Gate is what stands between a draft and publishing; publish tools refuse unless Passed.
type Gate struct {
	Version    int         `json:"version"`
	Passed     bool        `json:"passed"`
	Validation Validation  `json:"validation"`
	Lint       []LintIssue `json:"lint"`
	Tests      TestReport  `json:"tests"`
	Render     string      `json:"render,omitempty"`
}

// PublishGate runs validation, lint, tests and the render check without writing.
func (s *Service) PublishGate(ctx context.Context, p principal.Principal, id string, version int) (Gate, error) {
	if e := p.Require("dash:write"); e != nil {
		return Gate{}, e
	}
	ref := "draft"
	if version > 0 {
		ref = fmt.Sprint(version)
	}
	v, e := s.Get(ctx, p, id, ref)
	if e != nil {
		return Gate{}, e
	}
	gate := Gate{Version: v.Version}
	rendered, e := s.engine.Execute(ctx, p, v.Spec)
	if e != nil {
		return Gate{}, e
	}
	gate.Validation = rendered.Validation
	if gate.Lint, e = s.LintSpec(ctx, p, v.Spec); e != nil {
		return Gate{}, e
	}
	if gate.Tests, e = s.RunTests(ctx, p, v.Spec); e != nil {
		return Gate{}, e
	}
	if s.RenderCheck != nil {
		if e = s.RenderCheck(ctx, p, id, v.Version); e != nil {
			gate.Render = e.Error()
		}
	}
	gate.Passed = rendered.Validation.OK && gate.Tests.Failed == 0 && gate.Render == ""
	for _, issue := range gate.Lint {
		gate.Passed = gate.Passed && issue.Severity != "error"
	}
	return gate, nil
}

// Refusal words a failed gate so the next call can fix it.
func (g Gate) Refusal() string {
	reasons := []string{}
	for _, i := range g.Validation.Errors {
		reasons = append(reasons, "validation: "+i.String())
	}
	for _, i := range g.Lint {
		if i.Severity == "error" {
			reasons = append(reasons, fmt.Sprintf("lint %s at %s: %s. Fix: %s", i.Rule, i.Path, i.Msg, i.Fix))
		}
	}
	for _, r := range g.Tests.Results {
		if !r.OK {
			reasons = append(reasons, fmt.Sprintf("test %q: %s", r.Ask, r.Msg))
		}
	}
	if g.Render != "" {
		reasons = append(reasons, "render: "+g.Render)
	}
	return fmt.Sprintf("publish gate failed for version %d, and the live dashboard is unchanged: %s", g.Version, strings.Join(reasons, "; "))
}
