package expressions

import (
	"context"
	"testing"
)

func BenchmarkParallelRowExpression(b *testing.B) {
	e, err := New()
	if err != nil {
		b.Fatal(err)
	}
	const expr = `row.amount > 0 && row.account.startsWith("expenses:")`
	program, err := e.Compile(expr)
	if err != nil {
		b.Fatal(err)
	}
	vars := map[string]any{"row": map[string]any{"amount": 14.0, "account": "expenses:food"}}
	for _, tc := range []struct {
		name     string
		prepared bool
	}{{"lookup", false}, {"prepared", true}} {
		b.Run(tc.name, func(b *testing.B) {
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					var err error
					if tc.prepared {
						_, err = EvalProgram(context.Background(), expr, program, vars)
					} else {
						_, err = e.Eval(context.Background(), expr, vars)
					}
					if err != nil {
						b.Error(err)
						return
					}
				}
			})
		})
	}
}
