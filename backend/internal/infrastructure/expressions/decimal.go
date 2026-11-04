package expressions

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/functions"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/interpreter"
	"google.golang.org/protobuf/types/known/structpb"
)

// Rational values are adapted without depending on the ledger component.
type decimalAdapter struct{}

func (a decimalAdapter) NativeToValue(value any) ref.Val {
	switch v := value.(type) {
	case ref.Val:
		return v
	case interface{ Rat() *big.Rat }:
		return checkedDecimal(v.Rat())
	case map[string]any:
		return types.NewStringInterfaceMap(a, v)
	case []any:
		return types.NewDynamicList(a, v)
	case nil, bool, string, int, int32, int64, uint, uint32, uint64, float32, float64, []byte:
		return types.DefaultTypeAdapter.NativeToValue(value)
	}
	if value != nil {
		switch reflect.TypeOf(value).Kind() {
		case reflect.Map:
			return types.NewDynamicMap(a, value)
		case reflect.Slice, reflect.Array:
			return types.NewDynamicList(a, value)
		}
	}
	return types.DefaultTypeAdapter.NativeToValue(value)
}

type exactDecimal struct{ r *big.Rat }

func (d exactDecimal) Rat() *big.Rat  { return new(big.Rat).Set(d.r) }
func (d exactDecimal) Float() float64 { value, _ := d.r.Float64(); return value }
func (d exactDecimal) MarshalJSON() ([]byte, error) {
	value, err := decimalString(d.r)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

var decimalType = types.NewObjectType("moneybag.decimal", traits.AdderType|traits.ComparerType|traits.DividerType|traits.MultiplierType|traits.NegatorType|traits.SubtractorType)

func checkedDecimal(r *big.Rat) ref.Val {
	if r.Num().BitLen() > 4096 || r.Denom().BitLen() > 4096 {
		return types.NewErr("decimal expression exceeds the 4096-bit arithmetic budget")
	}
	return exactDecimal{new(big.Rat).Set(r)}
}

func rational(value ref.Val) (*big.Rat, bool) {
	switch v := value.(type) {
	case exactDecimal:
		return v.r, true
	case types.Int:
		return new(big.Rat).SetInt64(int64(v)), true
	case types.Uint:
		return new(big.Rat).SetInt(new(big.Int).SetUint64(uint64(v))), true
	case types.Double:
		n := float64(v)
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, false
		}
		// Decimal operands interpret numeric literals by their decimal representation.
		r, ok := new(big.Rat).SetString(strconv.FormatFloat(n, 'g', -1, 64))
		return r, ok
	}
	return nil, false
}

func decimalBinary(op string, lhs, rhs ref.Val) ref.Val {
	a, aok := rational(lhs)
	b, bok := rational(rhs)
	if !aok || !bok {
		return types.NoSuchOverloadErr()
	}
	r := new(big.Rat)
	switch op {
	case "+":
		r.Add(a, b)
	case "-":
		r.Sub(a, b)
	case "*":
		r.Mul(a, b)
	case "/":
		if b.Sign() == 0 {
			return types.NewErr("decimal division by zero")
		}
		r.Quo(a, b)
	case "<":
		return types.Bool(a.Cmp(b) < 0)
	case "<=":
		return types.Bool(a.Cmp(b) <= 0)
	case ">":
		return types.Bool(a.Cmp(b) > 0)
	case ">=":
		return types.Bool(a.Cmp(b) >= 0)
	default:
		return types.NewErr("unknown decimal operator")
	}
	return checkedDecimal(r)
}

