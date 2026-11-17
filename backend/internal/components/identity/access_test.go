package identity

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
)

func TestWatchedAccessUsesProductionIntervalAndDeadline(t *testing.T) {
	for _, cause := range []error{ErrAccessDenied, ErrAccessUnavailable} {
		synctest.Test(t, func(t *testing.T) {
			var calls atomic.Int32
			ctx, release, err := watchAccess(t.Context(), func(context.Context) error {
				if calls.Add(1) > 1 {
					return cause
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			time.Sleep(25 * time.Second)
			synctest.Wait()
			if !errors.Is(context.Cause(ctx), cause) || calls.Load() != 2 {
				t.Fatal("idle work escaped fresh recheck")
			}
		})
	}
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		ctx, release, err := watchAccess(t.Context(), func(ctx context.Context) error {
			if calls.Add(1) > 1 {
				<-ctx.Done()
				return ErrAccessUnavailable
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		time.Sleep(27 * time.Second)
		synctest.Wait()
		if !errors.Is(context.Cause(ctx), ErrAccessUnavailable) {
			t.Fatal("check exceeded the two-second deadline")
		}
	})
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		_, release, err := watchAccess(t.Context(), func(context.Context) error { calls.Add(1); return nil })
		if err != nil {
			t.Fatal(err)
		}
		release()
		synctest.Wait()
		time.Sleep(time.Minute)
		if calls.Load() != 1 {
			t.Fatal("released work kept checking")
		}
	})
}

func TestAccessUsesStoredSubjectAndRejectsAnOldCachedInitialDecision(t *testing.T) {
	pool := pgtest.New(t)
	s := NewService(NewPostgresRepository(pool))
	u := identityFixtureUser(t, pool, "eval-access@example.test", true)
	if _, err := s.LinkIdentity(t.Context(), "usr_eval_original", u.Email); err != nil {
		t.Fatal(err)
	}
	var fresh atomic.Int32
	s.access = func(_ context.Context, subject string, force bool) error {
		if subject != "usr_eval_original" {
			t.Error("wrong stored subject")
		}
		if force {
			fresh.Add(1)
			return ErrAccessDenied
		}
		return nil
	}
	if err := s.CheckAccess(t.Context(), u.ID, false); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := s.WatchAccess(t.Context(), u.ID)
	defer release()
	if !errors.Is(err, ErrAccessDenied) || !errors.Is(context.Cause(ctx), ErrAccessDenied) || fresh.Load() != 1 {
		t.Fatal("old allow extended watched work")
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE user_identities RENAME TO eval_unavailable_identities`); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAccess(t.Context(), u.ID, false); !errors.Is(err, ErrAccessUnavailable) {
		t.Fatal("storage failure did not fail closed", err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE eval_unavailable_identities RENAME TO user_identities`); err != nil {
		t.Fatal(err)
	}
	other := identityFixtureUser(t, pool, "eval-unbound@example.test", false)
	if err := s.CheckAccess(t.Context(), other.ID, false); !errors.Is(err, ErrAccessDenied) {
		t.Fatal("unbound user admitted")
	}
	if _, err := s.LinkIdentity(t.Context(), "usr_eval_original", "changed@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAccess(t.Context(), u.ID, false); err != nil {
		t.Fatal("email change moved binding", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET disabled=true WHERE id=@id`, pgx.NamedArgs{"id": u.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAccess(t.Context(), u.ID, false); !errors.Is(err, ErrAccessDenied) {
		t.Fatal("disabled local user admitted")
	}
}

func TestAccessReleaseJoinsAnInflightCheckBeforeParentReuse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		started, unwind, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
		_, release, err := watchAccess(t.Context(), func(ctx context.Context) error {
			if calls.Add(1) == 1 {
				return nil
			}
			close(started)
			<-ctx.Done()
			<-unwind
			return ErrAccessUnavailable
		})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Second)
		synctest.Wait()
		<-started
		go func() { release(); close(released) }()
		synctest.Wait()
		select {
		case <-released:
			t.Fatal("parent released while a checker still references it")
		default:
		}
		close(unwind)
		synctest.Wait()
		<-released
	})
}
