package main

import (
	"context"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"log/slog"
	"sync"
	"time"
)

func nextDaily(now time.Time, hour int, loc *time.Location) time.Time {
	now = now.In(loc)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, loc)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
func daily(ctx context.Context, hour int, loc *time.Location, onStart bool, fn func(), wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		if onStart && ctx.Err() == nil {
			fn()
		}
		for {
			next := nextDaily(time.Now(), hour, loc)
			timer := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				fn()
			}
		}
	}()
}
func startJobs(parent context.Context, s mcpserver.Services, log *slog.Logger) func() {
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	users := func(name string, fn func(context.Context, principal.Principal) error) {
		job, stop := context.WithTimeout(ctx, 5*time.Minute)
		defer stop()
		e := s.Identity.EachUser(job, func(p principal.Principal) error {
			if e := fn(job, p); e != nil {
				log.Error("maintenance failed", "job", name, "user_id", p.UserID, "error", e)
			}
			return job.Err()
		})
		if e != nil {
			log.Error("maintenance user scan failed", "job", name, "error", e)
		}
	}
	daily(ctx, 3, time.UTC, false, func() {
		users("invariants", func(ctx context.Context, p principal.Principal) error {
			if e := s.Reconcile.Verify(ctx, p); e != nil {
				return e
			}
			return s.Ledger.Verify(ctx, p)
		})
	}, &wg)
	// The ECB publishes about 16:00 CET; sync at 17:00 Zagreb time and once at startup.
	zagreb, e := time.LoadLocation("Europe/Zagreb")
	if e != nil {
		log.Error("rate sync uses UTC; Europe/Zagreb unavailable", "error", e)
		zagreb = time.UTC
	}
	daily(ctx, 17, zagreb, s.Identity != nil && s.Rates != nil, func() {
		users("rates", func(ctx context.Context, p principal.Principal) error {
			p.Actor = principal.Actor{Kind: "system", ID: "rate-sync"}
			res, e := s.Rates.Sync(ctx, p, true, true)
			for _, st := range res.Providers {
				if st.Status == "error" {
					log.Warn("rate provider failed", "user_id", p.UserID, "provider", st.Provider, "failures", st.ConsecutiveFailures, "error", st.Error)
				}
			}
			return e
		})
	}, &wg)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				job, stop := context.WithTimeout(ctx, 5*time.Minute)
				e := s.Sharing.RefreshDue(job)
				stop()
				if e != nil {
					log.Error("shared snapshot refresh failed", "error", e)
				}
			}
		}
	}()
	return func() {
		cancel()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			log.Error("maintenance shutdown timed out")
		}
	}

}
