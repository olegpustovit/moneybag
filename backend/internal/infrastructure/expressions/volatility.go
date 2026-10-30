package expressions

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cel.dev/cel-go/common"
	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/parser"
)

type volatilityKey struct{}
type volatility struct {
	volatile atomic.Bool
	expires  atomic.Int64
}

var nowCall = regexp.MustCompile(`\bnow\b`)
var nowParser, nowParserErr = parser.NewParser(parser.MaxRecursionDepth(40), parser.ExpressionSizeCodePointLimit(2000))
var nowCache = struct {
	sync.RWMutex
	values map[string]bool
}{values: map[string]bool{}}

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
	if MayUseNow(src) {
		MarkVolatile(ctx)
	}
}

// MayUseNow conservatively identifies expressions that prevent cross-request caching.
func MayUseNow(src string) bool {
	if !strings.Contains(src, "now") || !nowCall.MatchString(src) {
		return false
	}
	nowCache.RLock()
	cached, ok := nowCache.values[src]
	nowCache.RUnlock()
	if ok {
		return cached
	}
	volatile := true
	if nowParserErr == nil {
		tree, issues := nowParser.Parse(common.NewTextSource(src))
		if len(issues.GetErrors()) == 0 && tree != nil && !ast.ExceedsDepth(tree, 40) {
			volatile = false
			ast.PreOrderVisit(tree.Expr(), ast.NewExprVisitor(func(expr ast.Expr) {
				if expr.Kind() == ast.CallKind && expr.AsCall().FunctionName() == "now" {
					volatile = true
				}
			}))
		}
	}
	nowCache.Lock()
	// shortcut: classification cache clears at 1024 expressions; use byte-aware eviction if churn grows.
	if len(nowCache.values) >= 1024 {
		clear(nowCache.values)
	}
	nowCache.values[src] = volatile
	nowCache.Unlock()
	return volatile
}
