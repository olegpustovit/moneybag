package identity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	idclient "github.com/olegpustovit/identity/idclient-go"
)

var ErrAccessDenied = errors.New("Moneybag access denied")
var ErrAccessUnavailable = errors.New("Moneybag access unavailable")

// ConfigureAccess runs once during composition, before requests or owner jobs.
func (s *Service) ConfigureAccess(issuer, clientID, secret string) error {
	client, err := idclient.NewAccessClient(idclient.AccessConfig{Issuer: issuer, ClientID: clientID, ClientSecret: secret, PositiveTTL: time.Minute})
	if err != nil {
		return errors.New("central access requires valid confidential Microapps ID credentials")
	}
	s.access = func(ctx context.Context, subject string, fresh bool) error {
		var decision idclient.AccessDecision
		var err error
		if fresh {
			decision, err = client.CheckFresh(ctx, subject)
		} else {
			decision, err = client.Check(ctx, subject)
		}
		if err != nil {
			return ErrAccessUnavailable
		}
		if !decision.Allowed {
			return ErrAccessDenied
		}
		return nil
	}
	return nil
}

func (s *Service) AccessEnforced() bool { return s != nil && s.access != nil }

// IdentitySubject uses the immutable local ownership binding, never an email or
// caller-supplied principal attribute. Disabled or unbound users fail closed.
func (s *Service) IdentitySubject(ctx context.Context, userID string) (string, error) {
	const subjectSQL = `SELECT i.subject,u.disabled FROM user_identities i JOIN users u ON u.id=i.user_id WHERE i.user_id=@user`
	var subject string
	var disabled bool
	err := s.repo.db.QueryRow(ctx, subjectSQL, pgx.NamedArgs{"user": userID}).Scan(&subject, &disabled)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (disabled || subject == "") {
		return "", ErrAccessDenied
	}
	if err != nil {
		return "", ErrAccessUnavailable
	}
	return subject, nil
}

func (s *Service) CheckSubject(ctx context.Context, subject string, fresh bool) error {
	if !s.AccessEnforced() {
		return nil
	}
	if subject == "" {
		return ErrAccessDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.access(ctx, subject, fresh)
}

func (s *Service) CheckAccess(ctx context.Context, userID string, fresh bool) error {
	if !s.AccessEnforced() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	subject, err := s.IdentitySubject(ctx, userID)
	if err != nil {
		return err
	}
	return s.CheckSubject(ctx, subject, fresh)
}

// WatchAccess requires a fresh initial decision, then rechecks independently of
// traffic every 25 seconds. The whole check is bounded to two seconds.
func (s *Service) WatchAccess(parent context.Context, userID string) (context.Context, context.CancelFunc, error) {
	if !s.AccessEnforced() {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel, nil
	}
	var expected string
	return watchAccess(parent, func(ctx context.Context) error {
		subject, err := s.IdentitySubject(ctx, userID)
		if err != nil {
			return err
		}
		if expected != "" && subject != expected {
			return ErrAccessDenied
		}
		expected = subject
		return s.CheckSubject(ctx, subject, true)
	})
}

func watchAccess(parent context.Context, check func(context.Context) error) (context.Context, context.CancelFunc, error) {
	ctx, stop := context.WithCancelCause(parent)
	done := make(chan struct{})
	// Fiber recycles its parent request context after the handler returns. Join
	// the checker before releasing that lifetime, including an in-flight check.
	cancel := func() { stop(context.Canceled); <-done }
	bounded := func() error {
		attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return check(attempt)
	}
	if err := bounded(); err != nil {
		stop(err)
		close(done)
		return ctx, cancel, err
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := bounded(); err != nil {
					stop(err)
					return
				}
			}
		}
	}()
	return ctx, cancel, nil
}
