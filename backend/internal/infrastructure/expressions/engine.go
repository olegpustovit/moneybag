package expressions

import (
	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/ext"
	"context"
	"fmt"
	"google.golang.org/protobuf/types/known/structpb"
	"math"
	"reflect"
	"strconv"
	"sync"
	"time"
	_ "time/tzdata"
)

type Engine struct {
	env      *cel.Env
	mu       sync.Mutex
	programs map[string]cel.Program
}
type Result struct {
	Value any    `json:"value"`
	Type  string `json:"type"`
	Cost  uint64 `json:"cost"`
}

func number(v ref.Val) (float64, bool) {
	switch n := v.(type) {
	case types.Double:
		return float64(n), true
	case types.Int:
		return float64(n), true
	case types.Uint:
		return float64(n), true
	}
	return 0, false
}
func New() (*Engine, error) {
	opts := []cel.EnvOption{cel.Variable("row", cel.DynType), cel.Variable("data", cel.MapType(cel.StringType, cel.DynType)), cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)), cel.Variable("computed", cel.MapType(cel.StringType, cel.DynType)), cel.Variable("rates", cel.MapType(cel.StringType, cel.DynType)), cel.ParserRecursionLimit(40), cel.ParserExpressionSizeLimit(2000), cel.CrossTypeNumericComparisons(true)}
	unary := func(name string, fn func(float64) float64) {
		opts = append(opts, cel.Function(name, cel.Overload("mb_"+name, []*cel.Type{cel.DynType}, cel.DoubleType, cel.UnaryBinding(func(v ref.Val) ref.Val {
			n, ok := number(v)
			if !ok {
				return types.NewErr("%s needs a number", name)
			}
			r := fn(n)
			if math.IsInf(r, 0) || math.IsNaN(r) {
				return types.NewErr("non-finite result")
			}
			return types.Double(r)
		}))))
	}
	unary("abs", math.Abs)
	opts = append(opts, cel.Function("round", cel.Overload("mb_round", []*cel.Type{cel.DynType, cel.DynType}, cel.DoubleType, cel.BinaryBinding(func(x, y ref.Val) ref.Val {
		a, ok := number(x)
		b, bok := number(y)
		if !ok || !bok || b < 0 || b > 12 {
			return types.NewErr("round(number, decimals 0..12) expected")
		}
		p := math.Pow10(int(b))
		return types.Double(math.Round(a*p) / p)
	}))), cel.Function("pct", cel.Overload("mb_pct", []*cel.Type{cel.DynType, cel.DynType}, cel.DoubleType, cel.BinaryBinding(func(x, y ref.Val) ref.Val {
		a, ok := number(x)
		b, bok := number(y)
		if !ok || !bok {
			return types.NewErr("pct needs two numbers")
		}
		if b == 0 {
			return types.Double(0)
		}
		return types.Double(a / b * 100)
	}))))
	for _, name := range []string{"sum", "min", "max"} {
		name := name
		opts = append(opts, cel.Function(name, cel.Overload("mb_"+name, []*cel.Type{cel.ListType(cel.DynType)}, cel.DoubleType, cel.UnaryBinding(func(v ref.Val) ref.Val {
			l, ok := v.(traits.Lister)
			if !ok {
				return types.NewErr("%s needs a list", name)
			}
			n := int(l.Size().(types.Int))
			if n > 5000 {
				return types.NewErr("list too large")
			}
			total := 0.0
			for i := 0; i < n; i++ {
				x, ok := number(l.Get(types.Int(i)))
				if !ok {
					return types.NewErr("%s needs numbers", name)
				}
				if i == 0 && name != "sum" {
					total = x
				} else {
					switch name {
					case "sum":
						total += x
					case "min":
						total = math.Min(total, x)
					case "max":
						total = math.Max(total, x)
					}
				}
			}
			return types.Double(total)
		}))))
	}
	for name, symbol := range map[string]string{"eur": "€", "usd": "$"} {
		name, symbol := name, symbol
		opts = append(opts, cel.Function(name, cel.Overload("mb_"+name, []*cel.Type{cel.DynType}, cel.StringType, cel.UnaryBinding(func(v ref.Val) ref.Val {
			n, ok := number(v)
			if !ok {
				return types.NewErr("%s needs a number", name)
			}
			return types.String(symbol + strconv.FormatFloat(n, 'f', 0, 64))
		}))))
	}
	opts = append(opts, cel.Function("month", cel.Overload("mb_month", []*cel.Type{cel.StringType}, cel.StringType, cel.UnaryBinding(func(v ref.Val) ref.Val {
		s := string(v.(types.String))
		if len(s) < 10 {
			return types.NewErr("month needs ISO date")
		}
		if _, e := time.Parse("2006-01-02", s[:10]); e != nil {
			return types.NewErr("invalid date")
		}
		return types.String(s[:7])
	}))))
	zagreb, e := time.LoadLocation("Europe/Zagreb")
	if e != nil {
		return nil, e
	}
	date := func(v ref.Val, name string) (time.Time, ref.Val) {
		s, _ := v.(types.String)
		t, e := time.Parse("2006-01-02", string(s))
		if e != nil {
			return t, types.NewErr("%s needs a YYYY-MM-DD date", name)
		}
		return t, nil
	}
	opts = append(opts, ext.Strings(), ext.Lists(), ext.Sets(),
		cel.Function("today", cel.Overload("mb_today", nil, cel.StringType, cel.FunctionBinding(func(...ref.Val) ref.Val {
			return types.String(time.Now().In(zagreb).Format("2006-01-02"))
		}))),
		cel.Function("now", cel.Overload("mb_now", nil, cel.TimestampType, cel.FunctionBinding(func(...ref.Val) ref.Val {
			return types.Timestamp{Time: time.Now().In(zagreb)}
		}))),
		cel.Function("month_of", cel.Overload("mb_month_of", []*cel.Type{cel.StringType}, cel.StringType, cel.UnaryBinding(func(v ref.Val) ref.Val {
			t, bad := date(v, "month_of")
			if bad != nil {
				return bad
			}
			return types.String(t.Format("2006-01"))
		}))),
		cel.Function("day_of_month", cel.Overload("mb_day_of_month", []*cel.Type{cel.StringType}, cel.IntType, cel.UnaryBinding(func(v ref.Val) ref.Val {
			t, bad := date(v, "day_of_month")
			if bad != nil {
				return bad
			}
			return types.Int(t.Day())
		}))),
		cel.Function("days_in_month", cel.Overload("mb_days_in_month", []*cel.Type{cel.StringType}, cel.IntType, cel.UnaryBinding(func(v ref.Val) ref.Val {
			s, _ := v.(types.String)
			t, e := time.Parse("2006-01", string(s))
			if e != nil {
				return types.NewErr("days_in_month needs YYYY-MM")
			}
			return types.Int(t.AddDate(0, 1, -1).Day())
		}))))
	env, e := cel.NewEnv(opts...)
	if e != nil {
		return nil, e
	}
	return &Engine{env: env, programs: map[string]cel.Program{}}, nil
}
func (e *Engine) Compile(src string) (cel.Program, error) {
	if len(src) > 2000 {
		return nil, fmt.Errorf("expression exceeds 2000 characters")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.programs[src]; ok {
		return p, nil
	}
	ast, issues := e.env.Compile(src)
	if issues.Err() != nil {
		return nil, issues.Err()
	}
	p, err := e.env.Program(ast, cel.CostLimit(10000), cel.InterruptCheckFrequency(64), cel.EvalOptions(cel.OptTrackCost))
	if err != nil {
		return nil, err
	}
	if len(e.programs) >= 1024 {
		clear(e.programs)
	}
	e.programs[src] = p
	return p, nil
}
func (e *Engine) Eval(ctx context.Context, src string, vars map[string]any) (Result, error) {
	p, err := e.Compile(src)
	if err != nil {
		return Result{}, err
	}
	env := map[string]any{"row": map[string]any{}, "data": map[string]any{}, "params": map[string]any{}, "computed": map[string]any{}, "rates": map[string]any{}}
	for k, v := range vars {
		env[k] = v
	}
	out, details, err := p.ContextEval(ctx, env)
	if err != nil {
		return Result{}, err
	}
	if types.IsError(out) || types.IsUnknown(out) {
		return Result{}, fmt.Errorf("expression failed")
	}
	native, err := out.ConvertToNative(reflect.TypeOf(&structpb.Value{}))
	if err != nil {
		return Result{}, fmt.Errorf("convert CEL result: %w", err)
	}
	cost := uint64(0)
	if details != nil && details.ActualCost() != nil {
		cost = *details.ActualCost()
	}
	return Result{native.(*structpb.Value).AsInterface(), out.Type().TypeName(), cost}, nil
}
