package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

type snapshotKey struct {
	user, kind, name, params, day string
	revision                      int64
	generation                    uint64
}
type snapshotFlight struct {
	done      chan struct{}
	value     any
	err       error
	volatile  bool
	abandoned bool
	expires   time.Time
}
type snapshotItem struct {
	value   any
	expires time.Time
}
type snapshotState struct {
	generation uint64
	writes     int
}

// Snapshots keeps immutable read results in this single process.
type Snapshots struct {
	mu       sync.Mutex
	revision func(context.Context, principal.Principal) (int64, error)
	zone     *time.Location
	items    map[snapshotKey]snapshotItem
	order    []snapshotKey
	pending  map[snapshotKey]*snapshotFlight
	users    map[string]snapshotState
}

func NewSnapshots(a *audit.Service) (*Snapshots, error) {
	zone, err := time.LoadLocation("Europe/Zagreb")
	if err != nil {
		return nil, fmt.Errorf("load snapshot timezone: %w", err)
	}
	return &Snapshots{revision: a.Revision, zone: zone, items: map[snapshotKey]snapshotItem{}, pending: map[snapshotKey]*snapshotFlight{}, users: map[string]snapshotState{}}, nil
}

// Changed invalidates readers both before and after a potentially partial write.
func (s *Snapshots) Changed(user string, finished bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.users[user]
	state.generation++
	if finished {
		if state.writes > 0 {
			state.writes--
		}
	} else {
		state.writes++
	}
	s.users[user] = state
}

func (s *Snapshots) memoEpoch(user string) (uint64, bool) {
	if s == nil {
		return 0, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.users[user]
	return state.generation, state.writes == 0
}

func (s *Snapshots) read(ctx context.Context, p principal.Principal, kind, name string, params any, build func() (any, error), cacheable func(any) bool) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if mongodb.InWorkflow(ctx) {
		return build()
	}
	if err := p.Require("ledger:read"); err != nil {
		return nil, err
	}
	if expressions.Volatile(ctx) {
		return build()
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	revision, err := s.revision(ctx, p)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	state := s.users[p.UserID]
	if state.writes != 0 {
		s.mu.Unlock()
		return build()
	}
	now := time.Now()
	day := now.In(s.zone).Format("2006-01-02") + "|" + now.Format("2006-01-02") + "|" + now.UTC().Format("2006-01-02")
	key := snapshotKey{user: p.UserID, kind: kind, name: name, params: string(encoded), day: day, revision: revision, generation: state.generation}
	if item, ok := s.items[key]; ok {
		if item.expires.IsZero() || now.Before(item.expires) {
			s.mu.Unlock()
			expressions.ExpireAt(ctx, item.expires)
			return item.value, nil
		}
		delete(s.items, key)
		for i, old := range s.order {
			if old == key {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	}
	if flight := s.pending[key]; flight != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-flight.done:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if flight.abandoned {
				return s.read(ctx, p, kind, name, params, build, cacheable)
			}
			if flight.err == nil && !flight.expires.IsZero() && !time.Now().Before(flight.expires) {
				return s.read(ctx, p, kind, name, params, build, cacheable)
			}
			if flight.volatile {
				expressions.MarkVolatile(ctx)
			}
			expressions.ExpireAt(ctx, flight.expires)
			return flight.value, flight.err
		}
	}
	flight := &snapshotFlight{done: make(chan struct{})}
	s.pending[key] = flight
	s.mu.Unlock()
	defer func() {
		if failure := recover(); failure != nil {
			s.mu.Lock()
			flight.err = fmt.Errorf("snapshot build failed")
			delete(s.pending, key)
			close(flight.done)
			s.mu.Unlock()
			panic(failure)
		}
	}()
	flight.value, flight.err = build()
	flight.abandoned = ctx.Err() != nil
	flight.volatile = expressions.Volatile(ctx)
	flight.expires = expressions.Expiry(ctx)
	canStore := cacheable == nil || flight.err == nil && cacheable(flight.value)
	s.mu.Lock()
	state = s.users[p.UserID]
	if flight.err == nil && ctx.Err() == nil && !flight.volatile && state.writes == 0 && state.generation == key.generation && canStore {
		// shortcut: FIFO limits snapshots to 64 entries; upgrade to byte-aware eviction if memory requires it.
		if len(s.order) >= 64 {
			delete(s.items, s.order[0])
			s.order = s.order[1:]
		}
		s.items[key] = snapshotItem{value: flight.value, expires: flight.expires}
		s.order = append(s.order, key)
	}
	delete(s.pending, key)
	close(flight.done)
	s.mu.Unlock()
	return flight.value, flight.err
}
