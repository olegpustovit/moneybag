package dashboard

import (
	"context"
	"errors"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"testing"
	"time"
)

func TestDetachedViewRefreshStopsBeforeOwnerDataWhenDenied(t *testing.T) {
	checked := make(chan struct{})
	cache := &Snapshots{last: map[string]*lastView{"eval-view": {}}}
	s := &Service{snapshots: cache}
	s.WithAccessGuard(func(ctx context.Context, user string) (context.Context, context.CancelFunc, error) {
		if user != "eval-owner" {
			t.Error("wrong detached owner")
		}
		next, release := context.WithCancel(ctx)
		release()
		close(checked)
		return next, release, errors.New("owned denial")
	})
	// No repository/engine is configured: accessing finance data would panic.
	s.refreshView(principal.Principal{UserID: "eval-owner"}, "eval-dashboard", "published", nil, "eval-view")
	select {
	case <-checked:
	case <-time.After(time.Second):
		t.Fatal("detached refresh skipped access check")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		cache.mu.Lock()
		running := cache.last["eval-view"].refreshing
		cache.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("denied refresh did not release its cache flight")
}