func decimalOptions() []cel.EnvOption {
	opts := []cel.EnvOption{cel.CustomTypeAdapter(decimalAdapter{})}
	decimal := cel.ObjectType("moneybag.decimal")
	for name, result := range map[string]*cel.Type{"double": cel.DoubleType, "string": cel.StringType} {
		target := types.DoubleType
		if name == "string" {
			target = types.StringType
		}
		opts = append(opts, cel.Function(name, cel.Overload("mb_decimal_to_"+name, []*cel.Type{decimal}, result, cel.UnaryBinding(func(value ref.Val) ref.Val { return value.ConvertToType(target) }))))
	}
	for op, name := range map[string]string{"+": "add", "-": "subtract", "*": "multiply", "/": "divide", "<": "less", "<=": "less_equal", ">": "greater", ">=": "greater_equal"} {
		result := decimal
		if strings.ContainsAny(op, "<>") {
			result = cel.BoolType
		}
		for _, pair := range [][2]*cel.Type{{decimal, decimal}, {decimal, cel.IntType}, {cel.IntType, decimal}, {decimal, cel.UintType}, {cel.UintType, decimal}, {decimal, cel.DoubleType}, {cel.DoubleType, decimal}} {
			id := "mb_decimal_" + name + "_" + pair[0].TypeName() + "_" + pair[1].TypeName()
			opts = append(opts, cel.Function("_"+op+"_", cel.Overload(id, []*cel.Type{pair[0], pair[1]}, result)))
		}
	}
	return opts
}

func (d exactDecimal) Add(v ref.Val) ref.Val      { return decimalBinary("+", d, v) }
func (d exactDecimal) Subtract(v ref.Val) ref.Val { return decimalBinary("-", d, v) }
func (d exactDecimal) Multiply(v ref.Val) ref.Val { return decimalBinary("*", d, v) }
func (d exactDecimal) Divide(v ref.Val) ref.Val   { return decimalBinary("/", d, v) }
func (d exactDecimal) Negate() ref.Val            { return checkedDecimal(new(big.Rat).Neg(d.r)) }
func (d exactDecimal) Compare(v ref.Val) ref.Val {
	r, ok := rational(v)
	if !ok {
		return types.NoSuchOverloadErr()
	}
	return types.Int(d.r.Cmp(r))
}
func (d exactDecimal) Equal(v ref.Val) ref.Val {
	r, ok := rational(v)
	return types.Bool(ok && d.r.Cmp(r) == 0)
}
func (d exactDecimal) Type() ref.Type { return decimalType }
func (d exactDecimal) Value() any     { return new(big.Rat).Set(d.r) }
func (d exactDecimal) ConvertToType(t ref.Type) ref.Val {
	switch t {
	case decimalType:
		return d
	case types.TypeType:
		return decimalType
	case types.DoubleType:
		v, _ := d.r.Float64()
		return types.Double(v)
	case types.StringType:
		text, err := decimalString(d.r)
		if err != nil {
			return types.NewErr("%s", err)
		}
		return types.String(text)
	}
	return types.NewErr("unsupported decimal conversion to %s", t.TypeName())
}
func (d exactDecimal) ConvertToNative(t reflect.Type) (any, error) {
	text, err := decimalString(d.r)
	if err != nil {
		return nil, err
	}
	if t == reflect.TypeOf(&structpb.Value{}) {
		return structpb.NewStringValue(text), nil
	}
	if t.Kind() == reflect.String || t.Kind() == reflect.Interface {
		return text, nil
	}
	return nil, fmt.Errorf("unsupported decimal conversion to %s", t)
}

func decimalString(r *big.Rat) (string, error) {
	denominator := new(big.Int).Set(r.Denom())
	two, five, rem := big.NewInt(2), big.NewInt(5), new(big.Int)
	a, b := 0, 0
	for rem.Mod(denominator, two).Sign() == 0 {
		denominator.Quo(denominator, two)
		a++
	}
	for rem.Mod(denominator, five).Sign() == 0 {
		denominator.Quo(denominator, five)
		b++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", fmt.Errorf("decimal result is not finite; round it explicitly")
	}
	text := r.FloatString(max(a, b))
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	return text, nil
}

func roundDecimal(d exactDecimal, places int) ref.Val {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	numerator := new(big.Int).Mul(d.r.Num(), scale)
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(numerator, d.r.Denom(), remainder)
	if new(big.Int).Lsh(new(big.Int).Abs(remainder), 1).Cmp(d.r.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(int64(d.r.Sign())))
	}
	return checkedDecimal(new(big.Rat).SetFrac(whole, scale))
}

