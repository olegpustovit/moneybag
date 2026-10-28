package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

type paramsKey struct{}

type Reader interface {
	Rows(context.Context, principal.Principal, string) ([]map[string]any, error)
}
type Engine struct {
	Expr   *expressions.Engine
	Reader Reader
}
type execution struct {
	ctx    context.Context
	engine *Engine
	p      principal.Principal
	vars   map[string]any
	cost   uint64
	count  int
}

func (x *execution) eval(expr string, row map[string]any) (any, error) {
	x.count++
	if x.count > 20000 {
		return nil, fmt.Errorf("page evaluation exceeds 20000 calls")
	}
	vars := map[string]any{}
	for k, v := range x.vars {
		vars[k] = v
	}
	if row != nil {
		vars["row"] = row
	}
	r, e := x.engine.Expr.Eval(x.ctx, expr, vars)
	x.cost += r.Cost
	if x.cost > 1000000 {
		return nil, fmt.Errorf("page evaluation exceeds one million operations")
	}
	return r.Value, e
}
func native(v any) (map[string]any, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	var out map[string]any
	e = json.Unmarshal(b, &out)
	return out, e
}
func numeric(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		if !math.IsInf(n, 0) && !math.IsNaN(n) {
			return n, nil
		}
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	}
	return 0, fmt.Errorf("expected finite number")
}
func (x *execution) query(q Query) (any, error) {
	var rows []map[string]any
	var e error
	data := x.vars["data"].(map[string]any)
	switch {
	case q.From == "range":
		n, e := x.eval(q.N, nil)
		if e != nil {
			return nil, e
		}
		f, e := numeric(n)
		if e != nil || f < 0 || f > 600 || f != math.Trunc(f) {
			return nil, fmt.Errorf("range must be integer 0..600")
		}
		rows = []map[string]any{}
		for i := 0; i < int(f); i++ {
			rows = append(rows, map[string]any{"i": float64(i)})
		}
	case strings.HasPrefix(q.From, "query:"):
		v, ok := data[strings.TrimPrefix(q.From, "query:")]
		if !ok {
			return nil, fmt.Errorf("query dependency not ready")
		}
		b, _ := json.Marshal(v)
		if e = json.Unmarshal(b, &rows); e != nil {
			return nil, fmt.Errorf("query source must be rows")
		}
	default:
		rows, e = x.engine.Reader.Rows(x.ctx, x.p, q.From)
		if e != nil {
			return nil, e
		}
	}
	if e := boundedData(rows); e != nil {
		return nil, e
	}
	if len(rows) > 5000 {
		return nil, fmt.Errorf("source exceeds 5000 rows")
	}
	out := []map[string]any{}
	for _, r := range rows {
		if q.Where != "" {
			v, e := x.eval(q.Where, r)
			if e != nil {
				return nil, e
			}
			ok, isBool := v.(bool)
			if !isBool {
				return nil, fmt.Errorf("where must produce boolean")
			}
			if !ok {
				continue
			}
		}
		pending := map[string]string{}
		originals := map[string]any{}
		for k, v := range q.Derive {
			pending[k] = v
			if old, ok := r[k]; ok {
				originals[k] = old
				delete(r, k)
			}
		}
		for len(pending) > 0 {
			progress := false
			var last error
			for k, expr := range pending {
				if old, ok := originals[k]; ok {
					r[k] = old
				}
				v, e := x.eval(expr, r)
				delete(r, k)
				if e != nil {
					last = e
					continue
				}
				r[k] = v
				delete(pending, k)
				progress = true
			}
			if !progress {
				return nil, last
			}
		}
		out = append(out, r)
	}
	if len(q.Group) > 0 || len(q.Agg) > 0 {
		groups := map[string][]map[string]any{}
		keys := []string{}
		for _, r := range out {
			values := []any{}
			for _, k := range q.Group {
				values = append(values, r[k])
			}
			b, _ := json.Marshal(values)
			key := string(b)
			if _, ok := groups[key]; !ok {
				keys = append(keys, key)
			}
			groups[key] = append(groups[key], r)
		}
		if len(out) == 0 && len(q.Group) == 0 {
			keys = []string{"[]"}
			groups["[]"] = nil
		}
		out = []map[string]any{}
		for _, key := range keys {
			group := groups[key]
			r := map[string]any{}
			if len(group) > 0 {
				for _, k := range q.Group {
					r[k] = group[0][k]
				}
			}
			for k, agg := range q.Agg {
				match := aggPattern.FindStringSubmatch(agg)
				if match == nil {
					return nil, fmt.Errorf("aggregation must be sum/avg/min/max/count/last(expression)")
				}
				op, expr := match[1], match[2]
				if op == "count" {
					r[k] = float64(len(group))
					continue
				}
				var total float64
				var last any
				for i, row := range group {
					v, e := x.eval(expr, row)
					if e != nil {
						return nil, e
					}
					last = v
					if op == "last" {
						continue
					}
					n, e := numeric(v)
					if e != nil {
						return nil, e
					}
					switch op {
					case "sum", "avg":
						total += n
					case "min":
						if i == 0 || n < total {
							total = n
						}
					case "max":
						if i == 0 || n > total {
							total = n
						}
					}
				}
				if op == "avg" && len(group) > 0 {
					total /= float64(len(group))
				}
				if op == "last" {
					r[k] = last
				} else {
					r[k] = total
				}
			}
			out = append(out, r)
		}
	}
	for i := len(q.Sort) - 1; i >= 0; i-- {
		key := strings.TrimLeft(q.Sort[i], "-+")
		desc := strings.HasPrefix(q.Sort[i], "-")
		sort.SliceStable(out, func(a, b int) bool {
			cmp := strings.Compare(fmt.Sprint(out[a][key]), fmt.Sprint(out[b][key]))
			if n, e := numeric(out[a][key]); e == nil {
				if m, e := numeric(out[b][key]); e == nil {
					cmp = 0
					if n < m {
						cmp = -1
					} else if n > m {
						cmp = 1
					}
				}
			}
			if desc {
				return cmp > 0
			}
			return cmp < 0
		})
	}
	if q.Limit < 0 || q.Limit > 5000 {
		return nil, fmt.Errorf("query limit must be 1..5000")
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	if q.As == "map" {
		m := map[string]any{}
		for _, r := range out {
			k, ok := r[q.Key].(string)
			if !ok {
				return nil, fmt.Errorf("map key must be string")
			}
			m[k] = r[q.Value]
		}
		return m, nil
	}
	if q.As != "" {
		return nil, fmt.Errorf("as must be map")
	}
	if e := boundedData(out); e != nil {
		return nil, e
	}
	return out, nil
}

var aggPattern = regexp.MustCompile(`^(sum|avg|min|max|count|last)\((.*)\)$`)

func (e *Engine) Execute(ctx context.Context, p principal.Principal, spec Spec) (Rendered, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, paramsKey{}, spec.Params)
	result := Rendered{Values: map[string]any{}, Validation: Validation{OK: true, Errors: []Issue{}, Warnings: []Issue{}}}
	issue := func(path string, err error) {
		result.Validation.Errors = append(result.Validation.Errors, Issue{path, err.Error()})
		result.Validation.OK = false
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return result, err
	}
	if len(raw) > 256*1024 {
		return result, fmt.Errorf("dashboard exceeds 256 KiB")
	}
	if len(spec.Components) > 100 || len(spec.Data) > 50 || len(spec.Computed) > 100 || len(spec.Pages) < 1 || len(spec.Pages) > 20 {
		return result, fmt.Errorf("dashboard needs 1..20 pages, at most 100 components, 50 queries and 100 computed expressions")
	}
	count := 0
	walkExpressions(spec, func(path, expr string) {
		count++
		if _, err := e.Expr.Compile(expr); err != nil {
			issue(path, err)
		}
	})
	if count > 200 {
		issue("/", fmt.Errorf("dashboard exceeds 200 expressions"))
	}
	if !result.Validation.OK {
		return result, nil
	}
	x := &execution{ctx: ctx, engine: e, p: p, vars: map[string]any{"data": map[string]any{}, "params": spec.Params, "computed": map[string]any{}, "rates": map[string]any{}}}
	if spec.Params == nil {
		x.vars["params"] = map[string]any{}
	}
	x.load(spec.Data, spec.Computed, issue)
	if !result.Validation.OK {
		return result, nil
	}
	components := map[string]Component{}
	html := map[string]string{}
	for i, c := range spec.Components {
		path := fmt.Sprintf("/components/%d", i)
		if c.ID == "" {
			issue(path+"/id", fmt.Errorf("id required"))
			continue
		}
		if _, ok := components[c.ID]; ok {
			issue(path+"/id", fmt.Errorf("duplicate component id"))
			continue
		}
		components[c.ID] = c
		body, value, err := x.render(c)
		if err != nil {
			issue(path, err)
			continue
		}
		if len(body) > 2*1024*1024 {
			issue(path, fmt.Errorf("component exceeds 2 MiB"))
			continue
		}
		html[c.ID] = body
		result.Values[c.ID] = value
		if e := boundedData(result.Values); e != nil {
			issue(path, e)
			return result, nil
		}
	}
	var output strings.Builder
	used := map[string]bool{}
	pageIDs := map[string]bool{}
	for pi, pg := range spec.Pages {
		if pg.ID == "" || pageIDs[pg.ID] {
			issue(fmt.Sprintf("/pages/%d/id", pi), fmt.Errorf("unique page id required"))
		}
		pageIDs[pg.ID] = true
		output.WriteString(`<section class="dashboard-page">`)
		if len(spec.Pages) > 1 {
			output.WriteString(`<h2>` + escape(pg.Title) + `</h2>`)
		}
		output.WriteString(`<div class="dash">`)
		for ri, r := range pg.Layout {
			span := 0
			for _, col := range r.Cols {
				span += col.Span
				if col.Span < 1 || col.Span > 12 {
					issue(fmt.Sprintf("/pages/%d/layout/%d", pi, ri), fmt.Errorf("span must be 1..12"))
				}
				c, ok := components[col.Component]
				if !ok {
					issue(fmt.Sprintf("/pages/%d/layout/%d", pi, ri), fmt.Errorf("component %s not found", col.Component))
					continue
				}
				used[c.ID] = true
				fragment := componentHTML(c, col.Span, html[c.ID])
				if output.Len()+len(fragment) > 2*1024*1024 {
					issue("/pages", fmt.Errorf("rendered dashboard exceeds 2 MiB"))
					result.HTML = ""
					return result, nil
				}
				output.WriteString(fragment)
			}
			if span > 12 {
				issue(fmt.Sprintf("/pages/%d/layout/%d", pi, ri), fmt.Errorf("row exceeds 12 columns"))
			}
		}
		output.WriteString(`</div></section>`)
	}
	for id := range components {
		if !used[id] {
			result.Validation.Warnings = append(result.Validation.Warnings, Issue{"/components", "component " + id + " is not placed"})
		}
	}
	result.Validation.Cost = x.cost
	result.HTML = output.String()
	if ctx.Err() != nil {
		issue("/", fmt.Errorf("dashboard exceeded 1 second evaluation budget"))
	}
	return result, nil
}
func walkExpressions(s Spec, fn func(string, string)) {
	emit := func(p, v string) {
		if v != "" {
			fn(p, v)
		}
	}
	for k, q := range s.Data {
		base := "/data/" + k
		emit(base+"/where", q.Where)
		emit(base+"/n", q.N)
		for f, v := range q.Derive {
			emit(base+"/derive/"+f, v)
		}
		for f, v := range q.Agg {
			if m := aggPattern.FindStringSubmatch(v); m != nil && m[1] != "count" {
				emit(base+"/agg/"+f, m[2])
			}
		}
	}
	for k, v := range s.Computed {
		emit("/computed/"+k, v)
	}
	for i, c := range s.Components {
		base := fmt.Sprintf("/components/%d", i)
		for k, v := range map[string]string{"value": c.Value, "delta": c.Delta, "sub": c.Sub, "tone": c.Tone, "label": c.Label, "actual": c.Actual, "limit": c.Limit, "metric": c.Metric} {
			emit(base+"/"+k, v)
		}
		for j, col := range c.Columns {
			emit(fmt.Sprintf("%s/columns/%d/value", base, j), col.Value)
			emit(fmt.Sprintf("%s/columns/%d/tone", base, j), col.Tone)
		}
		if c.Encoding != nil {
			emit(base+"/encoding/x/expr", c.Encoding.X.Expr)
			emit(base+"/encoding/y/expr", c.Encoding.Y.Expr)
			if c.Encoding.Color != nil {
				emit(base+"/encoding/color/expr", c.Encoding.Color.Expr)
			}
		}
		if c.Rule != nil {
			emit(base+"/rule/y", c.Rule.Y)
			emit(base+"/rule/label", c.Rule.Label)
		}
		for _, m := range holes.FindAllStringSubmatch(c.MD, -1) {
			emit(base+"/md", strings.TrimSpace(m[1]))
		}
	}
}

