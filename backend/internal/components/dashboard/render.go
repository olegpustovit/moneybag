package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func escape(v any) string { return html.EscapeString(fmt.Sprint(v)) }

var holes = regexp.MustCompile(`\{\{([^}]+)\}\}`)
var bold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
var componentTemplate = template.Must(template.New("component").Parse(`<section class="cmp" style="grid-column:span {{.Span}}" data-component="{{.ID}}"><div class="ct"><span>{{.Title}}</span><span class="id">{{.Type}} · {{.ID}}</span></div>{{.Body}}</section>`))

func componentHTML(c Component, span int, body string) string {
	var b bytes.Buffer
	_ = componentTemplate.Execute(&b, struct {
		Span            int
		ID, Title, Type string
		Body            template.HTML
	}{span, c.ID, c.Title, c.Type, template.HTML(body)})
	return b.String()
}

// formats are the names allowed after "|" in a {{ ref | format }} placeholder.
var formats = map[string]bool{"usd": true, "usd0": true, "eur": true, "eur0": true, "pct0": true, "date": true}
var holeFormat = regexp.MustCompile(`^(.*[^|\s])\s*\|\s*([a-z0-9]+)\s*$`)

func parseHole(h string) (expr, format string) {
	h = strings.TrimSpace(h)
	if m := holeFormat.FindStringSubmatch(h); m != nil {
		return m[1], m[2]
	}
	return h, ""
}

// template resolves {{ cel [| format] }} placeholders; any failing placeholder is an error naming the field.
func (x *execution) template(field, s string) (string, error) {
	var last error
	out := holes.ReplaceAllStringFunc(s, func(h string) string {
		expr, f := parseHole(h[2 : len(h)-2])
		if f != "" && !formats[f] {
			last = &EvalError{Field: field, Expr: expr, Hint: "formats: usd, usd0, eur, eur0, pct0, date", Err: fmt.Errorf("unknown format %q", f)}
			return ""
		}
		v, e := x.evalAt(field, expr, nil)
		if e != nil {
			last = e
			return ""
		}
		switch {
		case f == "date":
			text := fmt.Sprint(v)
			if t, e := time.Parse(time.RFC3339, text); e == nil {
				return t.Format("2006-01-02")
			}
			return text[:min(10, len(text))]
		case f != "":
			return format(v, f)
		}
		return fmt.Sprint(v)
	})
	return out, last
}

// resolveText expands placeholders in the title and column labels.
func (x *execution) resolveText(c Component) (Component, error) {
	var e error
	if c.Title, e = x.template("title", c.Title); e != nil {
		return c, e
	}
	c.Columns = append([]Column(nil), c.Columns...)
	for i := range c.Columns {
		if c.Columns[i].Label, e = x.template(fmt.Sprintf("columns/%d/label", i), c.Columns[i].Label); e != nil {
			return c, e
		}
	}
	return c, nil
}
func format(v any, f string) string {
	n, e := numeric(v)
	if e != nil {
		return fmt.Sprint(v)
	}
	switch f {
	case "eur0":
		return "€" + groupNumber(n, 0)
	case "usd0":
		return "$" + groupNumber(n, 0)
	case "eur":
		return "€" + groupNumber(n, 2)
	case "usd":
		return "$" + groupNumber(n, 2)
	case "int":
		return groupNumber(n, 0)
	case "mo":
		return groupNumber(n, 1) + " mo"
	case "pct0":
		return groupNumber(n, 0) + "%"
	case "pct":
		return groupNumber(n, 1) + "%"
	default:
		if n == math.Trunc(n) {
			return groupNumber(n, 0)
		}
		return groupNumber(n, 2)
	}
}
func groupNumber(n float64, d int) string {
	sign := ""
	if n < 0 {
		sign = "−"
		n = -n
	}
	s := strconv.FormatFloat(n, 'f', d, 64)
	parts := strings.Split(s, ".")
	integer := parts[0]
	for i := len(integer) - 3; i > 0; i -= 3 {
		integer = integer[:i] + "," + integer[i:]
	}
	if len(parts) > 1 {
		integer += "." + parts[1]
	}
	return sign + integer
}
func (x *execution) tone(field, expr string, row map[string]any) (string, error) {
	if expr == "" {
		return "", nil
	}
	v, e := x.evalAt(field, expr, row)
	if e != nil {
		return "", e
	}
	if v == "ok" || v == "neg" {
		return fmt.Sprint(v), nil
	}
	return "", nil
}

