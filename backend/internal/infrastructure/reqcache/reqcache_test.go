package reqcache

import (
	"context"
	"errors"
	"testing"
)

func TestDoRunsOncePerKeyOnlyWithinScope(t *testing.T) {
	n := 0
	fn := func() (int, error) { n++; return n, nil }
	ctx := With(context.Background())
	if a, _ := Do(ctx, "k", fn); a != 1 {
		t.Fatal(a)
	}
	if b, _ := Do(ctx, "k", fn); b != 1 || n != 1 {
		t.Fatal("not memoized", b, n)
	}
	if c, _ := Do(context.Background(), "k", fn); c != 2 {
		t.Fatal("memoized outside a scope", c)
	}
	if _, e := Do(ctx, "err", func() (int, error) { return 0, errors.New("x") }); e == nil {
		t.Fatal("error lost")
	}
}