func (x *execution) load(data map[string]Query, computedExprs map[string]string, issue func(string, error)) {
	pending := map[string]Query{}
	for k, q := range data {
		pending[k] = q
	}
	for len(pending) > 0 {
		progress := false
		errs := map[string]error{}
		for k, q := range pending {
			v, err := x.query(q)
			if err != nil {
				errs[k] = err
				continue
			}
			x.vars["data"].(map[string]any)[k] = v
			if e := boundedData(x.vars["data"]); e != nil {
				issue("/data", e)
				return
			}
			delete(pending, k)
			progress = true
		}
		if !progress {
			for k, err := range errs {
				issue("/data/"+k, err)
			}
			break
		}
	}
	computed := map[string]string{}
	for k, v := range computedExprs {
		computed[k] = v
	}
	for len(computed) > 0 {
		progress := false
		errs := map[string]error{}
		for k, expr := range computed {
			v, err := x.eval(expr, nil)
			if err != nil {
				errs[k] = err
				continue
			}
			x.vars["computed"].(map[string]any)[k] = v
			delete(computed, k)
			progress = true
		}
		if !progress {
			for k, err := range errs {
				issue("/computed/"+k, err)
			}
			break
		}
	}
}

func (e *Engine) Evaluate(ctx context.Context, p principal.Principal, expr string, data map[string]Query, params map[string]any) (expressions.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, paramsKey{}, params)
	spec := Spec{Data: data, Computed: map[string]string{"result": expr}, Params: params}
	raw, err := json.Marshal(spec)
	if err != nil {
		return expressions.Result{}, err
	}
	if len(raw) > 256*1024 || len(data) > 50 {
		return expressions.Result{}, fmt.Errorf("expression context exceeds query or byte budget")
	}
	count := 0
	walkExpressions(spec, func(_ string, expr string) {
		count++
		if _, e := e.Expr.Compile(expr); e != nil && err == nil {
			err = e
		}
	})
	if count > 200 {
		return expressions.Result{}, fmt.Errorf("expression context exceeds 200 expressions")
	}
	if err != nil {
		return expressions.Result{}, err
	}
	if params == nil {
		params = map[string]any{}
	}
	x := &execution{ctx: ctx, engine: e, p: p, vars: map[string]any{"data": map[string]any{}, "params": params, "computed": map[string]any{}, "rates": map[string]any{}}}
	x.load(data, nil, func(path string, e error) {
		if err == nil {
			err = fmt.Errorf("%s: %w", path, e)
		}
	})
	if err != nil {
		return expressions.Result{}, err
	}
	result, err := e.Expr.Eval(ctx, expr, x.vars)
	result.Cost += x.cost
	return result, err
}
