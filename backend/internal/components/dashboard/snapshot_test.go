package dashboard

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func testSnapshots() *Snapshots {
	return &Snapshots{revision: func(context.Context, principal.Principal) (int64, error) { return 1, nil }, zone: time.UTC, items: map[snapshotKey]snapshotItem{}, pending: map[snapshotKey]*snapshotFlight{}, users: map[string]snapshotState{}}
}
func testPrincipal(id string) principal.Principal {
	return principal.Principal{UserID: id, Scopes: []string{"ledger:read"}}
}

func TestSnapshotsInvalidateAcrossWriteAndIsolateOwners(t *testing.T) {
	cache := testSnapshots()
	a := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	b := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	var builds atomic.Int64
	read := func(p principal.Principal) int64 {
		v, err := cache.read(expressions.WithVolatility(context.Background()), p, "source", "journal", nil, func() (any, error) { return builds.Add(1), nil }, nil)
		if err != nil {
			t.Fatal(err)
		}
		return v.(int64)
	}
	if read(a) != 1 || read(a) != 1 || read(b) != 2 {
		t.Fatal("cache miss or owner isolation")
	}
	cache.Changed(a.UserID, false)
	if read(a) != 3 || read(a) != 4 {
		t.Fatal("write must bypass cache")
	}
	cache.Changed(a.UserID, true)
	if read(a) != 5 || read(a) != 5 || read(b) != 2 {
		t.Fatal("write did not invalidate owner only")
	}
}

func TestSnapshotsCoalesceAndDiscardInFlightDuringWrite(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	started, release := make(chan struct{}), make(chan struct{})
	var builds atomic.Int64
	build := func() (any, error) {
		if builds.Add(1) == 1 {
			close(started)
			<-release
		}
		return builds.Load(), nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = cache.read(expressions.WithVolatility(context.Background()), p, "source", "rows", nil, build, nil)
	}()
	<-started
	cache.Changed(p.UserID, false)
	cache.Changed(p.UserID, true)
	close(release)
	wg.Wait()
	v, err := cache.read(expressions.WithVolatility(context.Background()), p, "source", "rows", nil, build, nil)
	if err != nil || v.(int64) != 2 {
		t.Fatalf("stale in-flight result: %v %v", v, err)
	}
}

func TestSnapshotsNeverStoreVolatileResults(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	var builds int
	for range 2 {
		ctx := expressions.WithVolatility(context.Background())
		_, err := cache.read(ctx, p, "source", "clock", nil, func() (any, error) { builds++; expressions.MarkVolatile(ctx); return builds, nil }, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if builds != 2 {
		t.Fatalf("cached volatile result: %d builds", builds)
	}
}

func TestSnapshotsExpireScheduledValues(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	var builds int
	for range 2 {
		ctx := expressions.WithVolatility(context.Background())
		_, err := cache.read(ctx, p, "source", "rate", nil, func() (any, error) {
			builds++
			expressions.ExpireAt(ctx, time.Now().Add(-time.Second))
			return builds, nil
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if builds != 2 {
		t.Fatalf("expired result reused: %d", builds)
	}
}

func TestSnapshotsCoalesceConcurrentReads(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	started, release := make(chan struct{}), make(chan struct{})
	var builds atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cache.read(expressions.WithVolatility(context.Background()), p, "source", "rows", nil, func() (any, error) {
				if builds.Add(1) == 1 {
					close(started)
				}
				<-release
				return 1, nil
			}, nil)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if builds.Load() != 1 {
		t.Fatalf("concurrent builds: %d", builds.Load())
	}
}

func TestSnapshotHitPropagatesFutureExpiry(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	at := time.Now().Add(time.Hour).Truncate(time.Second)
	first := expressions.WithVolatility(context.Background())
	_, err := cache.read(first, p, "source", "rate", nil, func() (any, error) { expressions.ExpireAt(first, at); return 1, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := expressions.WithVolatility(context.Background())
	_, err = cache.read(second, p, "source", "rate", nil, func() (any, error) { t.Fatal("unexpected rebuild"); return nil, nil }, nil)
	if err != nil || !expressions.Expiry(second).Equal(at) {
		t.Fatalf("expiry propagation: %v, %v", expressions.Expiry(second), err)
	}
}

func TestSourceCloneIsolatesNestedValues(t *testing.T) {
	original := map[string]any{"groups": map[string]any{"name": "original"}, "tags": []any{"a"}}
	copy := cloneSourceValue(original).(map[string]any)
	copy["groups"].(map[string]any)["name"] = "changed"
	copy["tags"].([]any)[0] = "b"
	if original["groups"].(map[string]any)["name"] != "original" || original["tags"].([]any)[0] != "a" {
		t.Fatal("nested source values shared")
	}
}
