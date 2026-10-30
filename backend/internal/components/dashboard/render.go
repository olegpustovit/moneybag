package dashboard

import (
	"encoding/json"
	"fmt"
	"html"
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
func (x *execution) render(c Component) (any, error) {
	if metricTypes[c.Type] {
		return x.renderMetric(c)
	}
	switch c.Type {
	case "kpi":
		v, e := x.evalAt("value", c.Value, nil)
		if e != nil {
			return nil, e
		}
		t, e := x.tone("tone", c.Tone, nil)
		if e != nil {
			return nil, e
		}
		sub := ""
		if c.Sub != "" {
			// Expand placeholders in the source, never in the result: evaluated ledger text may contain "{{".
			src, e := x.template("sub", c.Sub)
			if e != nil {
				return nil, e
			}
			s, e := x.evalAt("sub", src, nil)
			if e != nil {
				return nil, e
			}
			sub = fmt.Sprint(s)
		}
		values := map[string]any{"type": "kpi", "value": v, "text": format(v, c.Format), "tone": t}
		if c.Delta != "" {
			d, e := x.evalAt("delta", c.Delta, nil)
			if e != nil {
				return nil, e
			}
			direction := "neutral"
			if n, err := numeric(d); err == nil {
				switch {
				case n > 0:
					direction = "up"
				case n < 0:
					direction = "down"
				default:
					direction = "unchanged"
				}
			}
			values["delta"] = map[string]any{"text": format(d, c.Format), "direction": direction}
			values["sub_text"] = sub
			sub += " · " + format(d, c.Format)
		}
		values["sub"] = sub
		if c.Sparkline != "" {
			result, err := x.evalAt("sparkline", c.Sparkline, nil)
			if err != nil {
				return nil, err
			}
			items, ok := result.([]any)
			if !ok || len(items) > 600 {
				return nil, &EvalError{Field: "sparkline", Expr: c.Sparkline, Err: fmt.Errorf("sparkline must be a list of at most 600 numbers")}
			}
			numbers := make([]float64, len(items))
			labels := make([]string, len(items))
			for i, item := range items {
				numbers[i], err = numeric(item)
				if err != nil {
					return nil, &EvalError{Field: "sparkline", Expr: c.Sparkline, Err: fmt.Errorf("sparkline point %d must be a finite number", i+1)}
				}
				labels[i] = format(numbers[i], c.Format)
			}
			values["sparkline"] = numbers
			values["sparkline_text"] = labels
		}
		return values, nil
	case "text":
		text, e := x.template("md", c.MD)
		if e != nil {
			return nil, e
		}
		safe := escape(text)
		safe = bold.ReplaceAllString(safe, "<strong>$1</strong>")
		safe = strings.ReplaceAll(safe, "\n", "<br>")
		return map[string]any{"type": "text", "html": safe}, nil
	case "table", "scenario_compare":
		rows, e := x.rows(c)
		if e != nil {
			return nil, e
		}
		if len(c.Columns) < 1 || len(c.Columns) > 30 {
			return nil, fmt.Errorf("table needs 1..30 columns")
		}
		columns := []map[string]any{}
		for i, col := range c.Columns {
			align := "left"
			switch col.Format {
			case "usd", "usd0", "eur", "eur0", "int", "number", "pct", "pct0", "mo":
				align = "right"
			}
			switch col.Type {
			case "money", "number", "pct", "days", "count":
				align = "right"
			case "text", "date":
				align = "left"
			}
			columns = append(columns, map[string]any{"key": fmt.Sprintf("c%d", i), "label": col.Label, "format": col.Format, "align": align, "type": col.Type, "total": col.Total})
		}
		values := []map[string]any{}
		for _, r := range rows {
			row := map[string]any{}
			for i, col := range c.Columns {
				v, e := x.evalAt(fmt.Sprintf("columns/%d/value", i), col.Value, r)
				if e != nil {
					return nil, e
				}
				t, e := x.tone(fmt.Sprintf("columns/%d/tone", i), col.Tone, r)
				if e != nil {
					return nil, e
				}
				row[fmt.Sprintf("c%d", i)] = map[string]any{"v": v, "t": format(v, col.Format), "tone": t}
			}
			values = append(values, row)
		}
		return map[string]any{"type": c.Type, "columns": columns, "rows": values}, nil
	case "budget_bars":
		rows, e := x.rows(c)
		if e != nil {
			return nil, e
		}
		values := make([]map[string]any, 0, len(rows))
		for _, r := range rows {
			l, e := x.evalAt("label", c.Label, r)
			if e != nil {
				return nil, e
			}
			av, e := x.evalAt("actual", c.Actual, r)
			if e != nil {
				return nil, e
			}
			lv, e := x.evalAt("limit", c.Limit, r)
			if e != nil {
				return nil, e
			}
			actual, e := numeric(av)
			if e != nil {
				return nil, e
			}
			limit, e := numeric(lv)
			if e != nil || actual < 0 || limit < 0 {
				return nil, fmt.Errorf("budget bars need nonnegative numbers")
			}
			status := "within"
			if actual > limit {
				status = "over"
			}
			value := map[string]any{"label": fmt.Sprint(l), "actual": actual, "limit": limit, "status": status, "actual_text": format(actual, "eur0"), "limit_text": format(limit, "eur0")}
			if avg, err := numeric(r["avg6"]); err == nil {
				value["average_text"] = format(avg, "eur0")
				if months, err := numeric(r["window_months"]); err == nil && months > 0 && months == math.Trunc(months) {
					value["average_label"] = fmt.Sprintf("%g-month average", months)
				}
			}
			switch accounts := r["accounts"].(type) {
			case []string:
				if accounts != nil {
					value["accounts"] = accounts
				}
			case []any:
				ids := make([]string, 0, len(accounts))
				for _, account := range accounts {
					id, ok := account.(string)
					if !ok {
						ids = nil
						break
					}
					ids = append(ids, id)
				}
				if ids != nil {
					value["accounts"] = ids
				}
			}
			values = append(values, value)
		}
		return map[string]any{"type": "budget", "rows": values}, nil
	case "chart":
		return x.chart(c)
	default:
		return nil, &EvalError{Field: "type", Hint: "types: " + strings.Join(ComponentTypes, ", ") + "; read catalog_get for each one's fields", Err: fmt.Errorf("unknown component type %q", c.Type)}
	}
}

type chartPoint struct {
	X      string  `json:"x"`
	Y      float64 `json:"y"`
	YText  string  `json:"y_text"`
	Series string  `json:"series"`
}

func chartValue(c Component, points []chartPoint, names []string, reference *float64, label string) map[string]any {
	field := c.Encoding.X.Field
	if field == "" {
		field = c.Encoding.X.Expr
	}
	kind := "category"
	if len(points) > 0 {
		kind = "date"
		for _, point := range points {
			if _, err := time.Parse("2006-01-02", point.X); err != nil {
				kind = "month"
				break
			}
		}
		if kind == "month" {
			for _, point := range points {
				if _, err := time.Parse("2006-01", point.X); err != nil {
					kind = "category"
					break
				}
			}
		}
	}
	series := make([]map[string]any, 0, len(names))
	for i, name := range names {
		series = append(series, map[string]any{"name": name, "color_index": i % 5})
	}
	xType := ""
	if c.X != nil {
		xType = c.X.Type
	}
	out := map[string]any{"type": "chart", "mark": c.Mark, "x": map[string]any{"field": field, "kind": kind, "type": xType}, "series": series, "points": points, "y_format": c.Format}
	if reference != nil {
		out["rule"] = map[string]any{"y": *reference, "label": label}
	}
	return out
}

func (x *execution) chart(c Component) (any, error) {
	if c.Encoding == nil {
		return nil, fmt.Errorf("chart encoding required")
	}
	if c.Mark != "bar" && c.Mark != "line" && c.Mark != "area" && c.Mark != "stack" && c.Mark != "donut" {
		return nil, fmt.Errorf("chart mark must be bar, line, area, stack or donut")
	}
	rows, e := x.rows(c)
	if e != nil {
		return nil, e
	}
	if len(rows) > 5000 {
		return nil, fmt.Errorf("chart exceeds 5000 points")
	}
	if len(rows) == 0 {
		return chartValue(c, []chartPoint{}, nil, nil, ""), nil
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
	series := []string{}
	bySeries := map[string]bool{}
	for _, r := range rows {
		v, e := field(c.Encoding.X, r)
		if e != nil {
			return nil, e
		}
		y, e := field(c.Encoding.Y, r)
		if e != nil {
			return nil, e
		}
		n, e := numeric(y)
		if e != nil {
			return nil, e
		}
		seriesName := ""
		if c.Encoding.Color != nil {
			v, e := field(*c.Encoding.Color, r)
			if e != nil {
				return nil, e
			}
			seriesName = fmt.Sprint(v)
		}
		p := chartPoint{X: fmt.Sprint(v), Y: n, YText: format(n, c.Format), Series: seriesName}
		points = append(points, p)
		if !bySeries[p.Series] {
			series = append(series, p.Series)
			bySeries[p.Series] = true
		}
	}
	for _, p := range points {
		if p.Y < 0 && c.Mark == "donut" {
			return nil, fmt.Errorf("donut values must be nonnegative")
		}
		if p.Y < 0 && c.Mark == "stack" {
			return nil, fmt.Errorf("stack values must be nonnegative")
		}
	}
	if c.Mark == "donut" {
		return chartValue(c, points, series, nil, ""), nil
	}
	var reference *float64
	ruleLabel := ""
	if c.Rule != nil {
		v, e := x.evalAt("rule/y", c.Rule.Y, nil)
		if e != nil {
			return nil, e
		}
		rule, e := numeric(v)
		if e != nil {
			return nil, e
		}
		reference = &rule
		if c.Rule.Label != "" {
			v, e := x.evalAt("rule/label", c.Rule.Label, nil)
			if e != nil {
				return nil, e
			}
			ruleLabel = fmt.Sprint(v)
		}
	}
	return chartValue(c, points, series, reference, ruleLabel), nil
}
