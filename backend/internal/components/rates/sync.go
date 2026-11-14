package rates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/safefetch"
)

const (
	ecbURL       = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist-90d.xml"
	goldAPIURL   = "https://api.gold-api.com/price/XAU"
	swissURL     = "https://forex-data-feed.swissquote.com/public-quotes/bboquotes/instrument/XAU/USD"
	ecbSource    = "ECB daily reference"
	goldSource   = "gold-api.com spot"
	swissSource  = "Swissquote spot"
	gramsPerOz   = "31.1035"
	maxFeedBytes = 1024 * 1024
)

type endpoints struct{ ecb, gold, swiss, ecbData string }

// SetHTTPClient and SetEndpoints let tests serve the providers locally.
func (s *Service) SetHTTPClient(c *http.Client) { s.http = c }
func (s *Service) SetEndpoints(ecb, gold, swiss string) {
	s.urls = endpoints{ecb, gold, swiss, s.urls.ecbData}
}
func (s *Service) SetECBDataURL(base string) { s.urls.ecbData = base }

// ProviderState is the persisted outcome of the latest sync for one user and provider.
type ProviderState struct {
	ID                  string `json:"-" db:"id"`
	UserID              string `json:"-" db:"user_id"`
	Provider            string `json:"provider" db:"provider"`
	Status              string `json:"status" db:"status"` // ok | error | skipped | never
	LastAttempt         string `json:"last_attempt,omitempty" db:"last_attempt"`
	LastSuccess         string `json:"last_success,omitempty" db:"last_success"`
	Error               string `json:"error,omitempty" db:"error"`
	ConsecutiveFailures int    `json:"consecutive_failures" db:"consecutive_failures"`
	NewestAt            string `json:"newest_at,omitempty" db:"newest_at"`
	NewestValue         string `json:"newest_value,omitempty" db:"newest_value"`
}
type SyncResult struct {
	Written   []Quote         `json:"written"`
	Providers []ProviderState `json:"providers"`
}

var providers = []string{"ecb", "gold-api", "swissquote"}

