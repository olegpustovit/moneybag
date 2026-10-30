package dashboard

import (
	"cel.dev/cel-go/cel"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"maps"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

type paramsKey struct{}

// evalBudget bounds all queries of one dashboard.
const evalBudget = 10 * time.Second

type Reader interface {
	Rows(context.Context, principal.Principal, string) ([]map[string]any, error)
}
type Engine struct {
	Expr   *expressions.Engine
	Reader Reader
}
type execution struct {
	ctx        context.Context
	engine     *Engine
	p          principal.Principal
	vars       map[string]any
	cost       uint64
	count      int
	source     map[string][]map[string]any // rows already read from each source in this execution
	memo       *sourceMemo
	dataBudget *byteBudget // all query results of this execution share 8 MiB
	programs   map[string]cel.Program
	selectors  map[string][]string
}

var rowSelector = regexp.MustCompile(`^row\.[a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)?$`)

func (x *execution) eval(expr string, row map[string]any) (any, error) {
	if err := x.ctx.Err(); err != nil {
		return nil, err
	}
	x.count++
	if x.count > 5000000 {
		return nil, fmt.Errorf("page evaluation exceeds 5000000 calls")
	}
	if x.programs == nil {
		x.programs = map[string]cel.Program{}
		x.selectors = map[string][]string{}
	}
	program := x.programs[expr]
	if program == nil {
		var err error
		program, err = x.engine.Expr.Compile(expr)
		if err != nil {
			return nil, err
		}
		x.programs[expr] = program
		if rowSelector.MatchString(expr) {
			x.selectors[expr] = strings.Split(expr[4:], ".")
		}
	}
	if fields := x.selectors[expr]; row != nil && len(fields) != 0 {
		var value any = row
		for _, field := range fields {
			m, ok := value.(map[string]any)
			if !ok {
				value = nil
				break
			}
			value, ok = m[field]
			if !ok {
				value = nil
				break
			}
		}
		// Missing fields and CEL-specific values still go through the interpreter.
		fast := false
		switch v := value.(type) {
		case string, bool:
			fast = true
		case float64:
			fast = !math.IsInf(v, 0) && !math.IsNaN(v)
		}
		if fast {
			x.cost += uint64(len(fields) + 1)
			if x.cost > 100000000 {
				return nil, fmt.Errorf("page evaluation exceeds 100 million operations")
			}
			return value, nil
		}
	}
	r, e := expressions.EvalProgramRow(x.ctx, expr, program, x.vars, row)
	x.cost += r.Cost
	if x.cost > 100000000 {
		return nil, fmt.Errorf("page evaluation exceeds 100 million operations")
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
	if err := x.ctx.Err(); err != nil {
		return nil, err
	}
	var rows []map[string]any
	immutable := false
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
		cached, ok := x.source[q.From]
		if !ok {
			if x.memo == nil {
				x.memo = &sourceMemo{}
			}
			cached, e = x.memo.read(q.From, func() ([]map[string]any, error) {
				if sources, ok := x.engine.Reader.(*Sources); ok {
					return sources.sharedRows(x.ctx, x.p, q.From)
				}
				return x.engine.Reader.Rows(x.ctx, x.p, q.From)
			})
			if e != nil {
				return nil, e
			}
			if x.source == nil {
				x.source = map[string][]map[string]any{}
			}
			x.source[q.From] = cached
		}
		rows = cached
		_, immutable = x.engine.Reader.(*Sources)
	}
	if len(rows) > 250000 {
		return nil, fmt.Errorf("source exceeds 250000 rows")
	}
	out := []map[string]any{}
	class := ""
	if q.From == "postings" && q.Where != "" && len(rows) > 1 {
		class = x.engine.Expr.RequiredClass(q.Where)
	}
	for i, r := range rows {
		if i%1000 == 0 && x.ctx.Err() != nil {
			return nil, x.ctx.Err()
		}
		if q.Where != "" {
			if i > 0 && class != "" {
				if actual, ok := r["class"].(string); ok && actual != class {
					continue
				}
			}
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
		// Derivations modify rows; external readers may also retain their results.
		if !immutable || len(q.Derive) > 0 {
			r = maps.Clone(r)
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
		for i, r := range out {
			if i%1000 == 0 && x.ctx.Err() != nil {
				return nil, x.ctx.Err()
			}
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
				v, e := x.aggregate(agg, group)
				if e != nil {
					return nil, fmt.Errorf("agg %s: %w", k, e)
				}
				r[k] = v
			}
			out = append(out, r)
		}
	}
	if q.HideZero {
		kept := []map[string]any{}
		for _, r := range out {
			numbers, nonzero := 0, false
			for k, v := range r {
				if len(q.Agg) > 0 {
					if _, ok := q.Agg[k]; !ok {
						continue
					}
				}
				if n, e := numeric(v); e == nil {
					numbers++
					nonzero = nonzero || n != 0
				}
			}
			if numbers == 0 || nonzero {
				kept = append(kept, r)
			}
		}
		out = kept
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

var aggPattern = regexp.MustCompile(`^(sum|avg|mean|min|max|count|last|median|count_distinct|percentile|linear_slope)\((.*)\)$`)

// splitArgs splits on commas outside brackets and quotes.
func splitArgs(s string) []string {
	var out []string
	depth, start := 0, 0
	var quote rune
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case strings.ContainsRune("([{", r):
			depth++
		case strings.ContainsRune(")]}", r):
			depth--
		case r == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// parseAgg returns the operation (mean is avg) and its argument expressions.
func parseAgg(s string) (string, []string, error) {
	m := aggPattern.FindStringSubmatch(s)
	if m == nil {
		return "", nil, fmt.Errorf("aggregation must be sum/avg/mean/min/max/count/last/median/count_distinct(expr), percentile(expr, p) or linear_slope(x, y)")
	}
	op, args := m[1], splitArgs(m[2])
	if op == "mean" {
		op = "avg"
	}
	want := 1
	if op == "percentile" || op == "linear_slope" {
		want = 2
	}
	if op != "count" && len(args) != want {
		return "", nil, fmt.Errorf("%s takes %d argument(s)", op, want)
	}
	return op, args, nil
}

func (x *execution) aggregate(agg string, group []map[string]any) (any, error) {
	op, args, e := parseAgg(agg)
	if e != nil {
		return nil, e
	}
	if op == "count" {
		return float64(len(group)), nil
	}
	values := func(expr string) ([]any, error) {
		out := make([]any, len(group))
		for i, row := range group {
			v, e := x.eval(expr, row)
			if e != nil {
				return nil, e
			}
			out[i] = v
		}
		return out, nil
	}
	first, e := values(args[0])
	if e != nil {
		return nil, e
	}
	switch op {
	case "last":
		if len(first) == 0 {
			return nil, nil
		}
		return first[len(first)-1], nil
	case "count_distinct":
		seen := map[string]bool{}
		for _, v := range first {
			seen[fmt.Sprint(v)] = true
		}
		return float64(len(seen)), nil
	}
	nums := make([]float64, len(first))
	for i, v := range first {
		if nums[i], e = numeric(v); e != nil {
			return nil, e
		}
	}
	switch op {
	case "sum", "avg":
		total := 0.0
		for _, n := range nums {
			total += n
		}
		if op == "avg" && len(nums) > 0 {
			total /= float64(len(nums))
		}
		return total, nil
	case "min", "max":
		best := 0.0
		for i, n := range nums {
			if i == 0 || op == "min" && n < best || op == "max" && n > best {
				best = n
			}
		}
		return best, nil
	case "median", "percentile":
		p := 50.0
		if op == "percentile" {
			v, e := x.eval(args[1], nil)
			if e == nil {
				p, e = numeric(v)
			}
			if e != nil || p < 0 || p > 100 {
				return nil, fmt.Errorf("percentile p must be a number 0..100")
			}
		}
		if len(nums) == 0 {
			return nil, nil
		}
		sort.Float64s(nums)
		rank := p / 100 * float64(len(nums)-1)
		lo := int(math.Floor(rank))
		hi := min(lo+1, len(nums)-1)
		return nums[lo] + (nums[hi]-nums[lo])*(rank-float64(lo)), nil
	}
	// linear_slope: least-squares dy/dx.
	second, e := values(args[1])
	if e != nil {
		return nil, e
	}
	xs, ys := make([]float64, len(nums)), make([]float64, len(nums))
	var mx, my float64
	for i, n := range nums {
		y, e := numeric(second[i])
		if e != nil {
			return nil, e
		}
		xs[i], ys[i] = n, y
		mx, my = mx+n, my+y
	}
	count := float64(len(nums))
	if count < 2 {
		return nil, nil
	}
	mx, my = mx/count, my/count
	// Centered sums avoid the cancellation of n*sxx - sx*sx on large x (timestamps).
	var sxy, sxx float64
	for i := range xs {
		sxy, sxx = sxy+(xs[i]-mx)*(ys[i]-my), sxx+(xs[i]-mx)*(xs[i]-mx)
	}
	if sxx <= count*mx*mx*1e-24 {
		return nil, nil
	}
	return sxy / sxx, nil
}

// EvalError locates a failed expression inside a component so validation can name it.
type EvalError struct {
	Field, Expr, Hint string
	Err               error
}

func componentPath(id string) string {
	return strings.ReplaceAll(strings.ReplaceAll(id, "~", "~0"), "/", "~1")
}

func (e *EvalError) Error() string { return e.Err.Error() }
func (e *EvalError) Unwrap() error { return e.Err }

func (x *execution) evalAt(field, expr string, row map[string]any) (any, error) {
	v, e := x.eval(expr, row)
	if e != nil {
		return nil, &EvalError{Field: field, Expr: expr, Err: e}
	}
	return v, nil
}

func shareJournalAndBalances(ctx context.Context, data map[string]Query, params map[string]any) context.Context {
	from, _ := params["from"].(string)
	to, _ := params["to"].(string)
	if from != "" || to != "" {
		return ctx
	}
	dates, ok := ctx.Value(balanceDatesKey{}).([]string)
	if !ok {
		return ctx
	}
	for _, q := range data {
		if q.From == "journal" || q.From == "effective_entries" || q.From == "postings" {
			return ledger.WithCombinedJournal(ctx, dates)
		}
	}
	return ctx
}

func sharePostedSources(ctx context.Context, data map[string]Query, params map[string]any) context.Context {
	from, _ := params["from"].(string)
	to, _ := params["to"].(string)
	if from != "" || to != "" {
		return ctx
	}
	seen := map[string]bool{}
	for _, q := range data {
		if q.From == "journal" || q.From == "effective_entries" || q.From == "postings" {
			seen[q.From] = true
		}
	}
	if len(seen) < 2 {
		return ctx
	}
	return ledger.WithSharedPostedStream(ctx)
}

func shareBalanceHistory(ctx context.Context, data map[string]Query) context.Context {
	var hasAccounts, hasHistory bool
	for _, q := range data {
		hasAccounts = hasAccounts || q.From == "accounts"
		hasHistory = hasHistory || q.From == "balance_history"
	}
	if !hasAccounts || !hasHistory {
		return ctx
	}
	dates := balanceHistoryDates(time.Now())
	return context.WithValue(ledger.WithBalanceSeries(ctx, dates), balanceDatesKey{}, dates)
}

func shareBudgetMonths(ctx context.Context, data map[string]Query, params map[string]any) context.Context {
	var hasLines, hasMonths bool
	for _, q := range data {
		hasLines = hasLines || q.From == "budget_lines"
		hasMonths = hasMonths || q.From == "months"
	}
	if !hasLines || !hasMonths {
		return ctx
	}
	month, _ := params["month"].(string)
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	date, err := time.Parse("2006-01", month)
	if err != nil {
		return ctx
	}
	months := 6
	if n, ok := params["window_months"].(float64); ok && int(n) != 0 {
		months = int(n)
	}
	if months < 1 || months > 36 {
		return ctx
	}
	return budget.WithMonthReadWindow(ctx, date.AddDate(0, -max(6, months), 0).Format("2006-01-02"), date.AddDate(0, 1, -1).Format("2006-01-02"))
}

func (e *Engine) Execute(ctx context.Context, p principal.Principal, spec Spec) (Rendered, error) {
	ctx = expressions.WithVolatility(ctx)
	ctx = ledger.WithAccountCache(rates.WithQuoteCache(reqcache.With(ctx))) // sources, quotes and accounts are read once per request
	ctx, cancel := context.WithTimeout(ctx, evalBudget)
	defer cancel()
	ctx = context.WithValue(shareJournalAndBalances(sharePostedSources(shareBalanceHistory(shareBudgetMonths(ctx, spec.Data, spec.Params), spec.Data), spec.Data, spec.Params), spec.Data, spec.Params), paramsKey{}, spec.Params)
	result := Rendered{Values: map[string]any{}, Validation: Validation{OK: true, Errors: []Issue{}, Warnings: []Issue{}}}
	issue := func(path string, err error) {
		i := Issue{Path: path, Msg: err.Error()}
		var ee *EvalError
		if errors.As(err, &ee) {
			i.Field, i.Expr, i.Hint = ee.Field, ee.Expr, ee.Hint
		}
		if rest, ok := strings.CutPrefix(path, "/components/"); ok {
			var field string
			i.ComponentID, field, _ = strings.Cut(rest, "/")
			i.ComponentID = strings.ReplaceAll(strings.ReplaceAll(i.ComponentID, "~1", "/"), "~0", "~")
			if i.Field == "" {
				i.Field = field
			}
		}
		result.Validation.Errors = append(result.Validation.Errors, i)
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
	globalCompileError := false
	walkExpressions(spec, func(path, expr string) {
		count++
		if _, err := e.Expr.Compile(expr); err != nil {
			issue(path, &EvalError{Expr: expr, Err: err})
			if !strings.HasPrefix(path, "/components/") {
				globalCompileError = true
			}
		}
	})
	if count > 200 {
		issue("/", fmt.Errorf("dashboard exceeds 200 expressions"))
		globalCompileError = true
	}
	if globalCompileError {
		return result, nil
	}
	x := &execution{ctx: ctx, engine: e, p: p, vars: map[string]any{"data": map[string]any{}, "params": spec.Params, "computed": map[string]any{}, "rates": map[string]any{}}}
	if spec.Params == nil {
		x.vars["params"] = map[string]any{}
	}
	compiledIssues := len(result.Validation.Errors)
	x.load(spec.Data, spec.Computed, issue)
	if len(result.Validation.Errors) > compiledIssues {
		return result, nil
	}
	invalidComponents := map[string]bool{}
	for _, problem := range result.Validation.Errors {
		if problem.ComponentID != "" {
			invalidComponents[problem.ComponentID] = true
		}
	}
	components := map[string]Component{}
	html := map[string]string{}
	valuesBudget := &byteBudget{8 * 1024 * 1024}
	for i, c := range spec.Components {
		if c.ID == "" {
			issue(fmt.Sprintf("/components/%d/id", i), fmt.Errorf("id required"))
			continue
		}
		path := "/components/" + componentPath(c.ID)
		if _, ok := components[c.ID]; ok {
			issue(path+"/id", fmt.Errorf("duplicate component id"))
			continue
		}
		components[c.ID] = c
		if invalidComponents[c.ID] {
			continue
		}
		c, err := x.resolveText(c)
		if err != nil {
			issue(path, err)
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
		if typed, ok := value.(map[string]any); ok {
			typed["title"] = c.Title
		}
		result.Values[c.ID] = value
		if e := valuesBudget.spend(value); e != nil {
			issue(path, e)
			return result, nil
		}
	}
	var output strings.Builder
	used := map[string]bool{}
	pageIDs := map[string]bool{}
	for pi, pg := range spec.Pages {
		pg.Title = ""
		if pg.ID == "" || pageIDs[pg.ID] {
			issue(fmt.Sprintf("/pages/%d/id", pi), fmt.Errorf("unique page id required"))
		}
		pageIDs[pg.ID] = true
		output.WriteString(`<section class="dashboard-page">`)
		if len(spec.Pages) > 1 {
			title, err := x.template("title", spec.Pages[pi].Title)
			if err != nil {
				issue(fmt.Sprintf("/pages/%d/title", pi), err)
			}
			output.WriteString(`<h2>` + escape(title) + `</h2>`)
			pg.Title = title
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
				body, ok := html[c.ID]
				if !ok {
					c.Title = "Component unavailable"
					body = "<p>This component could not be displayed.</p>"
				}
				fragment := componentHTML(c, col.Span, body)
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
		result.Pages = append(result.Pages, pg)
	}
	for id := range components {
		if !used[id] {
			result.Validation.Warnings = append(result.Validation.Warnings, Issue{Path: "/components/" + componentPath(id), Msg: "component " + id + " is not placed", ComponentID: id})
		}
	}
	result.Validation.Cost = x.cost
	result.HTML = output.String()
	result.Computed = x.vars["computed"].(map[string]any)
	if ctx.Err() != nil {
		issue("/", fmt.Errorf("dashboard exceeded 10 second evaluation budget"))
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
			if op, args, err := parseAgg(v); err == nil && op != "count" {
				emit(base+"/agg/"+f, args[0])
				if op == "linear_slope" || op == "percentile" {
					emit(base+"/agg/"+f, args[1])
				}
			}
		}
	}
	for k, v := range s.Computed {
		emit("/computed/"+k, v)
	}
	for _, pg := range s.Pages {
		for _, h := range holes.FindAllStringSubmatch(pg.Title, -1) {
			expr, _ := parseHole(h[1])
			emit("/pages/"+pg.ID+"/title", expr)
		}
	}
	for i, c := range s.Components {
		base := "/components/" + componentPath(c.ID)
		if c.ID == "" {
			base = fmt.Sprintf("/components/%d", i)
		}
		for _, h := range holes.FindAllStringSubmatch(c.Title, -1) {
			expr, _ := parseHole(h[1])
			emit(base+"/title", expr)
		}
		for j, col := range c.Columns {
			for _, h := range holes.FindAllStringSubmatch(col.Label, -1) {
				expr, _ := parseHole(h[1])
				emit(fmt.Sprintf("%s/columns/%d/label", base, j), expr)
			}
		}
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
			expr, _ := parseHole(m[1])
			emit(base+"/md", expr)
		}
	}
}

func (x *execution) load(data map[string]Query, computedExprs map[string]string, issue func(string, error)) {
	if !x.loadQueries(data, issue) {
		pending := map[string]Query{}
		for k, q := range data {
			pending[k] = q
		}
		for len(pending) > 0 {
			progress := false
			errs := map[string]error{}
			for k, q := range pending {
				if err := x.ctx.Err(); err != nil {
					issue("/data/"+k, err)
					return
				}
				v, err := x.query(q)
				if err != nil {
					errs[k] = err
					continue
				}
				x.vars["data"].(map[string]any)[k] = v
				if x.dataBudget == nil {
					x.dataBudget = &byteBudget{8 * 1024 * 1024}
				}
				if e := x.dataBudget.spend(v); e != nil {
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
	}

	computed := map[string]string{}
	for k, v := range computedExprs {
		computed[k] = v
	}
	for len(computed) > 0 {
		progress := false
		errs := map[string]error{}
		for k, expr := range computed {
			if err := x.ctx.Err(); err != nil {
				issue("/computed/"+k, err)
				return
			}
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
	ctx = ledger.WithAccountCache(rates.WithQuoteCache(reqcache.With(ctx))) // sources, quotes and accounts are read once per request
	ctx, cancel := context.WithTimeout(ctx, evalBudget)
	defer cancel()
	ctx = context.WithValue(shareJournalAndBalances(sharePostedSources(shareBalanceHistory(shareBudgetMonths(ctx, data, params), data), data, params), data, params), paramsKey{}, params)
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
