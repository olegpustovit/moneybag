package dashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/klauspost/compress/gzip"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
)

// Encoded is an immutable JSON response: gzip bytes and an ETag over the JSON they hold.
type Encoded struct {
	Gzip []byte
	ETag string
}

// Encode marshals v once so every later read serves the same bytes.
func Encode(v any) (Encoded, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return Encoded{}, err
	}
	return encodeRaw(raw)
}

func encodeRaw(raw []byte) (Encoded, error) {
	return encodeTagged(raw, raw)
}

// encodeView leaves as_of out of the ETag: a rebuild with the same numbers must still answer 304.
// as_of and stale are the last fields, so user content cannot be mistaken for them.
func encodeView(raw []byte) (Encoded, error) {
	asOf, stale := bytes.LastIndex(raw, []byte(`"as_of":`)), bytes.LastIndex(raw, []byte(`"stale":`))
	if asOf < 0 || stale < asOf {
		return Encoded{}, fmt.Errorf("view body has no trailing as_of and stale fields")
	}
	return encodeTagged(raw, append(raw[:asOf:asOf], raw[stale:]...))
}

func encodeTagged(raw, tagged []byte) (Encoded, error) {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(raw); err != nil {
		return Encoded{}, err
	}
	if err := w.Close(); err != nil {
		return Encoded{}, err
	}
	sum := sha256.Sum256(tagged)
	return Encoded{Gzip: b.Bytes(), ETag: `W/"` + hex.EncodeToString(sum[:16]) + `"`}, nil
}

// Raw decompresses for the rare client that does not accept gzip.
func (e Encoded) Raw() ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(e.Gzip))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// PageView is the browser's one read of a dashboard. Stale must stay the last field: staleEncoded rewrites it.
type PageView struct {
	Head       Head           `json:"head"`
	Spec       Spec           `json:"spec"`
	Version    int            `json:"version"`
	Status     string         `json:"status"`
	Pages      []Page         `json:"pages"`
	Values     map[string]any `json:"values"`
	Computed   map[string]any `json:"computed,omitempty"`
	Validation Validation     `json:"validation"`
	AsOf       string         `json:"as_of"`
	Stale      bool           `json:"stale"`
}

type viewEntry struct {
	encoded Encoded
	ok      bool
	once    sync.Once
	stale   Encoded
	err     error
}

// staleEncoded is built on the first stale read, which is rare, so fresh reads never pay for it.
func (e *viewEntry) staleEncoded() (Encoded, error) {
	e.once.Do(func() {
		raw, err := e.encoded.Raw()
		if err != nil {
			e.err = err
			return
		}
		trimmed, found := bytes.CutSuffix(raw, []byte(`"stale":false}`))
		if !found {
			e.err = fmt.Errorf("view body has no trailing stale field")
			return
		}
		e.stale, e.err = encodeView(append(trimmed, `"stale":true}`...))
	})
	return e.stale, e.err
}

// PageView serves the last good render while only rates changed; ledger and spec writes always rebuild.
func (s *Service) PageView(ctx context.Context, p principal.Principal, id, version string, params map[string]any) (Encoded, error) {
	return s.pageView(ctx, p, id, version, params, true)
}

// FreshPageView never serves a stale render; the agent's preview must show current numbers.
func (s *Service) FreshPageView(ctx context.Context, p principal.Principal, id, version string, params map[string]any) (Encoded, error) {
	return s.pageView(ctx, p, id, version, params, false)
}

func (s *Service) pageView(ctx context.Context, p principal.Principal, id, version string, params map[string]any, allowStale bool) (Encoded, error) {
	if err := p.Require("ledger:read"); err != nil {
		return Encoded{}, err
	}
	if len(params) == 0 {
		params = nil
	}
	ctx = expressions.WithVolatility(reqcache.With(ctx))
	if s.snapshots == nil || mongodb.InWorkflow(ctx) {
		// Scoped services have no snapshots, so a grant's restricted view is never cached.
		entry, err := s.buildView(ctx, p, id, version, params)
		if err != nil {
			return Encoded{}, err
		}
		return entry.encoded, nil
	}
	raw, err := json.Marshal(struct {
		Version string
		Params  map[string]any
	}{version, params})
	if err != nil {
		return Encoded{}, err
	}
	key := p.UserID + "|" + id + "|" + string(raw)
	if allowStale {
		if entry := s.snapshots.staleView(ctx, p, key); entry != nil {
			s.refreshView(p, id, version, params, key)
			return entry.staleEncoded()
		}
	}
	entry, err := s.cachedView(ctx, p, id, version, params, key)
	if err != nil {
		return Encoded{}, err
	}
	return entry.encoded, nil
}