// rows resolves a component's rows: a data source name, otherwise a CEL expression returning a list.
func (x *execution) rows(c Component) ([]map[string]any, error) {
	data := x.vars["data"].(map[string]any)
	v, ok := data[c.Rows]
	if !ok {
		names := []string{}
		for k := range data {
			names = append(names, k)
		}
		sort.Strings(names)
		hint := "rows must name a data source (" + strings.Join(names, ", ") + ") or be a CEL expression returning a list of rows"
		if c.Rows == "" {
			return nil, &EvalError{Field: "rows", Hint: hint, Err: fmt.Errorf("rows is empty")}
		}
		var e error
		if v, e = x.eval(c.Rows, nil); e != nil {
			return nil, &EvalError{Field: "rows", Expr: c.Rows, Hint: hint, Err: fmt.Errorf("unknown data source and not a valid expression: %w", e)}
		}
	}
	if rows, ok := v.([]map[string]any); ok {
		return rows, nil
	}
	var rows []map[string]any
	b, _ := json.Marshal(v)
	if e := json.Unmarshal(b, &rows); e != nil {
		return nil, &EvalError{Field: "rows", Expr: c.Rows, Hint: "rows must resolve to a list of objects", Err: fmt.Errorf("rows %q is not a list of rows", c.Rows)}
	}
	return rows, nil
}
func (x *execution) render(c Component) (string, any, error) {
	switch c.Type {
	case "kpi":
		v, e := x.evalAt("value", c.Value, nil)
		if e != nil {
			return "", nil, e
		}
		t, e := x.tone("tone", c.Tone, nil)
		if e != nil {
			return "", nil, e
		}
		sub := ""
		if c.Sub != "" {
			s, e := x.evalAt("sub", c.Sub, nil)
			if e != nil {
				return "", nil, e
			}
			if sub, e = x.template("sub", fmt.Sprint(s)); e != nil {
				return "", nil, e
			}
		}
		if c.Delta != "" {
			d, e := x.evalAt("delta", c.Delta, nil)
			if e != nil {
				return "", nil, e
			}
			sub += " · " + format(d, c.Format)
		}
		return `<div class="kpi"><div class="v ` + t + `">` + escape(format(v, c.Format)) + `</div><div class="d">` + escape(sub) + `</div></div>`, v, nil
	case "text":
		text, e := x.template("md", c.MD)
		if e != nil {
			return "", nil, e
		}
		safe := escape(text)
		safe = bold.ReplaceAllString(safe, "<strong>$1</strong>")
		safe = strings.ReplaceAll(safe, "\n", "<br>")
		return `<div class="dashboard-note">` + safe + `</div>`, text, nil
	case "table", "scenario_compare":
		rows, e := x.rows(c)
		if e != nil {
			return "", nil, e
		}
		if len(c.Columns) < 1 || len(c.Columns) > 30 {
			return "", nil, fmt.Errorf("table needs 1..30 columns")
		}
		var b strings.Builder
		b.WriteString(`<div class="tbl" tabindex="0" role="region" aria-label="` + escape(c.Title+" table") + `"><table><thead><tr>`)
		for i, col := range c.Columns {
			if i > 0 {
				b.WriteString(`<th class="r">` + escape(col.Label) + `</th>`)
			} else {
				b.WriteString(`<th>` + escape(col.Label) + `</th>`)
			}
		}
		b.WriteString(`</tr></thead><tbody>`)
		values := []map[string]any{}
		for _, r := range rows {
			b.WriteString(`<tr>`)
			row := map[string]any{}
			for i, col := range c.Columns {
				v, e := x.evalAt(fmt.Sprintf("columns/%d/value", i), col.Value, r)
				if e != nil {
					return "", nil, e
				}
				t, e := x.tone(fmt.Sprintf("columns/%d/tone", i), col.Tone, r)
				if e != nil {
					return "", nil, e
				}
				class := t
				if i > 0 {
					class += " r"
				}
				b.WriteString(`<td class="` + class + `">` + escape(format(v, col.Format)) + `</td>`)
				row[col.Label] = v
			}
			values = append(values, row)
			b.WriteString(`</tr>`)
		}
		if len(rows) == 0 {
			b.WriteString(`<tr><td colspan="` + strconv.Itoa(len(c.Columns)) + `">No matching rows.</td></tr>`)
		}
		b.WriteString(`</tbody></table></div>`)
		return b.String(), values, nil
	case "budget_bars":
		rows, e := x.rows(c)
		if e != nil {
			return "", nil, e
		}
		type bar struct {
			Label         string
			Actual, Limit float64
		}
		bars := []bar{}
		maxValue := 1.0
		for _, r := range rows {
			l, e := x.evalAt("label", c.Label, r)
			if e != nil {
				return "", nil, e
			}
			av, e := x.evalAt("actual", c.Actual, r)
			if e != nil {
				return "", nil, e
			}
			lv, e := x.evalAt("limit", c.Limit, r)
			if e != nil {
				return "", nil, e
			}
			a, e := numeric(av)
			if e != nil {
				return "", nil, e
			}
			limit, e := numeric(lv)
			if e != nil || a < 0 || limit < 0 {
				return "", nil, fmt.Errorf("budget bars need nonnegative numbers")
			}
			bars = append(bars, bar{fmt.Sprint(l), a, limit})
			maxValue = math.Max(maxValue, math.Max(a, limit))
		}
		var b strings.Builder
		for _, r := range bars {
			class := ""
			if r.Actual > r.Limit {
				class = "over"
			}
			fmt.Fprintf(&b, `<div class="bbar"><span>%s</span><span class="tr"><i class="%s" style="width:%.3f%%"></i><b style="left:calc(%.3f%% - 1px)"></b></span><span class="v">%s / %s</span></div>`, escape(r.Label), class, r.Actual/maxValue*100, r.Limit/maxValue*100, escape(format(r.Actual, "eur0")), escape(format(r.Limit, "eur0")))
		}
		b.WriteString(`<div class="legend"><span><i class="legend-ok"></i>within limit</span><span><i class="legend-neg"></i>over limit</span><span>│ limit</span></div>`)
		return b.String(), bars, nil
	case "chart":
		return x.chart(c)
	default:
		return "", nil, fmt.Errorf("unknown component type %q", c.Type)
	}
}

