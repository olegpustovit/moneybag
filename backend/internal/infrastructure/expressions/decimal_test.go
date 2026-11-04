package expressions

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"sync"
	"testing"
)

type decimalFixture string

func (d decimalFixture) Rat() *big.Rat { r, _ := new(big.Rat).SetString(string(d)); return r }

func TestExactDecimalExpressions(t *testing.T) {
	engine, err := New()
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"row": map[string]any{"a": decimalFixture("0.1"), "b": decimalFixture("0.2"), "sum": decimalFixture("0.3"), "large": decimalFixture("10000000000000001.01"), "qty": decimalFixture("2.5"), "price": decimalFixture("3.12"), "amount": decimalFixture("7.80")}}
	for _, tc := range []struct {
		expression string
		want       any
	}{
		{"row.a+row.b == row.sum", true},
		{"0.3 == row.sum", true},
		{"row.a == 0.1", true},
		{"0.3 != row.sum", false},
		{"row.qty*row.price == row.amount", true},
		{"row.large + row.a - row.a", "10000000000000001.01"},
		{"row.a+row.b", "0.3"},
		{"2*row.qty", "5"}, {"row.qty*2", "5"},
		{"0.1+row.b", "0.3"}, {"row.a+0.2", "0.3"},
		{"row.a > 0", true}, {"0 < row.a", true},
		{"row.a < 0.2", true}, {"0.2 >= row.a", true},
		{"-row.qty", "-2.5"},
		{"row.amount/row.qty", "3.12"},
		{"sum([row.a,row.b])", "0.3"},
		{"sum([0.1,0.2,row.a])", "0.4"},
		{"min([row.a,row.b])", "0.1"},
		{"max([row.a,row.b])", "0.2"},
		{"abs(-row.large)", "10000000000000001.01"},
		{"round(row.large/2,2)", "5000000000000000.51"},
		{"round(-row.large/2,2)", "-5000000000000000.51"},
		{"round(row.a/3,2)", "0.03"},
		{"double(round(row.a/3,2))", float64(0.03)},
		{"(row.a/3)*3==row.a", true},
		{"double(row.qty)", float64(2.5)},
		{"string(row.amount)", "7.8"},
		{"[row.a,row.b]", nil},
		{"1+2", float64(3)}, {"0.1+0.2", 0.30000000000000004},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			result, err := engine.Eval(context.Background(), tc.expression, vars)
			got := result.Value
			if _, ok := got.(exactDecimal); ok {
				raw, encodeErr := json.Marshal(got)
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				if decodeErr := json.Unmarshal(raw, &got); decodeErr != nil {
					t.Fatal(decodeErr)
				}
			}
			if err != nil || (tc.want != nil && got != tc.want) {
				t.Fatalf("got %#v, %v; want %#v", result.Value, err, tc.want)
			}
		})
	}
	for _, expression := range []string{"row.a/0", "row.a/3", "row.a+'bad'"} {
		if _, err := engine.Eval(context.Background(), expression, vars); err == nil {
			t.Fatalf("accepted %s", expression)
		}
	}
	tooLarge := decimalFixture("1" + strings.Repeat("0", 1300))
	if _, err = engine.Eval(context.Background(), "row.value+row.value", map[string]any{"row": map[string]any{"value": tooLarge}}); err == nil {
		t.Fatal("arithmetic budget not enforced")
	}
}

func TestDecimalEvaluationIsolationAndBudgets(t *testing.T) {
	engine, err := New()
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"row": map[string]any{"amount": decimalFixture("10000000000000001.01")}}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 25 {
				result, err := engine.Eval(context.Background(), "row.amount+0.01-row.amount==0.01", vars)
				if err != nil || result.Value != true {
					t.Errorf("concurrent decimal: %v %v", result.Value, err)
					return
				}
			}
		})
	}
	workers.Wait()
	values := make([]any, 300)
	for i := range values {
		values[i] = decimalFixture("1")
	}
	vars = map[string]any{"row": map[string]any{"values": values}}
	if _, err := engine.Eval(context.Background(), "row.values.all(x,row.values.all(y,x==y))", vars); err == nil {
		t.Fatal("equality decorator bypasses the evaluation budget")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Eval(ctx, "row.values.all(x,row.values.all(y,x==y))", vars); err == nil {
		t.Fatal("decimal evaluation ignores cancellation")
	}
}
