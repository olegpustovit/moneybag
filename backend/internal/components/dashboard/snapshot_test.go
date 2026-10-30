package dashboard

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
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

func TestSnapshotsKeepRendersDuringSourceChurn(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	builds := map[string]int{}
	read := func(kind, name string) {
		t.Helper()
		_, err := cache.read(expressions.WithVolatility(context.Background()), p, kind, name, nil, func() (any, error) {
			builds[kind+name]++
			return name, nil
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 7; i++ {
		read("render", fmt.Sprint(i))
	}
	for i := 0; i < 100; i++ {
		read("source", fmt.Sprint(i))
	}
	for i := 0; i < 7; i++ {
		read("render", fmt.Sprint(i))
		if builds["render"+fmt.Sprint(i)] != 1 {
			t.Fatalf("render %d was evicted", i)
		}
	}
	if len(cache.items) != 64 || len(cache.order) != 64 {
		t.Fatalf("cache size: %d items, %d ordered", len(cache.items), len(cache.order))
	}
	for i := 7; i < 64; i++ {
		read("render", fmt.Sprint(i))
	}
	read("source", "not-retained")
	read("source", "not-retained")
	if builds["sourcenot-retained"] != 2 || len(cache.items) != 64 {
		t.Fatal("a full render cache must not grow or evict a render for a source")
	}
	read("render", "new")
	read("render", "0")
	if builds["render0"] != 2 || len(cache.items) != 64 {
		t.Fatal("new renders must evict the oldest render when no sources remain")
	}
}

func TestSnapshotsKeepDashboardMetadataDuringSourceChurn(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	builds := map[string]int{}
	read := func(kind, name string) {
		t.Helper()
		_, err := cache.read(expressions.WithVolatility(context.Background()), p, kind, name, nil, func() (any, error) { builds[kind+name]++; return name, nil }, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 7; i++ {
		read("dashboard", fmt.Sprint(i))
		read("render", fmt.Sprint(i))
	}
	for i := 0; i < 120; i++ {
		read("source", fmt.Sprint(i))
	}
	for i := 0; i < 7; i++ {
		read("dashboard", fmt.Sprint(i))
		read("render", fmt.Sprint(i))
		if builds["dashboard"+fmt.Sprint(i)] != 1 || builds["render"+fmt.Sprint(i)] != 1 {
			t.Fatalf("dashboard %d evicted during source churn", i)
		}
	}
	if len(cache.items) != 64 {
		t.Fatalf("cache size: %d", len(cache.items))
	}
	for i := 7; i < 57; i++ {
		read("dashboard", fmt.Sprint(i))
	}
	read("source", "not-retained")
	read("source", "not-retained")
	if builds["sourcenot-retained"] != 2 || len(cache.items) != 64 {
		t.Fatal("source displaced protected cache entries")
	}
	read("dashboard", "new")
	read("dashboard", "0")
	if builds["dashboard0"] != 2 || len(cache.items) != 64 {
		t.Fatal("new metadata must evict the oldest protected entry when full")
	}
}

func TestSnapshotsDiscardOldRendersAfterWrites(t *testing.T) {
	cache := testSnapshots()
	owner := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	other := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	read := func(p principal.Principal, name string) {
		t.Helper()
		if _, err := cache.read(expressions.WithVolatility(context.Background()), p, "render", name, nil, func() (any, error) { return name, nil }, nil); err != nil {
			t.Fatal(err)
		}
	}
	read(other, "other")
	for range 12 {
		cache.Changed(owner.UserID, false)
		cache.Changed(owner.UserID, true)
		for i := 0; i < 7; i++ {
			read(owner, fmt.Sprint(i))
		}
		if len(cache.items) != 8 || len(cache.order) != 8 {
			t.Fatalf("stale renders accumulated: %d items, %d ordered", len(cache.items), len(cache.order))
		}
	}
	for key := range cache.items {
		if key.user == other.UserID {
			return
		}
	}
	t.Fatal("another user's render was invalidated")
}

func TestSnapshotsPruneOldRevisionDayAndExpiry(t *testing.T) {
	cache := testSnapshots()
	owner := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	other := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	var revision atomic.Int64
	revision.Store(1)
	cache.revision = func(_ context.Context, p principal.Principal) (int64, error) {
		if p.UserID == owner.UserID {
			return revision.Load(), nil
		}
		return 1, nil
	}
	read := func(p principal.Principal, kind, name string) {
		t.Helper()
		if _, err := cache.read(expressions.WithVolatility(context.Background()), p, kind, name, nil, func() (any, error) { return name, nil }, nil); err != nil {
			t.Fatal(err)
		}
	}
	read(owner, "render", "old")
	read(other, "render", "other")
	revision.Store(2)
	read(owner, "render", "new")
	for key := range cache.items {
		if key.user == owner.UserID && key.revision == 1 {
			t.Fatal("old owner revision was retained")
		}
	}
	if len(cache.items) != 2 {
		t.Fatalf("another user's cache was pruned: %d items", len(cache.items))
	}
	var currentDay string
	for key := range cache.items {
		currentDay = key.day
	}
	expired := snapshotKey{user: other.UserID, kind: "render", name: "expired", day: currentDay, revision: 1}
	oldDay := snapshotKey{user: other.UserID, kind: "render", name: "old-day", day: "old", revision: 1}
	cache.items[expired] = snapshotItem{value: 1, expires: time.Now().Add(-time.Second)}
	cache.items[oldDay] = snapshotItem{value: 1}
	cache.order = append(cache.order, expired, oldDay)
	read(owner, "source", "current")
	if _, ok := cache.items[expired]; ok {
		t.Fatal("expired render was retained")
	}
	if _, ok := cache.items[oldDay]; ok {
		t.Fatal("old-day render was retained")
	}
}

func TestCachedSourceQueryCopiesResultsAndChargesOriginalCost(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	q := Query{From: "postings", Where: "row.amount > 0"}
	params := map[string]any{"month": "2026-10"}
	key := struct {
		Query  Query
		Params any
	}{q, params}
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	ctx := expressions.WithVolatility(context.Background())
	_, err := cache.read(ctx, p, "query", q.From, key, func() (any, error) {
		expressions.ExpireAt(ctx, expires)
		return queryResult{value: []map[string]any{{"nested": map[string]any{"value": float64(1)}, "bson": bson.A{bson.D{{Key: "value", Value: 1}}}}}, cost: 17, count: 3}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		ctx := expressions.WithVolatility(context.Background())
		x := &execution{ctx: ctx, p: p, engine: &Engine{Reader: &Sources{Snapshots: cache}}, vars: map[string]any{"params": params}}
		value, err := x.cachedSourceQuery(q)
		if err != nil || x.cost != 17 || x.count != 3 || !expressions.Expiry(ctx).Equal(expires) {
			t.Fatalf("cached accounting or expiry: %d %d %v %v", x.cost, x.count, expressions.Expiry(ctx), err)
		}
		nested := value.([]map[string]any)[0]["nested"].(map[string]any)
		if nested["value"] != float64(1) {
			t.Fatalf("cached value mutated: %v", nested)
		}
		nested["value"] = float64(999)
		bsonValue := value.([]map[string]any)[0]["bson"].(bson.A)[0].(bson.D)
		if bsonValue[0].Value != 1 {
			t.Fatalf("cached BSON mutated: %v", bsonValue)
		}
		bsonValue[0].Value = 999
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

type observingContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *observingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestSnapshotWaiterRetriesCancelledLeader(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	leader, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	leaderDone := make(chan error, 1)
	go func() {
		_, err := cache.read(leader, p, "source", "rows", nil, func() (any, error) { close(started); <-leader.Done(); return nil, leader.Err() }, nil)
		leaderDone <- err
	}()
	<-started
	waiterCtx := &observingContext{Context: context.Background(), observed: make(chan struct{})}
	waiterDone := make(chan error, 1)
	go func() {
		value, err := cache.read(waiterCtx, p, "source", "rows", nil, func() (any, error) { return 42, nil }, nil)
		if err == nil && value != 42 {
			err = fmt.Errorf("unexpected value: %v", value)
		}
		waiterDone <- err
	}()
	<-waiterCtx.observed
	cancel()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader: %v", err)
	}
	if err := <-waiterDone; err != nil {
		t.Fatalf("waiter: %v", err)
	}
}

func TestSnapshotCancelledWaiterDoesNotCancelLeader(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	started, release := make(chan struct{}), make(chan struct{})
	leaderDone := make(chan error, 1)
	go func() {
		_, err := cache.read(context.Background(), p, "source", "rows", nil, func() (any, error) { close(started); <-release; return 42, nil }, nil)
		leaderDone <- err
	}()
	<-started
	waiter, cancel := context.WithCancel(context.Background())
	waiterCtx := &observingContext{Context: waiter, observed: make(chan struct{})}
	waiterDone := make(chan error, 1)
	go func() {
		_, err := cache.read(waiterCtx, p, "source", "rows", nil, func() (any, error) { return nil, fmt.Errorf("unexpected rebuild") }, nil)
		waiterDone <- err
	}()
	<-waiterCtx.observed
	cancel()
	if err := <-waiterDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter: %v", err)
	}
	close(release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader: %v", err)
	}
	value, err := cache.read(context.Background(), p, "source", "rows", nil, func() (any, error) { return nil, fmt.Errorf("unexpected rebuild") }, nil)
	if err != nil || value != 42 {
		t.Fatalf("cached: %v, %v", value, err)
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

func TestNestedLeafMemoizationAndVolatility(t *testing.T) {
	for _, tc := range []struct {
		query Query
		want  bool
	}{
		{Query{From: "postings", Where: `row.account == 'assets:cash'`}, true},
		{Query{From: "journal", Derive: map[string]string{"x": "now()"}}, false},
		{Query{From: "effective_entries", Where: `row.name == 'now'`}, false},
		{Query{From: "view:other"}, false}, {Query{From: "scenarios"}, false},
	} {
		if got := memoizableLeaf(View{ID: "test", Query: tc.query}); got != tc.want {
			t.Fatalf("%+v: got %t", tc.query, got)
		}
	}
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	base := reqcache.With(context.Background())
	first := expressions.WithVolatility(base)
	second := expressions.WithVolatility(base)
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	var calls atomic.Int64
	read := func() ([]map[string]any, error) {
		calls.Add(1)
		expressions.ExpireAt(first, expires)
		expressions.MarkVolatile(first)
		return []map[string]any{{"n": 1}}, nil
	}
	a, err := cachedView(first, p, 0, "view:leaf", "{}", read)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cachedView(second, p, 0, "view:leaf", "{}", read)
	if err != nil || calls.Load() != 1 || len(a) != 1 || len(b) != 1 || !expressions.Volatile(second) || !expressions.Expiry(second).Equal(expires) {
		t.Fatalf("cache/metadata: calls=%d, expiry=%v, err=%v", calls.Load(), expressions.Expiry(second), err)
	}
	_, err = cachedView(second, p, 0, "view:leaf", `{"from":"2025-01-01"}`, read)
	if err != nil || calls.Load() != 2 {
		t.Fatalf("params isolation: calls=%d, err=%v", calls.Load(), err)
	}
	other := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	_, err = cachedView(second, other, 0, "view:leaf", "{}", read)
	if err != nil || calls.Load() != 3 {
		t.Fatalf("owner isolation: calls=%d, err=%v", calls.Load(), err)
	}
}

func TestMemoizableViewChains(t *testing.T) {
	views := map[string]View{
		"leaf":    {ID: "leaf", Query: Query{From: "postings"}},
		"middle":  {ID: "middle", Query: Query{From: "view:leaf"}},
		"now":     {ID: "now", Query: Query{From: "view:leaf", Where: "now() != null"}},
		"unsafe":  {ID: "unsafe", Query: Query{From: "scenarios"}},
		"a":       {ID: "a", Query: Query{From: "view:b"}},
		"b":       {ID: "b", Query: Query{From: "view:a"}},
		"missing": {ID: "missing", Query: Query{From: "view:missing-child"}},
	}
	load := func(id string) (View, bool) { v, ok := views[id]; return v, ok }
	for _, tc := range []struct {
		id    string
		stack []string
		want  bool
	}{
		{"leaf", nil, true}, {"middle", []string{"root"}, true},
		{"now", nil, false}, {"unsafe", nil, false}, {"a", nil, false},
		{"missing", nil, false}, {"middle", []string{"leaf"}, false},
		{"middle", []string{"1", "2", "3", "4", "5", "6", "7"}, false},
		{"middle", []string{"1", "2", "3", "4", "5", "6"}, true},
	} {
		if got := memoizableChain(views[tc.id], tc.stack, load); got != tc.want {
			t.Fatalf("%s with %v: got %t, want %t", tc.id, tc.stack, got, tc.want)
		}
	}
}

func TestNestedLeafCoalescesConcurrentReads(t *testing.T) {
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	ctx := reqcache.With(context.Background())
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cachedView(expressions.WithVolatility(ctx), p, 0, "view:leaf", "{}", func() ([]map[string]any, error) {
				if calls.Add(1) == 1 {
					close(started)
				}
				<-release
				return []map[string]any{{"n": 1}}, nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("built %d times", calls.Load())
	}
}

func TestLeafMemoEpochChangesAcrossWrites(t *testing.T) {
	cache := testSnapshots()
	p := testPrincipal("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	ctx := reqcache.With(context.Background())
	var calls int
	read := func() ([]map[string]any, error) { calls++; return []map[string]any{{"n": calls}}, nil }
	epoch, stable := cache.memoEpoch(p.UserID)
	if !stable {
		t.Fatal("unexpected active write")
	}
	if _, err := cachedView(ctx, p, epoch, "view:leaf", "{}", read); err != nil {
		t.Fatal(err)
	}
	cache.Changed(p.UserID, false)
	if _, stable = cache.memoEpoch(p.UserID); stable {
		t.Fatal("active write must bypass request memo")
	}
	cache.Changed(p.UserID, true)
	next, stable := cache.memoEpoch(p.UserID)
	if !stable || next == epoch {
		t.Fatal("epoch did not advance")
	}
	rows, err := cachedView(ctx, p, next, "view:leaf", "{}", read)
	if err != nil || calls != 2 || rows[0]["n"] != 2 {
		t.Fatalf("stale leaf reused: %v, %v, %d", rows, err, calls)
	}
}
