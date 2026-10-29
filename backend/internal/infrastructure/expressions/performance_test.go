package expressions

import (
	"context"
	"maps"
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

func BenchmarkDashboardRowActivation(b *testing.B) {
	e, err := New()
	if err != nil {
		b.Fatal(err)
	}
	const expr = `row.amount > 0 && row.account.startsWith("expenses:")`
	program, err := e.Compile(expr)
	if err != nil {
		b.Fatal(err)
	}
	vars := map[string]any{"data": map[string]any{}, "params": map[string]any{}, "computed": map[string]any{}, "rates": map[string]any{}}
	row := map[string]any{"amount": 14.0, "account": "expenses:food"}
	for _, tc := range []struct {
		name      string
		optimized bool
	}{{"copied", false}, {"row", true}} {
		b.Run(tc.name, func(b *testing.B) {
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					var err error
					if tc.optimized {
						_, err = EvalProgramRow(context.Background(), expr, program, vars, row)
					} else {
						copy := maps.Clone(vars)
						copy["row"] = row
						_, err = EvalProgram(context.Background(), expr, program, copy)
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