func decimalAggregate(name string, list traits.Lister) ref.Val {
	total := new(big.Rat)
	for i := types.Int(0); i < list.Size().(types.Int); i++ {
		value, ok := rational(list.Get(i))
		if !ok {
			return types.NewErr("%s needs numbers", name)
		}
		switch {
		case name == "sum":
			total.Add(total, value)
		case i == 0 || name == "min" && value.Cmp(total) < 0 || name == "max" && value.Cmp(total) > 0:
			total.Set(value)
		}
		if total.Num().BitLen() > 4096 || total.Denom().BitLen() > 4096 {
			return types.NewErr("decimal aggregate exceeds arithmetic budget")
		}
	}
	return checkedDecimal(total)
}

// CEL's standard operators have singleton bindings; runtime overrides retain their
// trait dispatch and only change operations involving an exact decimal operand.
func decimalBindings() []*functions.Overload {
	bindings := []*functions.Overload{}
	for _, op := range []string{"+", "-", "*", "/", "<", "<=", ">", ">="} {
		bindings = append(bindings, &functions.Overload{Operator: "_" + op + "_", Binary: func(a, b ref.Val) ref.Val {
			_, left := a.(exactDecimal)
			_, right := b.(exactDecimal)
			if left || right {
				return decimalBinary(op, a, b)
			}
			switch op {
			case "+":
				if value, ok := a.(traits.Adder); ok {
					return value.Add(b)
				}
			case "-":
				if value, ok := a.(traits.Subtractor); ok {
					return value.Subtract(b)
				}
			case "*":
				if value, ok := a.(traits.Multiplier); ok {
					return value.Multiply(b)
				}
			case "/":
				if value, ok := a.(traits.Divider); ok {
					return value.Divide(b)
				}
			default:
				if value, ok := a.(types.Double); ok && math.IsNaN(float64(value)) {
					return types.False
				}
				if value, ok := b.(types.Double); ok && math.IsNaN(float64(value)) {
					return types.False
				}
				if value, ok := a.(traits.Comparer); ok {
					result := value.Compare(b)
					cmp, ok := result.(types.Int)
					if !ok {
						return result
					}
					switch op {
					case "<":
						return types.Bool(cmp < 0)
					case "<=":
						return types.Bool(cmp <= 0)
					case ">":
						return types.Bool(cmp > 0)
					case ">=":
						return types.Bool(cmp >= 0)
					}
				}
			}
			return types.NoSuchOverloadErr()
		}})
	}
	return bindings
}

type decimalEquality struct {
	interpreter.InterpretableCall
	lhs, rhs interpreter.InterpretableV2
}

func (d decimalEquality) Eval(ctx interpreter.Activation) ref.Val {
	return d.Exec(interpreter.AsFrame(ctx))
}
func (d decimalEquality) Exec(frame *interpreter.ExecutionFrame) ref.Val {
	a := d.lhs.Exec(frame)
	if types.IsError(a) {
		return a
	}
	b := d.rhs.Exec(frame)
	if types.IsError(b) {
		return b
	}
	unknown, _ := types.MaybeMergeUnknowns(a, nil)
	unknown, _ = types.MaybeMergeUnknowns(b, unknown)
	if unknown != nil {
		return unknown
	}
	result := types.Equal(a, b)
	if _, ok := b.(exactDecimal); ok {
		result = b.Equal(a)
	}
	if _, ok := b.(interface{ ReferenceID() string }); ok {
		result = b.Equal(a)
	}
	if d.Function() == "_!=_" {
		return types.Bool(result != types.True)
	}
	return result
}

func decimalEqualityDecorator(value interpreter.InterpretableV2) (interpreter.InterpretableV2, error) {
	if call, ok := value.(interpreter.InterpretableCall); ok && (call.Function() == "_==_" || call.Function() == "_!=_") {
		args := call.Args()
		return decimalEquality{call, args[0], args[1]}, nil
	}
	return value, nil
}
