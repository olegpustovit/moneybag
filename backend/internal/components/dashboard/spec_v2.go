package dashboard

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/semantic"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"maps"
	"slices"
	"strings"
	"time"
)

// One source for validation, catalog_get and the schema enums.
var (
	Marks       = []string{"line", "area", "stack", "bar", "donut"}
	Formats     = []string{"usd", "usd0", "eur", "eur0", "int", "number", "pct", "pct0", "mo", "days", "date"}
	Tones       = []string{"ok", "neg"}
	ColumnTypes = []string{"text", "money", "number", "date", "pct", "days", "count"}
	Policies    = []string{"include", "exclude", "split"}
	Compares    = []string{"previous_period", "previous_year", "none"}
)

// ComponentDoc is one catalog entry: what an agent needs to place the type, with the lint rules that judge it.
type ComponentDoc struct {
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Fields      []string       `json:"fields"`
	Example     map[string]any `json:"example"`
	Lint        []string       `json:"lint"`
}

var metricFields = []string{"metric", "title", "split", "estimates", "compare", "fixed", "drill", "annotate", "color", "x", "mark"}

var ComponentCatalog = []ComponentDoc{
	{"stat", "One number from a metric, with its change, comparison period and sparkline.", []string{"metric", "title", "compare", "estimates", "fixed", "drill"}, map[string]any{"id": "spend", "type": "stat", "metric": "spending", "title": "Spending", "compare": "previous_period", "drill": map[string]any{"to": "entries", "filter": "same_as_page"}}, []string{"kpi_compare", "estimates_declared", "no_raw_ids"}},
	{"time_chart", "A metric over time on a real time axis with zoom, crosshair, annotations and estimate marking.", metricFields, map[string]any{"id": "trend", "type": "time_chart", "metric": "spending", "title": "Spending by category", "split": "category", "x": map[string]any{"type": "time"}, "mark": "stack", "annotate": "trips", "color": map[string]any{"by": "entity"}}, []string{"time_axis", "estimates_declared", "no_raw_ids"}},
	{"ranked_bars", "A metric split by a dimension, sorted, with a comparison marker and click to filter. Replaces most donuts.", metricFields, map[string]any{"id": "where", "type": "ranked_bars", "metric": "spending", "title": "Where it went", "split": "category", "compare": "previous_period", "color": map[string]any{"by": "entity"}}, []string{"estimates_declared", "no_raw_ids"}},
	{"sankey", "Flows through a metric split by a dimension: negative values flow in, positive values flow out.", metricFields, map[string]any{"id": "flow", "type": "sankey", "metric": "cash_flow", "title": "Money in and out", "split": "category"}, []string{"estimates_declared", "no_raw_ids"}},
	{"entries", "The entries behind a metric, first page only, with a link to the rest in Ledger.", []string{"metric", "title", "estimates", "fixed", "drill"}, map[string]any{"id": "behind", "type": "entries", "metric": "spending", "title": "The entries behind these numbers", "drill": map[string]any{"to": "entries", "filter": "same_as_page"}}, []string{"estimates_declared", "no_raw_ids"}},
	{"kpi", "A hand-written CEL value. Prefer stat with a metric.", []string{"value", "title", "format", "delta", "sparkline", "sub", "tone", "compare"}, map[string]any{"id": "cash", "type": "kpi", "title": "Cash", "value": "data.cash[0].total", "format": "eur", "delta": "data.cash[0].change"}, []string{"chart_unit", "kpi_compare", "no_raw_ids"}},
	{"table", "Inline rows of CEL columns, under 50 rows. Longer lists move to a drill-through.", []string{"rows", "title", "columns"}, map[string]any{"id": "pots", "type": "table", "title": "Pots", "rows": "pots", "columns": []map[string]any{{"label": "Pot", "value": "row.pot", "type": "text"}, {"label": "Balance", "value": "row.total", "format": "eur", "type": "money", "total": true}}}, []string{"inline_table_rows", "additive_totals", "no_raw_ids"}},
	{"chart", "A hand-written CEL chart. Prefer time_chart or ranked_bars with a metric.", []string{"rows", "title", "mark", "encoding", "format", "rule", "x"}, map[string]any{"id": "trend", "type": "chart", "title": "Net worth", "rows": "history", "mark": "line", "format": "eur", "x": map[string]any{"type": "time"}, "encoding": map[string]any{"x": map[string]any{"field": "date"}, "y": map[string]any{"field": "net"}}}, []string{"chart_unit", "time_axis", "no_raw_ids"}},
	{"budget_bars", "Budget lines against their limits.", []string{"rows", "title", "label", "actual", "limit"}, map[string]any{"id": "lines", "type": "budget_bars", "title": "This month", "rows": "lines", "label": "row.name", "actual": "row.actual", "limit": "row.limit"}, []string{"no_raw_ids"}},
	{"scenario_compare", "A table comparing model scenarios.", []string{"rows", "title", "columns", "scenarios"}, map[string]any{"id": "plans", "type": "scenario_compare", "title": "Plans", "rows": "plans", "columns": []map[string]any{{"label": "Plan", "value": "row.name", "type": "text"}}}, []string{"additive_totals", "no_raw_ids"}},
	{"text", "A short note. Markdown bold and {{ templates }} work.", []string{"md", "title"}, map[string]any{"id": "note", "type": "text", "md": "**Estimates** come from cash withdrawals."}, []string{"no_raw_ids"}},
}

