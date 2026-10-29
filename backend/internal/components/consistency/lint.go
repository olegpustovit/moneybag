package consistency

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
)

// Literal is a numeric literal found in a spec text or CEL field.
type Literal struct {
	Dashboard   string  `json:"dashboard,omitempty"`
	ComponentID string  `json:"component_id"`
	Field       string  `json:"field"`
	Literal     string  `json:"literal"`
	Value       float64 `json:"-"`
}

// allowed literals are structural constants (zero, one, percent, months, pay periods, weeks).
var allowed = map[float64]bool{0: true, 1: true, 100: true, 12: true, 26: true, 52: true}
var isoDate = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// shortcut: a number glued to a letter, underscore or dot (x2, a.5) is an identifier part, not a literal.
var number = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.])(\d[\d,]*(?:\.\d+)?)`)

// ScanText returns the non-allowlisted numeric literals in s. ISO dates are ignored; thousands separators are folded.
func ScanText(s string) []Literal {
	var out []Literal
	for _, m := range number.FindAllStringSubmatch(isoDate.ReplaceAllString(s, " "), -1) {
		text := strings.ReplaceAll(m[1], ",", "")
		v, e := strconv.ParseFloat(text, 64)
		if e != nil || allowed[v] {
			continue
		}
		out = append(out, Literal{Literal: text, Value: v})
	}
	return out
}

// SpecLiterals lists numeric literals in titles, sub, labels, notes and CEL of one spec.
// The spec has no constants block, so only the fixed allowlist applies.
func SpecLiterals(spec dashboard.Spec) []Literal {
	var out []Literal
	scan := func(id, field, text string) {
		for _, l := range ScanText(text) {
			l.Dashboard, l.ComponentID, l.Field = spec.ID, id, field
			out = append(out, l)
		}
	}
	scan("", "title", spec.Title)
	for _, pg := range spec.Pages {
		scan("page:"+pg.ID, "title", pg.Title)
	}
	for k, v := range spec.Computed {
		scan("computed:"+k, "expr", v)
	}
	for k, q := range spec.Data {
		scan("data:"+k, "where", q.Where)
		scan("data:"+k, "n", q.N)
		for f, v := range q.Derive {
			scan("data:"+k, "derive."+f, v)
		}
	}
	for _, c := range spec.Components {
		for f, v := range map[string]string{"title": c.Title, "sub": c.Sub, "label": c.Label, "md": c.MD, "value": c.Value, "delta": c.Delta, "tone": c.Tone, "actual": c.Actual, "limit": c.Limit, "metric": c.Metric} {
			scan(c.ID, f, v)
		}
		for i, col := range c.Columns {
			scan(c.ID, fmt.Sprintf("columns.%d.label", i), col.Label)
			scan(c.ID, fmt.Sprintf("columns.%d.value", i), col.Value)
			scan(c.ID, fmt.Sprintf("columns.%d.tone", i), col.Tone)
		}
		if c.Encoding != nil {
			scan(c.ID, "encoding.x", c.Encoding.X.Expr)
			scan(c.ID, "encoding.y", c.Encoding.Y.Expr)
			if c.Encoding.Color != nil {
				scan(c.ID, "encoding.color", c.Encoding.Color.Expr)
			}
		}
		if c.Rule != nil {
			scan(c.ID, "rule.y", c.Rule.Y)
			scan(c.ID, "rule.label", c.Rule.Label)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.ComponentID+a.Field+a.Literal < b.ComponentID+b.Field+b.Literal
	})
	return out
}

var dataRef = regexp.MustCompile(`\bdata\.([A-Za-z_]\w*)`)
var computedRef = regexp.MustCompile(`\bcomputed\.([A-Za-z_]\w*)`)

// SpecOrphans reports unused data queries and computed values, and references to things that do not exist.
// tables and scenarios are the ids that exist for table:<id> and scenario:<id> sources and component scenario lists.
func SpecOrphans(spec dashboard.Spec, tables, scenarios map[string]bool) []string {
	var text strings.Builder
	add := func(s string) { text.WriteString(s + "\n") }
	for _, v := range spec.Computed {
		add(v)
	}
	usedFrom := map[string]bool{}
	var out []string
	for k, q := range spec.Data {
		add(q.Where)
		add(q.N)
		for _, v := range q.Derive {
			add(v)
		}
		usedFrom[q.From] = true
		switch {
		case strings.HasPrefix(q.From, "table:") && !tables[strings.TrimPrefix(q.From, "table:")]:
			out = append(out, fmt.Sprintf("data %s reads missing table %s", k, q.From))
		case strings.HasPrefix(q.From, "scenario:") && !scenarios[strings.TrimPrefix(q.From, "scenario:")]:
			out = append(out, fmt.Sprintf("data %s reads missing scenario %s", k, q.From))
		}
	}
	usedRows := map[string]bool{}
	ids := map[string]bool{}
	for _, c := range spec.Components {
		ids[c.ID] = true
		usedRows[c.Rows] = true
		for _, v := range []string{c.Value, c.Delta, c.Sub, c.Tone, c.Label, c.Actual, c.Limit, c.Metric, c.MD} {
			add(v)
		}
		for _, col := range c.Columns {
			add(col.Value)
			add(col.Tone)
		}
		if c.Encoding != nil {
			add(c.Encoding.X.Expr)
			add(c.Encoding.Y.Expr)
			if c.Encoding.Color != nil {
				add(c.Encoding.Color.Expr)
			}
		}
		if c.Rule != nil {
			add(c.Rule.Y)
		}
		if _, ok := spec.Data[c.Rows]; c.Rows != "" && !ok {
			out = append(out, fmt.Sprintf("component %s rows reads missing query %s", c.ID, c.Rows))
		}
		for _, s := range c.Scenarios {
			if !scenarios[s] {
				out = append(out, fmt.Sprintf("component %s lists missing scenario %s", c.ID, s))
			}
		}
	}
	body := text.String()
	refData, refComputed := map[string]bool{}, map[string]bool{}
	for _, m := range dataRef.FindAllStringSubmatch(body, -1) {
		refData[m[1]] = true
	}
	for _, m := range computedRef.FindAllStringSubmatch(body, -1) {
		refComputed[m[1]] = true
	}
	for k := range refData {
		if _, ok := spec.Data[k]; !ok {
			out = append(out, "expression reads missing data."+k)
		}
	}
	for k := range refComputed {
		if _, ok := spec.Computed[k]; !ok {
			out = append(out, "expression reads missing computed."+k)
		}
	}
	for k := range spec.Data {
		if !refData[k] && !usedRows[k] && !usedFrom[k] {
			out = append(out, "data."+k+" is never referenced")
		}
	}
	for k := range spec.Computed {
		if !refComputed[k] {
			out = append(out, "computed."+k+" is never referenced")
		}
	}
	for _, pg := range spec.Pages {
		for _, r := range pg.Layout {
			for _, col := range r.Cols {
				if !ids[col.Component] {
					out = append(out, fmt.Sprintf("page %s places missing component %s", pg.ID, col.Component))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// Input is one model-input value (income amount, budget limit, assumption, rate quote).
type Input struct {
	Name  string
	Value float64
	// Exact inputs (rates) only match a literal with the same value; others also match a two-decimal literal equal to the rounded value.
	Exact bool
}

// Duplicate says where a literal repeats a model input.
type Duplicate struct {
	Where   string `json:"where"`
	Literal string `json:"literal"`
	Input   string `json:"input"`
}

// Duplicates finds literals equal to a model input. Allowlisted structural values never count.
func Duplicates(inputs []Input, lits []Literal, where func(Literal) string) []Duplicate {
	var out []Duplicate
	for _, l := range lits {
		decimals := 0
		if i := strings.Index(l.Literal, "."); i >= 0 {
			decimals = len(l.Literal) - i - 1
		}
		for _, in := range inputs {
			if allowed[in.Value] {
				continue
			}
			if l.Value == in.Value || !in.Exact && decimals <= 2 && l.Value == math.Round(in.Value*100)/100 {
				out = append(out, Duplicate{where(l), l.Literal, in.Name})
				break
			}
		}
	}
	return out
}
