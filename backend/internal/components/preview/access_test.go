package preview

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPrivateCaptureOwnsAccessCancellationAndCleansItsToken(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan error, 1)
	work, revoke := context.WithCancel(t.Context())
	defer revoke()
	s := New("http://127.0.0.1", func(ctx context.Context, _ string, _, _ int) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	s.WithAccessGuard(func(context.Context, string) (context.Context, context.CancelFunc, error) {
		return work, func() {}, nil
	})
	go func() { _, err := s.Render(t.Context(), testPrincipal(), "", Target{Route: "/budget"}); stopped <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("capture did not start")
	}
	revoke()
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("capture lost cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("capture escaped owner lifetime")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) != 0 {
		t.Fatal("cancelled capture retained its private token")
	}
}

func TestDeniedCaptureDoesNotIssueATokenOrReachTheRenderer(t *testing.T) {
	denied := errors.New("owned access denied")
	s := New("http://127.0.0.1", func(context.Context, string, int, int) ([]byte, error) {
		t.Fatal("denied capture reached the renderer")
		return nil, nil
	})
	s.WithAccessGuard(func(ctx context.Context, _ string) (context.Context, context.CancelFunc, error) {
		work, release := context.WithCancel(ctx)
		release()
		return work, release, denied
	})
	if _, err := s.Render(t.Context(), testPrincipal(), "", Target{Route: "/budget"}); !errors.Is(err, denied) {
		t.Fatal("capture did not reject before issuing a token", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) != 0 {
		t.Fatal("denied capture issued a private credential")
	}
}