// ComponentTypes lists every placeable type.
var ComponentTypes = func() []string {
	out := make([]string, len(ComponentCatalog))
	for i, doc := range ComponentCatalog {
		out[i] = doc.Type
	}
	return out
}()

var metricTypes = map[string]bool{"stat": true, "time_chart": true, "ranked_bars": true, "sankey": true, "entries": true}

func validEnum(value string, allowed []string) bool {
	return value == "" || slices.Contains(allowed, value)
}

func enumIssue(path, field, value string, allowed []string) Issue {
	return Issue{Path: path, Msg: fmt.Sprintf("%s %q is not valid", field, value), Field: field, Hint: "use " + strings.Join(allowed, ", ")}
}

// checkSpec validates the v2 fields without evaluating anything, so a typo fails with the allowed values.
func checkSpec(spec Spec) []Issue {
	out := []Issue{}
	if len(spec.Question) > 300 {
		out = append(out, Issue{Path: "/question", Msg: "question exceeds 300 characters", Field: "question"})
	}
	now := time.Now()
	if spec.Scope != nil {
		if spec.Scope.Time != nil {
			if _, _, e := semantic.ParseRange(spec.Scope.Time.Default, now); e != nil {
				out = append(out, Issue{Path: "/scope/time/default", Msg: e.Error(), Field: "default"})
			}
			for i, preset := range spec.Scope.Time.Presets {
				if _, _, e := semantic.ParseRange(preset, now); e != nil {
					out = append(out, Issue{Path: fmt.Sprintf("/scope/time/presets/%d", i), Msg: e.Error(), Field: "presets"})
				}
			}
		}
		if !validEnum(spec.Scope.Compare, Compares) {
			out = append(out, enumIssue("/scope/compare", "compare", spec.Scope.Compare, Compares))
		}
	}
	if spec.Answer != nil {
		if spec.Answer.Metric == "" {
			out = append(out, Issue{Path: "/answer/metric", Msg: "answer needs a metric", Field: "metric", Hint: "read metric ids with metric_list"})
		}
		for i, fact := range spec.Answer.Facts {
			if !slices.Contains(semantic.FactKinds, fact) {
				out = append(out, enumIssue(fmt.Sprintf("/answer/facts/%d", i), "fact", fact, semantic.FactKinds))
			}
		}
	}
	for i, test := range spec.Tests {
		base := fmt.Sprintf("/tests/%d", i)
		if test.Metric == "" || test.Ask == "" {
			out = append(out, Issue{Path: base, Msg: "a test needs ask and metric", Hint: "read metric ids with metric_list"})
		}
		if !validDay(test.From) || !validDay(test.To) || test.From > test.To {
			out = append(out, Issue{Path: base, Msg: "a test needs from and to as YYYY-MM-DD with from at or before to", Field: "from"})
		}
		if len(test.EqualsLedger.Accounts) == 0 {
			out = append(out, Issue{Path: base + "/equals_ledger/accounts", Msg: "equals_ledger needs account prefixes", Field: "accounts", Hint: "such as expenses:"})
		}
	}
	for i, c := range spec.Components {
		base := fmt.Sprintf("/components/%d", i)
		if !slices.Contains(ComponentTypes, c.Type) {
			continue // The renderer reports unknown types with the component's id.
		}
		if metricTypes[c.Type] && c.Metric == "" {
			out = append(out, Issue{Path: base + "/metric", Msg: c.Type + " needs a metric", ComponentID: c.ID, Field: "metric", Hint: "read metric ids with metric_list, or define one with metric_upsert"})
		}
		if c.Mark != "" && !slices.Contains(Marks, c.Mark) {
			i := enumIssue(base+"/mark", "mark", c.Mark, Marks)
			i.ComponentID = c.ID
			out = append(out, i)
		}
		if !validEnum(c.Estimates, Policies) {
			i := enumIssue(base+"/estimates", "estimates", c.Estimates, Policies)
			i.ComponentID = c.ID
			out = append(out, i)
		}
		if !validEnum(c.Compare, Compares) {
			i := enumIssue(base+"/compare", "compare", c.Compare, Compares)
			i.ComponentID = c.ID
			out = append(out, i)
		}
		if c.X != nil && !slices.Contains([]string{"time", "category"}, c.X.Type) {
			i := enumIssue(base+"/x/type", "x.type", c.X.Type, []string{"time", "category"})
			i.ComponentID = c.ID
			out = append(out, i)
		}
		if c.Color != nil && c.Color.By != "entity" {
			i := enumIssue(base+"/color/by", "color.by", c.Color.By, []string{"entity"})
			i.ComponentID = c.ID
			out = append(out, i)
		}
		if c.Drill != nil && c.Drill.To != "entries" {
			i := enumIssue(base+"/drill/to", "drill.to", c.Drill.To, []string{"entries"})
			i.ComponentID = c.ID
			out = append(out, i)
		}
		if c.Fixed != nil {
			if strings.TrimSpace(c.Fixed.Reason) == "" {
				out = append(out, Issue{Path: base + "/fixed/reason", Msg: "fixed needs a reason people can read", ComponentID: c.ID, Field: "reason"})
			}
			if c.Fixed.Range != "" {
				if _, _, e := semantic.ParseRange(c.Fixed.Range, now); e != nil {
					out = append(out, Issue{Path: base + "/fixed/range", Msg: e.Error(), ComponentID: c.ID, Field: "range"})
				}
			}
		}
		for j, col := range c.Columns {
			if !validEnum(col.Type, ColumnTypes) {
				i := enumIssue(fmt.Sprintf("%s/columns/%d/type", base, j), "type", col.Type, ColumnTypes)
				i.ComponentID = c.ID
				out = append(out, i)
			}
		}
	}
	return out
}