type chartPoint struct {
	X      string  `json:"x"`
	Y      float64 `json:"y"`
	Series string  `json:"series,omitempty"`
}

func (x *execution) chart(c Component) (string, any, error) {
	if c.Encoding == nil {
		return "", nil, fmt.Errorf("chart encoding required")
	}
	if c.Mark != "bar" && c.Mark != "line" && c.Mark != "area" && c.Mark != "stack" && c.Mark != "donut" {
		return "", nil, fmt.Errorf("chart mark must be bar, line, area, stack or donut")
	}
	rows, e := x.rows(c)
	if e != nil {
		return "", nil, e
	}
	if len(rows) > 600 {
		return "", nil, fmt.Errorf("chart exceeds 600 points")
	}
	if len(rows) == 0 {
		return `<p class="cap">No data for this chart.</p>`, []chartPoint{}, nil
	}
	field := func(axis Axis, r map[string]any) (any, error) {
		if axis.Expr != "" {
			return x.evalAt("encoding/expr", axis.Expr, r)
		}
		v, ok := r[axis.Field]
		if !ok {
			return nil, fmt.Errorf("chart field %q missing", axis.Field)
		}
		return v, nil
	}
	points := []chartPoint{}
	categories := []string{}
	series := []string{}
	byX := map[string]bool{}
	bySeries := map[string]bool{}
	for _, r := range rows {
		v, e := field(c.Encoding.X, r)
		if e != nil {
			return "", nil, e
		}
		y, e := field(c.Encoding.Y, r)
		if e != nil {
			return "", nil, e
		}
		n, e := numeric(y)
		if e != nil {
			return "", nil, e
		}
		seriesName := ""
		if c.Encoding.Color != nil {
			v, e := field(*c.Encoding.Color, r)
			if e != nil {
				return "", nil, e
			}
			seriesName = fmt.Sprint(v)
		}
		p := chartPoint{X: fmt.Sprint(v), Y: n, Series: seriesName}
		points = append(points, p)
		if !byX[p.X] {
			categories = append(categories, p.X)
			byX[p.X] = true
		}
		if !bySeries[p.Series] {
			series = append(series, p.Series)
			bySeries[p.Series] = true
		}
	}
	color := func(i int) string { return fmt.Sprintf("var(--c%d)", i%5) }
	colorIndex := map[string]int{}
	for i, s := range series {
		colorIndex[s] = i
	}
	var b strings.Builder
	b.WriteString(`<div class="chart" tabindex="0" role="region" aria-label="` + escape(c.Title+" chart; scroll to see all values") + `"><svg viewBox="0 0 560 250" role="img" aria-label="` + escape(c.Title) + `"><title>` + escape(c.Title) + `</title>`)
	if c.Mark == "donut" {
		total := 0.0
		for _, p := range points {
			if p.Y < 0 {
				return "", nil, fmt.Errorf("donut values must be nonnegative")
			}
			total += p.Y
		}
		if total <= 0 {
			return `<p class="cap">No positive values to display.</p>`, points, nil
		}
		offset := 0.0
		for i, p := range points {
			fraction := p.Y / total
			fmt.Fprintf(&b, `<circle cx="150" cy="125" r="80" fill="none" stroke="%s" stroke-width="34" pathLength="100" stroke-dasharray="%.6f %.6f" stroke-dashoffset="%.6f" transform="rotate(-90 150 125)"><title>%s: %s</title></circle>`, color(i), fraction*100, 100-fraction*100, -offset*100, escape(p.X), escape(format(p.Y, c.Format)))
			if i < 12 {
				fmt.Fprintf(&b, `<rect x="280" y="%d" width="10" height="10" fill="%s"/><text x="300" y="%d">%s · %s</text>`, 20+i*18, color(i), 29+i*18, escape(p.X), escape(format(p.Y, c.Format)))
			}
			offset += fraction
		}
		b.WriteString(`</svg></div>`)
		b.WriteString(chartData(c.Title, points, nil, ""))
		return b.String(), points, nil
	}
	low, high := 0.0, 1.0
	totals := map[string]float64{}
	for _, p := range points {
		low = math.Min(low, p.Y)
		high = math.Max(high, p.Y)
		totals[p.X] += math.Max(0, p.Y)
	}
	if c.Mark == "stack" {
		for _, v := range totals {
			high = math.Max(high, v)
		}
	}
	rule := 0.0
	hasRule := c.Rule != nil
	ruleLabel := ""
	if hasRule {
		v, e := x.evalAt("rule/y", c.Rule.Y, nil)
		if e != nil {
			return "", nil, e
		}
		rule, e = numeric(v)
		if e != nil {
			return "", nil, e
		}
		high = math.Max(high, rule)
		low = math.Min(low, rule)
		if c.Rule.Label != "" {
			v, e := x.evalAt("rule/label", c.Rule.Label, nil)
			if e != nil {
				return "", nil, e
			}
			ruleLabel = fmt.Sprint(v)
		}
	}
	if high == low {
		high = low + 1
	}
	step := niceStep((high - low) / 4)
	low, high = math.Floor(low/step)*step, math.Ceil(high/step)*step
	sy := func(v float64) float64 { return 12 + (high-v)/(high-low)*208 }
	width := 492.0 / float64(len(categories))
	catIndex := map[string]int{}
	for i, c := range categories {
		catIndex[c] = i
	}
	for v := low; v <= high+step/2; v += step {
		y := sy(v)
		fmt.Fprintf(&b, `<line x1="56" x2="548" y1="%.3f" y2="%.3f" class="chart-grid"/><text x="48" y="%.3f" text-anchor="end">%s</text>`, y, y, y+4, escape(format(v, "int")))
	}
	if c.Mark == "line" || c.Mark == "area" {
		for _, s := range series {
			var path strings.Builder
			first, last := -1, -1
			for _, p := range points {
				if p.Series != s {
					continue
				}
				i := catIndex[p.X]
				px := 56 + (float64(i)+0.5)*width
				command := "L"
				if first < 0 {
					command = "M"
					first = i
				}
				last = i
				fmt.Fprintf(&path, "%s%.3f %.3f ", command, px, sy(p.Y))
			}
			if first < 0 {
				continue
			}
			if c.Mark == "area" {
				fmt.Fprintf(&b, `<path d="%sL%.3f %.3f L%.3f %.3f Z" fill="%s" fill-opacity=".12"/>`, path.String(), 56+(float64(last)+0.5)*width, sy(0), 56+(float64(first)+0.5)*width, sy(0), color(colorIndex[s]))
			}
			fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="%s" stroke-width="2"/>`, path.String(), color(colorIndex[s]))
			for _, p := range points {
				if p.Series == s {
					fmt.Fprintf(&b, `<circle cx="%.3f" cy="%.3f" r="2.5" fill="%s"><title>%s %s: %s</title></circle>`, 56+(float64(catIndex[p.X])+0.5)*width, sy(p.Y), color(colorIndex[s]), escape(p.X), escape(s), escape(format(p.Y, c.Format)))
				}
			}
		}
	}
	if c.Mark == "bar" || c.Mark == "stack" {
		stack := map[string]float64{}
		for _, p := range points {
			bw := math.Min(32, width*0.7)
			px := 56 + (float64(catIndex[p.X])+0.5)*width - bw/2
			base := 0.0
			if c.Mark == "stack" {
				if p.Y < 0 {
					return "", nil, fmt.Errorf("stack values must be nonnegative")
				}
				base = stack[p.X]
				stack[p.X] += p.Y
			} else if len(series) > 1 {
				bw /= float64(len(series))
				px += float64(colorIndex[p.Series]) * bw
			}
			top, bottom := sy(base+p.Y), sy(base)
			if top > bottom {
				top, bottom = bottom, top
			}
			fmt.Fprintf(&b, `<rect x="%.3f" y="%.3f" width="%.3f" height="%.3f" fill="%s"><title>%s %s: %s</title></rect>`, px, top, bw, math.Max(0.5, bottom-top), color(colorIndex[p.Series]), escape(p.X), escape(p.Series), escape(format(p.Y, c.Format)))
		}
	}
	for i, c := range categories {
		if i%max(1, (len(categories)+7)/8) == 0 || i == len(categories)-1 {
			fmt.Fprintf(&b, `<text x="%.3f" y="242" text-anchor="middle">%s</text>`, 56+(float64(i)+0.5)*width, escape(c))
		}
	}
	if hasRule {
		fmt.Fprintf(&b, `<line x1="56" x2="548" y1="%.3f" y2="%.3f" stroke="var(--text2)" stroke-dasharray="5 4"/><text x="544" y="%.3f" text-anchor="end">%s</text>`, sy(rule), sy(rule), sy(rule)-5, escape(ruleLabel))
	}
	b.WriteString(`</svg>`)
	if len(series) > 1 {
		b.WriteString(`<div class="legend">`)
		for i, s := range series {
			fmt.Fprintf(&b, `<span><i style="background:%s"></i>%s</span>`, color(i), escape(s))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
	var reference *float64
	if hasRule {
		reference = &rule
	}
	b.WriteString(chartData(c.Title, points, reference, ruleLabel))
	return b.String(), points, nil
}

func chartData(title string, points []chartPoint, reference *float64, label string) string {
	var b strings.Builder
	b.WriteString(`<details class="chart-data"><summary>View chart data</summary><div class="tbl" tabindex="0" role="region" aria-label="` + escape(title+" chart data") + `"><table><caption>` + escape(title) + `</caption><thead><tr><th scope="col">Series</th><th scope="col">Category</th><th scope="col">Value</th></tr></thead><tbody>`)
	for _, p := range points {
		series := p.Series
		if series == "" {
			series = "Value"
		}
		b.WriteString(`<tr><th scope="row">` + escape(series) + `</th><td>` + escape(p.X) + `</td><td class="r">` + escape(strconv.FormatFloat(p.Y, 'f', -1, 64)) + `</td></tr>`)
	}
	b.WriteString(`</tbody>`)
	if reference != nil {
		b.WriteString(`<tfoot><tr><th colspan="2" scope="row">Reference: ` + escape(label) + `</th><td class="r">` + escape(strconv.FormatFloat(*reference, 'f', -1, 64)) + `</td></tr></tfoot>`)
	}
	b.WriteString(`</table></div></details>`)
	return b.String()
}

// niceStep rounds a raw axis interval up to 1, 2 or 5 times a power of ten so ticks read as round numbers.
func niceStep(raw float64) float64 {
	magnitude := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, f := range []float64{1, 2, 5} {
		if raw <= f*magnitude {
			return f * magnitude
		}
	}
	return 10 * magnitude
}
