package expressions

import (
	"fmt"
	"sort"

	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

// DataReferences finds statically named data keys; dynamic access depends on all keys.
func (e *Engine) DataReferences(src string) ([]string, bool, error) {
	ast, issues := e.env.Parse(src)
	if issues.Err() != nil {
		return nil, false, issues.Err()
	}
	refs := map[string]bool{}
	dynamic := false
	var walk func(*exprpb.Expr)
	walk = func(node *exprpb.Expr) {
		if node == nil {
			return
		}
		if id := node.GetIdentExpr(); id != nil && id.Name == "data" {
			dynamic = true
			return
		}
		if sel := node.GetSelectExpr(); sel != nil {
			if id := sel.Operand.GetIdentExpr(); id != nil && id.Name == "data" {
				refs[sel.Field] = true
				return
			}
			walk(sel.Operand)
			return
		}
		if call := node.GetCallExpr(); call != nil {
			if call.Function == "_[_]" && len(call.Args) == 2 {
				if id := call.Args[0].GetIdentExpr(); id != nil && id.Name == "data" {
					if c := call.Args[1].GetConstExpr(); c != nil {
						if v, ok := c.ConstantKind.(*exprpb.Constant_StringValue); ok {
							refs[v.StringValue] = true
							return
						}
					}
					dynamic = true
					walk(call.Args[1])
					return
				}
			}
			walk(call.Target)
			for _, arg := range call.Args {
				walk(arg)
			}
			return
		}
		if list := node.GetListExpr(); list != nil {
			for _, item := range list.Elements {
				walk(item)
			}
			return
		}
		if obj := node.GetStructExpr(); obj != nil {
			for _, item := range obj.Entries {
				walk(item.GetMapKey())
				walk(item.Value)
			}
			return
		}
		if c := node.GetComprehensionExpr(); c != nil {
			if c.IterVar == "data" || c.IterVar2 == "data" || c.AccuVar == "data" {
				dynamic = true
			}
			walk(c.IterRange)
			walk(c.AccuInit)
			walk(c.LoopCondition)
			walk(c.LoopStep)
			walk(c.Result)
		}
	}
	walk(ast.Expr())
	out := make([]string, 0, len(refs))
	for k := range refs {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > 200 {
		return nil, false, fmt.Errorf("expression references too many data keys")
	}
	return out, dynamic, nil
}