// MetricIDs lists the metrics a spec reads, for a share's default source list.
func (s Spec) MetricIDs() []string {
	out := []string{}
	if s.Answer != nil && s.Answer.Metric != "" {
		out = append(out, s.Answer.Metric)
	}
	for _, c := range s.Components {
		if metricTypes[c.Type] && c.Metric != "" && !slices.Contains(out, c.Metric) {
			out = append(out, c.Metric)
		}
	}
	return out
}

func validDay(s string) bool {
	_, e := time.Parse("2006-01-02", s)
	return e == nil
}

// withScope gives a scoped dashboard its clock: explicit params win, otherwise the default range resolves against today.
func withScope(ctx context.Context, spec Spec, issues *[]Issue) Spec {
	if spec.Scope == nil {
		return spec
	}
	params := map[string]any{}
	maps.Copy(params, spec.Params)
	if spec.Scope.Compare != "" {
		if _, set := params["compare"]; !set {
			params["compare"] = spec.Scope.Compare
		}
	}
	from, hasFrom := params["from"].(string)
	to, hasTo := params["to"].(string)
	if spec.Scope.Time != nil && !hasFrom && !hasTo {
		// The range moves with today, so its render must not be cached past today.
		expressions.MarkVolatile(ctx)
		var e error
		from, to, e = semantic.ParseRange(spec.Scope.Time.Default, time.Now())
		if e != nil {
			return spec // checkSpec already reported it.
		}
	}
	if !hasTo && to == "" {
		to = time.Now().UTC().Format("2006-01-02")
	}
	compare, _ := params["compare"].(string)
	compareFrom, compareTo, e := semantic.ComparePeriod(compare, from, to)
	if e != nil {
		*issues = append(*issues, Issue{Path: "/params/compare", Msg: e.Error(), Field: "compare"})
	}
	params["from"], params["to"], params["compare_from"], params["compare_to"] = from, to, compareFrom, compareTo
	spec.Params = params
	return spec
}

// metricRange is a panel's period: its own fixed range, otherwise the page clock.
func (x *execution) metricRange(c Component) (string, string, string, error) {
	params, _ := x.vars["params"].(map[string]any)
	from, _ := params["from"].(string)
	to, _ := params["to"].(string)
	compare := c.Compare
	if compare == "" {
		compare, _ = params["compare"].(string)
	}
	if c.Fixed != nil {
		text := c.Fixed.Range
		if text == "" {
			text = "all"
		}
		var e error
		if from, to, e = semantic.ParseRange(text, time.Now()); e != nil {
			return "", "", "", e
		}
	}
	return from, to, compare, nil
}

// metricAllowed keeps a share's source list authoritative: a metric reads the whole ledger, so a grant must name it.
func (x *execution) metricAllowed(id string) error {
	if x.engine.Metrics == nil {
		return fmt.Errorf("metrics are not available here")
	}
	if guard, ok := x.engine.Reader.(interface{ AllowsMetric(string) bool }); ok && !guard.AllowsMetric(id) {
		return fmt.Errorf("source not granted: metric:%s", id)
	}
	return nil
}

func metricHint(e error) error {
	return &EvalError{Field: "metric", Hint: "read metric ids with metric_list, or define one with metric_upsert", Err: e}
}

