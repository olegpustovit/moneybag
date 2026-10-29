package expressions

import (
	"context"
	"regexp"
	"sync/atomic"
)

type volatilityKey struct{}

var nowCall = regexp.MustCompile(`\bnow\b`)

// WithVolatility tracks whether a read evaluated a time-varying expression.
func WithVolatility(ctx context.Context) context.Context {
	if ctx.Value(volatilityKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, volatilityKey{}, &atomic.Bool{})
}
func Volatile(ctx context.Context) bool {
	flag, _ := ctx.Value(volatilityKey{}).(*atomic.Bool)
	return flag != nil && flag.Load()
}
func markVolatile(ctx context.Context, src string) {
	if flag, ok := ctx.Value(volatilityKey{}).(*atomic.Bool); ok && nowCall.MatchString(src) {
		flag.Store(true)
	}
}