// SyncStatus returns the state of every provider, "never" when it has not run.
func (s *Service) SyncStatus(ctx context.Context, p principal.Principal) ([]ProviderState, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	const statusSQL = `SELECT * FROM rate_sync WHERE user_id=@user`
	rows, e := s.repo.db.Query(ctx, statusSQL, pgx.NamedArgs{"user": p.UserID})
	if e != nil {
		return nil, e
	}
	have, e := pgx.CollectRows(rows, pgx.RowToStructByName[ProviderState])
	if e != nil {
		return nil, e
	}
	out := []ProviderState{}
	for _, name := range providers {
		st := ProviderState{Provider: name, Status: "never"}
		for _, h := range have {
			if h.Provider == name {
				st = h
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// track runs one provider and persists its outcome; a provider error is state, not a failure of the sync.
func (s *Service) track(ctx context.Context, p principal.Principal, provider string, fn func() ([]Quote, error)) ([]Quote, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	id := p.UserID + "|" + provider
	st := ProviderState{ID: id, UserID: p.UserID, Provider: provider}
	if old, e := s.provState(ctx, p, provider); e == nil {
		st = old
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	old := st
	q, fe := fn()
	st.LastAttempt = now
	if fe != nil {
		st.Status, st.Error = "error", fe.Error()
		if len(st.Error) > 300 {
			st.Error = st.Error[:300]
		}
		st.ConsecutiveFailures++
	} else {
		st.Status, st.Error, st.ConsecutiveFailures, st.LastSuccess = "ok", "", 0, now
		for _, x := range q {
			if x.At >= st.NewestAt {
				st.NewestAt, st.NewestValue = x.At, x.Value.String()
			}
		}
	}
	write := func(tx context.Context) error {
		const writeSQL = `INSERT INTO rate_sync (id,user_id,provider,status,last_attempt,last_success,error,consecutive_failures,newest_at,newest_value)
 VALUES (@id,@user,@provider,@status,@attempt,@success,@error,@failures,@at,@value)
 ON CONFLICT (id) DO UPDATE SET user_id=EXCLUDED.user_id, provider=EXCLUDED.provider,status=EXCLUDED.status,
 last_attempt=EXCLUDED.last_attempt,last_success=EXCLUDED.last_success,error=EXCLUDED.error,
 consecutive_failures=EXCLUDED.consecutive_failures,newest_at=EXCLUDED.newest_at,newest_value=EXCLUDED.newest_value`
		_, err := s.repo.db.Exec(tx, writeSQL, pgx.NamedArgs{"id": st.ID, "user": st.UserID, "provider": st.Provider, "status": st.Status, "attempt": st.LastAttempt, "success": st.LastSuccess, "error": st.Error, "failures": st.ConsecutiveFailures, "at": st.NewestAt, "value": st.NewestValue})
		return err
	}
	// shortcut: a rate_sync dashboard source shows attempt times up to a day old, upgrade if one needs them live.
	old.LastAttempt, old.LastSuccess = st.LastAttempt, st.LastSuccess
	if old == st {
		return q, s.audit.Workflow(ctx, write)
	}
	return q, s.audit.Workflow(ctx, func(tx context.Context) error { return s.audit.Mutation(audit.RateOnly(tx), p, write) })
}

// Sync refreshes ECB (when ecb) and gold (when gold: gold-api.com, else Swissquote) for one user.
func (s *Service) Sync(ctx context.Context, p principal.Principal, ecb, gold bool) (SyncResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return SyncResult{}, e
	}
	ctx = audit.WithBatch(ctx, ulid.Make().String())
	out := SyncResult{Written: []Quote{}}
	var firstErr error
	run := func(provider string, fn func() ([]Quote, error)) bool {
		q, e := s.track(ctx, p, provider, fn)
		out.Written = append(out.Written, q...)
		if e != nil && firstErr == nil {
			firstErr = e
		}
		st, _ := s.provState(ctx, p, provider)
		return st.Status == "ok"
	}
	if ecb {
		run("ecb", func() ([]Quote, error) { return s.refreshECB(ctx, p) })
	}
	if gold {
		ok := run("gold-api", func() ([]Quote, error) { return s.refreshGold(ctx, p, s.urls.gold, goldSource, parseGoldAPI) })
		if ok {
			if err := s.skip(ctx, p, "swissquote"); err != nil && firstErr == nil {
				firstErr = err
			}
		} else {
			run("swissquote", func() ([]Quote, error) { return s.refreshGold(ctx, p, s.urls.swiss, swissSource, parseSwissquote) })
		}
	}
	var e error
	if out.Providers, e = s.SyncStatus(ctx, p); e != nil {
		return out, e
	}
	return out, firstErr
}
func (s *Service) provState(ctx context.Context, p principal.Principal, provider string) (ProviderState, error) {
	const stateSQL = `SELECT * FROM rate_sync WHERE id=@id`
	rows, e := s.repo.db.Query(ctx, stateSQL, pgx.NamedArgs{"id": p.UserID + "|" + provider})
	if e != nil {
		return ProviderState{}, e
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByName[ProviderState])
}
func (s *Service) skip(ctx context.Context, p principal.Principal, provider string) error {
	id := p.UserID + "|" + provider
	if st, e := s.provState(ctx, p, provider); e == nil && st.Status == "skipped" {
		return nil
	} else if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		return s.audit.Mutation(audit.RateOnly(tx), p, func(tx context.Context) error {
			const skipSQL = `INSERT INTO rate_sync (id,user_id,provider,status) VALUES (@id,@user,@provider,'skipped')
 ON CONFLICT (id) DO UPDATE SET status='skipped',user_id=EXCLUDED.user_id,provider=EXCLUDED.provider`
			_, err := s.repo.db.Exec(tx, skipSQL, pgx.NamedArgs{"id": id, "user": p.UserID, "provider": provider})
			return err
		})
	})
}

// existing indexes stored quotes by pair@at, whatever their source.
func (s *Service) existing(ctx context.Context, p principal.Principal, from, at string, pairs []string) (map[string]Quote, error) {
	const existingSQL = `SELECT ` + quoteColumns + ` FROM prices WHERE user_id=@user
    AND (@from='' OR at>=NULLIF(@from,'')::timestamptz) AND (@at='' OR at=NULLIF(@at,'')::timestamptz)
    AND (@pairs::text[] IS NULL OR pair=ANY(@pairs::text[]))`
	rows, e := s.repo.db.Query(ctx, existingSQL, pgx.NamedArgs{"user": p.UserID, "from": from, "at": at, "pairs": pairs})
	if e != nil {
		return nil, e
	}
	qs, e := pgx.CollectRows(rows, pgx.RowToStructByName[Quote])
	if e != nil {
		return nil, e
	}
	have := map[string]Quote{}
	for _, q := range qs {
		have[q.Pair+"@"+q.At] = q
	}
	return have, nil
}

// put writes a provider quote unless the slot holds a correction, an archived quote, another source, or the same value.
func (s *Service) put(ctx context.Context, p principal.Principal, have map[string]Quote, in SetInput) (*Quote, error) {
	in.Provider = true
	t, e := time.Parse(time.RFC3339, in.At)
	if e != nil {
		return nil, e
	}
	in.At = t.UTC().Format(time.RFC3339)
	ctx = audit.WithRoutine(ctx)
	if old, ok := have[in.Pair+"@"+in.At]; ok && (old.Corrected || old.Archived || old.Source != in.Source || old.Value.String() == in.Value.String()) {
		return nil, nil
	}
	r, e := s.Set(ctx, p, in)
	return &r.Quote, e
}

func (s *Service) refreshECB(ctx context.Context, p principal.Principal) ([]Quote, error) {
	b, _, e := safefetch.Read(ctx, s.http, s.urls.ecb, maxFeedBytes)
	if e != nil {
		return nil, e
	}
	quotes, e := parseECB(b)
	if e != nil {
		return nil, e
	}
	oldest := quotes[0].At
	for _, q := range quotes {
		oldest = min(oldest, q.At)
	}
	have, e := s.existing(ctx, p, oldest, "", nil)
	if e != nil {
		return nil, e
	}
	out := []Quote{}
	e = s.audit.Workflow(ctx, func(tx context.Context) error {
		out = out[:0]
		for _, q := range quotes {
			v, e := ledger.ParseDecimal(q.Value)
			if e != nil {
				return e
			}
			w, e := s.put(tx, p, have, SetInput{Pair: q.Pair, Value: v, At: q.At, Source: ecbSource})
			if e != nil {
				return e
			}
			if w != nil {
				out = append(out, *w)
			}
		}
		return nil
	})
	return out, e
}

func (s *Service) refreshGold(ctx context.Context, p principal.Principal, url, source string, parse func([]byte) (*big.Rat, time.Time, error)) ([]Quote, error) {
	b, _, e := safefetch.Read(ctx, s.http, url, 64*1024)
	if e != nil {
		return nil, e
	}
	oz, at, e := parse(b)
	if e != nil {
		return nil, e
	}
	gram := new(big.Rat).Quo(oz, mustRat(gramsPerOz))
	ozD, e1 := ledger.ParseDecimal(oz.FloatString(4))
	gramD, e2 := ledger.ParseDecimal(gram.FloatString(6))
	if e1 != nil || e2 != nil {
		return nil, fmt.Errorf("gold price not representable: %v %v", e1, e2)
	}
	ts := at.UTC().Format(time.RFC3339)
	have, e := s.existing(ctx, p, "", ts, []string{"XAU/USD", "XAU_g/USD"})
	if e != nil {
		return nil, e
	}
	out := []Quote{}
	e = s.audit.Workflow(ctx, func(tx context.Context) error {
		out = out[:0]
		for _, in := range []SetInput{{Pair: "XAU/USD", Value: ozD}, {Pair: "XAU_g/USD", Value: gramD}} {
			in.At, in.Source = ts, source
			w, e := s.put(tx, p, have, in)
			if e != nil {
				return e
			}
			if w != nil {
				out = append(out, *w)
			}
		}
		return nil
	})
	return out, e
}
func mustRat(v string) *big.Rat { r, _ := new(big.Rat).SetString(v); return r }

// parseGoldAPI reads {"price":..., "updatedAt":RFC3339}.
func parseGoldAPI(b []byte) (*big.Rat, time.Time, error) {
	var d struct {
		Price     json.Number `json:"price"`
		UpdatedAt string      `json:"updatedAt"`
	}
	if e := json.Unmarshal(b, &d); e != nil {
		return nil, time.Time{}, fmt.Errorf("parse gold-api quote: %w", e)
	}
	price, ok := new(big.Rat).SetString(d.Price.String())
	at, e := time.Parse(time.RFC3339, d.UpdatedAt)
	if !ok || price.Sign() <= 0 || e != nil {
		return nil, time.Time{}, fmt.Errorf("gold-api quote needs a positive price and RFC3339 updatedAt")
	}
	return price, at, nil
}

// parseSwissquote takes the mid of the first spread profile; ts (ms) is optional and defaults to now.
func parseSwissquote(b []byte) (*big.Rat, time.Time, error) {
	var d []struct {
		Profiles []struct {
			Bid json.Number `json:"bid"`
			Ask json.Number `json:"ask"`
		} `json:"spreadProfilePrices"`
		TS int64 `json:"ts"`
	}
	if e := json.Unmarshal(b, &d); e != nil {
		return nil, time.Time{}, fmt.Errorf("parse Swissquote quote: %w", e)
	}
	if len(d) == 0 || len(d[0].Profiles) == 0 {
		return nil, time.Time{}, fmt.Errorf("Swissquote returned no prices")
	}
	bid, ok1 := new(big.Rat).SetString(d[0].Profiles[0].Bid.String())
	ask, ok2 := new(big.Rat).SetString(d[0].Profiles[0].Ask.String())
	if !ok1 || !ok2 || bid.Sign() <= 0 || ask.Sign() <= 0 {
		return nil, time.Time{}, fmt.Errorf("Swissquote bid and ask must be positive")
	}
	mid := new(big.Rat).Quo(new(big.Rat).Add(bid, ask), big.NewRat(2, 1))
	at := time.Now()
	if d[0].TS > 0 {
		at = time.UnixMilli(d[0].TS)
	}
	return mid, at, nil
}

// Stale lists reasons the stored rates look out of date; no quotes at all is not stale (nothing is tracked yet).
// ECB publishes on weekdays only, so its age counts weekdays: a weekend or a two-day holiday does not trip it.
func (s *Service) Stale(ctx context.Context, p principal.Principal, now time.Time) ([]string, error) {
	st, e := s.SyncStatus(ctx, p)
	if e != nil {
		return nil, e
	}
	out := []string{}
	for _, x := range st {
		if x.ConsecutiveFailures >= 3 {
			out = append(out, fmt.Sprintf("%s failed %d times in a row: %s", x.Provider, x.ConsecutiveFailures, x.Error))
		}
	}
	at := now.UTC().Format(time.RFC3339)
	if q, e := s.quote(ctx, p, "EUR", "USD", at); e == nil {
		t, _ := time.Parse(time.RFC3339, q.At)
		if n := weekdaysSince(t, now); n > 4 {
			out = append(out, fmt.Sprintf("EUR/USD newest quote %s is %d weekdays old", q.At, n))
		}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if q, e := s.quote(ctx, p, "XAU", "USD", at); e == nil {
		t, _ := time.Parse(time.RFC3339, q.At)
		if now.Sub(t) > 72*time.Hour {
			out = append(out, fmt.Sprintf("XAU/USD newest quote %s is older than 3 days", q.At))
		}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	return out, nil
}
func weekdaysSince(from, to time.Time) int {
	n := 0
	for d := from.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour); !d.After(to.UTC()); d = d.Add(24 * time.Hour) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			n++
		}
	}
	return n
}
