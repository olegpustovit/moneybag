// Package reqcache memoizes expensive reads for the lifetime of one read request.
package reqcache

import (
	"context"
	"sync"
)

type key struct{}

type entry struct {
	once sync.Once
	v    any
	e    error
}

type store struct {
	mu sync.Mutex
	m  map[string]*entry
}

// With scopes a memo to ctx; use it only around reads, since values computed under it are never refreshed.
func With(ctx context.Context) context.Context {
	if _, ok := ctx.Value(key{}).(*store); ok {
		return ctx
	}
	return context.WithValue(ctx, key{}, &store{m: map[string]*entry{}})
}

// Do runs fn once per key within the request; without With it just runs fn. Callers must not mutate the result.
func Do[T any](ctx context.Context, k string, fn func() (T, error)) (T, error) {
	s, ok := ctx.Value(key{}).(*store)
	if !ok {
		return fn()
	}
	s.mu.Lock()
	en := s.m[k]
	if en == nil {
		en = &entry{}
		s.m[k] = en
	}
	s.mu.Unlock()
	en.once.Do(func() { en.v, en.e = fn() })
	v, _ := en.v.(T)
	return v, en.e
}
