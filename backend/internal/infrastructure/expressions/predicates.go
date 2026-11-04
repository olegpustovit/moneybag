package expressions

import exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"

type RowPredicate struct {
	Field  string
	Values []any
}

// RequiredPredicates extracts only literal equalities/membership under AND. OR,
// negation, nested fields and computed operands remain in CEL.
func (e *Engine) RequiredPredicates(src string) ([]RowPredicate, error) {
	ast, issues := e.env.Parse(src)
	if issues.Err() != nil {
		return nil, issues.Err()
	}
	field := func(node *exprpb.Expr) string {
		if selectExpr := node.GetSelectExpr(); selectExpr != nil && selectExpr.Operand.GetIdentExpr().GetName() == "row" {
			return selectExpr.Field
		}
		if call := node.GetCallExpr(); call != nil && call.Function == "_[_]" && len(call.Args) == 2 && call.Args[0].GetIdentExpr().GetName() == "row" {
			return call.Args[1].GetConstExpr().GetStringValue()
		}
		return ""
	}
	literal := func(node *exprpb.Expr) (any, bool) {
		constant := node.GetConstExpr()
		if constant == nil {
			return nil, false
		}
		switch value := constant.ConstantKind.(type) {
		case *exprpb.Constant_StringValue:
			return value.StringValue, true
		case *exprpb.Constant_BoolValue:
			return value.BoolValue, true
		case *exprpb.Constant_Int64Value:
			return value.Int64Value, true
		case *exprpb.Constant_DoubleValue:
			return value.DoubleValue, true
		case *exprpb.Constant_Uint64Value:
			if value.Uint64Value <= 1<<63-1 {
				return int64(value.Uint64Value), true
			}
		}
		return nil, false
	}
	out := []RowPredicate{}
	var walk func(*exprpb.Expr)
	walk = func(node *exprpb.Expr) {
		call := node.GetCallExpr()
		if call == nil || len(call.Args) != 2 {
			return
		}
		if call.Function == "_&&_" {
			walk(call.Args[0])
			walk(call.Args[1])
			return
		}
		if call.Function == "_==_" {
			for i := range 2 {
				if name := field(call.Args[i]); name != "" {
					if value, ok := literal(call.Args[1-i]); ok {
						out = append(out, RowPredicate{name, []any{value}})
						return
					}
				}
			}
		}
		if call.Function == "@in" || call.Function == "_in_" {
			name := field(call.Args[0])
			list := call.Args[1].GetListExpr()
			if name == "" || list == nil {
				return
			}
			values := []any{}
			for _, element := range list.Elements {
				value, ok := literal(element)
				if !ok {
					return
				}
				values = append(values, value)
			}
			out = append(out, RowPredicate{name, values})
		}
	}
	walk(ast.Expr())
	return out, nil
}
