package dashboard

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"math"
	"regexp"
	"strconv"
	"strings"
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
func (x *execution) tone(expr string, row map[string]any) (string, error) {
	if expr == "" {
		return "", nil
	}
	v, e := x.eval(expr, row)
	if e != nil {
		return "", e
	}
	if v == "ok" || v == "neg" {
		return fmt.Sprint(v), nil
	}
	return "", nil
}
func (x *execution) rows(name string) ([]map[string]any, error) {
	data := x.vars["data"].(map[string]any)
	v, ok := data[name]
	if !ok {
		return nil, fmt.Errorf("unknown query %q", name)
	}
	rows, ok := v.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("query %q must be rows", name)
	}
	return rows, nil
}
func (x *execution) render(c Component) (string, any, error) {
	switch c.Type {
	case "kpi":
		v, e := x.eval(c.Value, nil)
		if e != nil {
			return "", nil, e
		}
		t, e := x.tone(c.Tone, nil)
		if e != nil {
			return "", nil, e
		}
		sub := ""
		if c.Sub != "" {
			s, e := x.eval(c.Sub, nil)
			if e != nil {
				return "", nil, e
			}
			sub = fmt.Sprint(s)
		}
		if c.Delta != "" {
			d, e := x.eval(c.Delta, nil)
			if e != nil {
				return "", nil, e
			}
			sub += " · " + format(d, c.Format)
		}
		return `<div class="kpi"><div class="v ` + t + `">` + escape(format(v, c.Format)) + `</div><div class="d">` + escape(sub) + `</div></div>`, v, nil
	case "text":
		var last error
		text := holes.ReplaceAllStringFunc(c.MD, func(h string) string {
			v, e := x.eval(strings.TrimSpace(h[2:len(h)-2]), nil)
			if e != nil {
				last = e
				return ""
			}
			return fmt.Sprint(v)
		})
		if last != nil {
			return "", nil, last
		}
		safe := escape(text)
		safe = bold.ReplaceAllString(safe, "<strong>$1</strong>")
		safe = strings.ReplaceAll(safe, "\n", "<br>")
		return `<div class="dashboard-note">` + safe + `</div>`, text, nil
	case "table", "scenario_compare":
		rows, e := x.rows(c.Rows)
		if e != nil {
			return "", nil, e
		}
		if len(c.Columns) < 1 || len(c.Columns) > 30 {
			return "", nil, fmt.Errorf("table needs 1..30 columns")
		}
		var b strings.Builder
		b.WriteString(`<div class="tbl" tabindex="0" role="region" aria-label="` + escape(c.Title+" table") + `"><table><thead><tr>`)
		for _, col := range c.Columns {
			b.WriteString(`<th>` + escape(col.Label) + `</th>`)
		}
		b.WriteString(`</tr></thead><tbody>`)
		values := []map[string]any{}
		for _, r := range rows {
			b.WriteString(`<tr>`)
			row := map[string]any{}
			for i, col := range c.Columns {
				v, e := x.eval(col.Value, r)
				if e != nil {
					return "", nil, e
				}
				t, e := x.tone(col.Tone, r)
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
		rows, e := x.rows(c.Rows)
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
			l, e := x.eval(c.Label, r)
			if e != nil {
				return "", nil, e
			}
			av, e := x.eval(c.Actual, r)
			if e != nil {
				return "", nil, e
			}
			lv, e := x.eval(c.Limit, r)
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
	rows, e := x.rows(c.Rows)
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
			return x.eval(axis.Expr, r)
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
		v, e := x.eval(c.Rule.Y, nil)
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
			v, e := x.eval(c.Rule.Label, nil)
			if e != nil {
				return "", nil, e
			}
			ruleLabel = fmt.Sprint(v)
		}
	}
	pad := (high - low) * 0.08
	high += pad
	if low < 0 {
		low -= pad
	}
	sy := func(v float64) float64 { return 12 + (high-v)/(high-low)*208 }
	width := 492.0 / float64(len(categories))
	catIndex := map[string]int{}
	for i, c := range categories {
		catIndex[c] = i
	}
	for i := 0; i <= 4; i++ {
		v := low + (high-low)*float64(i)/4
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
