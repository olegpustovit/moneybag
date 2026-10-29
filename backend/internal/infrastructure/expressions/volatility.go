package expressions

import (
	"context"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

type volatilityKey struct{}
type volatility struct {
	volatile atomic.Bool
	expires  atomic.Int64
}

var nowCall = regexp.MustCompile(`\bnow\b`)

// WithVolatility tracks time-varying expressions and the next scheduled data change.
func WithVolatility(ctx context.Context) context.Context {
	if ctx.Value(volatilityKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, volatilityKey{}, &volatility{})
}
func Tracking(ctx context.Context) bool { return ctx.Value(volatilityKey{}) != nil }
func Volatile(ctx context.Context) bool {
	flag, _ := ctx.Value(volatilityKey{}).(*volatility)
	return flag != nil && flag.volatile.Load()
}
func MarkVolatile(ctx context.Context) {
	if flag, ok := ctx.Value(volatilityKey{}).(*volatility); ok {
		flag.volatile.Store(true)
	}
}
func ExpireAt(ctx context.Context, at time.Time) {
	flag, ok := ctx.Value(volatilityKey{}).(*volatility)
	if !ok || at.IsZero() {
		return
	}
	next := at.Unix()
	for old := flag.expires.Load(); old == 0 || next < old; old = flag.expires.Load() {
		if flag.expires.CompareAndSwap(old, next) {
			return
		}
	}
}
func Expiry(ctx context.Context) time.Time {
	if flag, ok := ctx.Value(volatilityKey{}).(*volatility); ok {
		if n := flag.expires.Load(); n != 0 {
			return time.Unix(n, 0)
		}
	}
	return time.Time{}
}
func markVolatile(ctx context.Context, src string) {
	if strings.Contains(src, "now") && nowCall.MatchString(src) {
		MarkVolatile(ctx)
	}
}