// renderMetric returns plain JSON values, because a shared snapshot stores them in Mongo, which would otherwise rename struct fields.
func (x *execution) renderMetric(c Component) (map[string]any, error) {
	if e := x.metricAllowed(c.Metric); e != nil {
		return nil, metricHint(e)
	}
	from, to, compare, e := x.metricRange(c)
	if e != nil {
		return nil, e
	}
	metrics := x.engine.Metrics
	query := semantic.QueryInput{Metric: c.Metric, From: from, To: to, Compare: compare, Split: c.Split, Estimates: c.Estimates}
	where := ""
	if c.Drill != nil && c.Drill.Filter != "same_as_page" {
		where = c.Drill.Filter
	}
	value := map[string]any{"type": c.Type, "metric": c.Metric, "from": from, "to": to}
	if c.Fixed != nil {
		value["fixed"] = c.Fixed.Reason
	}
	if c.Type == "entries" {
		// The entries panel is one page; the drill-through opens the rest, and lint keeps long inline lists out.
		entries, e := metrics.EntriesOf(x.ctx, x.p, query, where, 50)
		if e != nil {
			return nil, metricHint(e)
		}
		value["rows"], value["total"], value["ref"] = entries.Rows, entries.Total, entries.Ref
		return native(value)
	}
	switch c.Type {
	case "stat":
		query.Bucket, query.Split = "auto", ""
	case "time_chart":
		query.Bucket = "auto"
		mark, xType := c.Mark, "time"
		if mark == "" {
			mark = "line"
		}
		if c.X != nil {
			xType = c.X.Type
		}
		value["mark"], value["x"] = mark, map[string]any{"type": xType}
		if c.Annotate != "" {
			notes, e := metrics.Annotations(x.ctx, x.p, c.Annotate, from, to)
			if e != nil {
				return nil, e
			}
			value["annotations"] = notes
		}
	case "ranked_bars", "sankey":
		if query.Split == "" {
			query.Split = "category"
		}
		query.Compare = ""
		if c.Type == "ranked_bars" {
			query.Compare = compare
		}
	}
	res, e := metrics.QueryWhere(x.ctx, x.p, query, where)
	if e != nil {
		return nil, metricHint(e)
	}
	value["unit"], value["label"], value["total"], value["text"] = res.Unit, res.Label, res.Total, res.Text
	value["estimates"], value["formula"], value["entries"] = res.Estimates, res.Formula, res.Entries
	if res.Compare != nil {
		value["compare"] = res.Compare
	}
	if res.Truncated {
		value["truncated"] = true
	}
	switch c.Type {
	case "stat":
		spark := make([]string, len(res.Series))
		for i, point := range res.Series {
			spark[i] = point.Value
		}
		value["sparkline"] = spark
	case "sankey":
		nodes, links := []string{res.Label}, []map[string]any{}
		for _, point := range res.Series {
			nodes = append(nodes, point.Name)
			if strings.HasPrefix(point.Value, "-") {
				links = append(links, map[string]any{"from": point.Name, "to": res.Label, "value": strings.TrimPrefix(point.Value, "-"), "text": strings.TrimPrefix(point.Text, "−"), "estimated": point.Estimated})
			} else {
				links = append(links, map[string]any{"from": res.Label, "to": point.Name, "value": point.Value, "text": point.Text, "estimated": point.Estimated})
			}
		}
		value["nodes"], value["links"] = nodes, links
	default:
		value["series"] = res.Series
	}
	return native(value)
}

// answer computes the page's lead facts into the reserved values key "answer".
func (x *execution) answer(spec Spec, values map[string]any) error {
	if spec.Answer == nil || spec.Answer.Metric == "" {
		return nil
	}
	if _, taken := values["answer"]; taken {
		return fmt.Errorf("component id answer is reserved when the spec has an answer")
	}
	if e := x.metricAllowed(spec.Answer.Metric); e != nil {
		return metricHint(e)
	}
	params, _ := x.vars["params"].(map[string]any)
	from, _ := params["from"].(string)
	to, _ := params["to"].(string)
	compare, _ := params["compare"].(string)
	facts := spec.Answer.Facts
	if len(facts) == 0 {
		facts = []string{"period_change"}
	}
	res, e := x.engine.Metrics.Query(x.ctx, x.p, semantic.QueryInput{Metric: spec.Answer.Metric, From: from, To: to, Compare: compare, Facts: facts})
	if e != nil {
		return metricHint(e)
	}
	sentence := ""
	if len(res.Facts) > 0 {
		sentence = res.Facts[0].Sentence
	}
	values["answer"], e = native(map[string]any{"type": "answer", "metric": res.Metric, "label": res.Label, "unit": res.Unit, "total": res.Total, "text": res.Text, "compare": res.Compare, "sentence": sentence, "facts": res.Facts})
	return e
}
