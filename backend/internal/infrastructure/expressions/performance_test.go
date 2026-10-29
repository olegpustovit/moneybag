package expressions

import (
	"cel.dev/cel-go/common/types"
	"context"
	"google.golang.org/protobuf/types/known/structpb"
	"maps"
	"reflect"
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

func BenchmarkNativeScalar(b *testing.B) {
	value := types.Double(12.345)
	for _, tc := range []struct {
		name string
		fast bool
	}{{"protobuf", false}, {"direct", true}} {
		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if tc.fast {
					_, _ = nativeValue(value)
				} else {
					v, _ := value.ConvertToNative(reflect.TypeOf(&structpb.Value{}))
					_ = v.(*structpb.Value).AsInterface()
				}
			}
		})
	}
}
