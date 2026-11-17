package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

type warmDashboard interface {
	List(context.Context, principal.Principal) ([]dashboard.Head, error)
	Preview(context.Context, principal.Principal, string, string, map[string]any) (dashboard.Rendered, error)
}

type warmTask struct {
	timer  *time.Timer
	cancel context.CancelFunc
}

// prewarmDebounce is a var so tests need not wait for it.
var prewarmDebounce = 2 * time.Second

type ownerAccess func(context.Context, string) (context.Context, context.CancelFunc, error)

func prewarmer(parent context.Context, snapshots *dashboard.Snapshots, service warmDashboard, log *slog.Logger, guards ...ownerAccess) (func(string, bool), func()) {
	var mu sync.Mutex
	var workers sync.WaitGroup
	stopped := false
	tasks := map[string]*warmTask{}
	change := func(user string, finished bool) {
		snapshots.Changed(user, finished)
		mu.Lock()
		defer mu.Unlock()
		if task := tasks[user]; task != nil {
			task.timer.Stop()
			task.cancel()
			delete(tasks, user)
		}
		if stopped || !finished {
			return
		}
		ctx, cancel := context.WithCancel(parent)
		task := &warmTask{cancel: cancel}
		task.timer = time.AfterFunc(prewarmDebounce, func() {
			mu.Lock()
			if stopped || tasks[user] != task {
				mu.Unlock()
				return
			}
			workers.Add(1)
			mu.Unlock()
			defer workers.Done()
			defer func() {
				mu.Lock()
				if tasks[user] == task {
					delete(tasks, user)
				}
				mu.Unlock()
				cancel()
			}()
			work := ctx
			if len(guards) > 0 {
				next, release, err := guards[0](ctx, user)
				if err != nil {
					cancel()
					return
				}
				defer release()
				work = next
			}
			p := principal.Principal{UserID: user, Scopes: []string{"ledger:read"}, Actor: principal.Actor{Kind: "system", ID: "prewarm"}}
			heads, err := service.List(work, p)
			if err == nil {
				for _, head := range heads {
					if head.PublishedVersion == 0 || work.Err() != nil {
						continue
					}
					if _, previewErr := service.Preview(work, p, head.ID, "published", nil); previewErr != nil && work.Err() == nil {
						log.Warn("dashboard prewarm failed", "dashboard", head.ID, "error", previewErr)
					}
				}
			}
			if err != nil && ctx.Err() == nil {
				log.Warn("dashboard prewarm failed", "error", err)
			}
		})
		tasks[user] = task
	}
	stop := func() {
		mu.Lock()
		stopped = true
		for _, task := range tasks {
			task.timer.Stop()
			task.cancel()
		}
		mu.Unlock()
		workers.Wait()
	}
	return change, stop
}