func (s *Service) cachedView(ctx context.Context, p principal.Principal, id, version string, params map[string]any, key string) (*viewEntry, error) {
	generation, quiet := s.snapshots.memoEpoch(p.UserID)
	revisions, err := s.snapshots.revisions(ctx, p)
	if err != nil {
		return nil, err
	}
	value, err := s.snapshots.read(ctx, p, "view", id, key, func() (any, error) {
		return s.buildView(ctx, p, id, version, params)
	}, func(v any) bool { return v.(*viewEntry).ok })
	if err != nil {
		return nil, err
	}
	entry := value.(*viewEntry)
	if entry.ok && quiet && !expressions.Volatile(ctx) {
		s.snapshots.rememberView(p.UserID, key, entry, revisions, generation, expressions.Expiry(ctx))
	}
	return entry, nil
}

// refreshView rebuilds in the background; only one refresh per view runs at a time.
func (s *Service) refreshView(p principal.Principal, id, version string, params map[string]any, key string) {
	if !s.snapshots.startRefresh(key) {
		return
	}
	go func() {
		defer s.snapshots.finishRefresh(key)
		defer func() {
			if failure := recover(); failure != nil {
				slog.Error("dashboard view refresh panicked", "dashboard", id, "panic", failure)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*evalBudget)
		defer cancel()
		ctx = expressions.WithVolatility(reqcache.With(ctx))
		entry, err := s.cachedView(ctx, p, id, version, params, key)
		if err != nil {
			slog.Warn("dashboard view refresh failed", "dashboard", id, "error", err)
			return
		}
		if !entry.ok {
			// Show the failure on the next read rather than an old render forever.
			s.snapshots.forgetView(key)
		}
	}()
}

func (s *Service) buildView(ctx context.Context, p principal.Principal, id, version string, params map[string]any) (*viewEntry, error) {
	head, err := s.head(ctx, p, id)
	if err != nil {
		return nil, err
	}
	current, err := s.Get(ctx, p, id, version)
	if err != nil {
		return nil, err
	}
	rendered, err := s.rendered(ctx, p, id, version, params)
	if err != nil {
		return nil, err
	}
	pages := rendered.Pages
	if pages == nil {
		pages = []Page{}
	}
	raw, err := json.Marshal(PageView{Head: head, Spec: current.Spec, Version: current.Version, Status: current.Status, Pages: pages, Values: rendered.Values, Computed: rendered.Computed, Validation: rendered.Validation, AsOf: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	encoded, err := encodeView(raw)
	if err != nil {
		return nil, err
	}
	return &viewEntry{encoded: encoded, ok: rendered.Validation.OK}, nil
}

// Cached keeps an encoded response until the user's next write, for other one-round-trip reads.
func (s *Service) Cached(ctx context.Context, p principal.Principal, kind, name string, params any, build func() (Encoded, error)) (Encoded, error) {
	if s.snapshots == nil {
		return build()
	}
	value, err := s.snapshots.read(ctx, p, kind, name, params, func() (any, error) { return build() }, nil)
	if err != nil {
		return Encoded{}, err
	}
	return value.(Encoded), nil
}

// lastView is the newest good render of a view, kept across writes so rate-only changes can serve it.
type lastView struct {
	entry      *viewEntry
	revisions  audit.Revisions
	day        string
	expires    time.Time
	refreshing bool
}

func (s *Snapshots) day(now time.Time) string {
	return now.In(s.zone).Format("2006-01-02") + "|" + now.Format("2006-01-02") + "|" + now.UTC().Format("2006-01-02")
}

func (s *Snapshots) staleView(ctx context.Context, p principal.Principal, key string) *viewEntry {
	current, err := s.revisions(ctx, p)
	if err != nil {
		return nil
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	last := s.last[key]
	if last == nil || last.revisions.Data == current.Data {
		return nil
	}
	if last.revisions.Ledger() != current.Ledger() || last.day != s.day(now) || (!last.expires.IsZero() && !now.Before(last.expires)) {
		delete(s.last, key)
		return nil
	}
	return last.entry
}

// shortcut: 32 remembered views evicted arbitrarily, a miss only costs one foreground render; upgrade to LRU if users hold more views.
func (s *Snapshots) rememberView(user, key string, entry *viewEntry, revisions audit.Revisions, generation uint64, expires time.Time) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.users[user]
	if state.writes != 0 || state.generation != generation {
		return
	}
	if s.last[key] == nil && len(s.last) >= 32 {
		for old := range s.last {
			delete(s.last, old)
			break
		}
	}
	s.last[key] = &lastView{entry: entry, revisions: revisions, day: s.day(now), expires: expires, refreshing: s.last[key] != nil && s.last[key].refreshing}
}

func (s *Snapshots) forgetView(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.last, key)
}

func (s *Snapshots) startRefresh(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := s.last[key]
	if last == nil || last.refreshing {
		return false
	}
	last.refreshing = true
	return true
}

func (s *Snapshots) finishRefresh(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last := s.last[key]; last != nil {
		last.refreshing = false
	}
}
