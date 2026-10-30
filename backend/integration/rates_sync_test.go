package integration

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestRateSyncStateAndCorrectionsSurvive(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, e := url.Parse(uri)
	if e != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("integration tests require the dedicated moneybag_test_ database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, e := mongodb.Connect(ctx, uri)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Disconnect(context.Background())
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_ratesync")
	_ = db.Drop(ctx)
	defer db.Drop(context.Background())
	ids := identity.NewService(identity.NewMongoRepository(db))
	if e = ids.Init(ctx); e != nil {
		t.Fatal(e)
	}
	u, e := ids.Setup(ctx, "rates@example.test", "Rates", "synthetic-test-password")
	if e != nil {
		t.Fatal(e)
	}
	p := identity.Principal(u)
	a := audit.NewService(audit.NewMongoRepository(db))
	prices := rates.NewService(rates.NewMongoRepository(db), a)

	var goldDown atomic.Bool
	goldDown.Store(true)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ecb":
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Envelope xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref"><Cube><Cube time="2026-10-28"><Cube currency="USD" rate="1.1133"/><Cube currency="GBP" rate="0.83785"/></Cube></Cube></Envelope>`))
		case "/gold":
			if goldDown.Load() {
				http.Error(w, "down", 500)
				return
			}
			_, _ = w.Write([]byte(`{"price":3110.35,"updatedAt":"2026-10-29T10:00:00Z"}`))
		case "/swiss":
			_, _ = w.Write([]byte(`[{"spreadProfilePrices":[{"bid":3110.00,"ask":3110.70}],"ts":1761732000000}]`))
		}
	}))
	defer srv.Close()
	c := srv.Client()
	c.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	prices.SetHTTPClient(c)
	prices.SetEndpoints("https://example.com/ecb", "https://example.com/gold", "https://example.com/swiss")
	state := func(name string) rates.ProviderState {
		st, e := prices.SyncStatus(ctx, p)
		if e != nil {
			t.Fatal(e)
		}
		for _, x := range st {
			if x.Provider == name {
				return x
			}
		}
		t.Fatal("no provider", name)
		return rates.ProviderState{}
	}

	res, e := prices.Sync(ctx, p, true, true)
	if e != nil || len(res.Written) != 4 {
		t.Fatal("first sync", res.Written, e)
	}
	if g := state("gold-api"); g.Status != "error" || g.ConsecutiveFailures != 1 || g.Error == "" {
		t.Fatal("failure not recorded", g)
	}
	synced, e := a.List(ctx, p, audit.ListInput{Tool: "rate_set"})
	if e != nil || len(synced.Events) != 4 {
		t.Fatal("sync events", synced, e)
	}
	for _, event := range synced.Events {
		if !event.Routine || event.BatchID == "" || event.BatchID != synced.Events[0].BatchID || !strings.HasPrefix(event.Summary, "Updated the ") {
			t.Fatalf("a rate sync must be routine and batched: %+v", event)
		}
	}
	feed, e := a.Changes(ctx, p, audit.ChangesInput{}, func(principal.Actor) string { return "System" })
	if e != nil || len(feed.Groups) != 0 {
		t.Fatalf("routine writes must stay out of the default feed: %+v %v", feed, e)
	}
	if feed, e = a.Changes(ctx, p, audit.ChangesInput{Routine: true}, func(principal.Actor) string { return "System" }); e != nil || len(feed.Groups) != 1 || feed.Groups[0].Summary != "Updated 4 rate quotes" {
		t.Fatalf("routine writes must collapse into one line: %+v %v", feed, e)
	}
	if s := state("swissquote"); s.Status != "ok" || s.NewestValue != "100.000000" {
		t.Fatal("fallback state", s)
	}
	got, e := prices.Get(ctx, p, []string{"XAU/USD", "XAU_g/USD"}, "2026-12-01T00:00:00Z")
	if e != nil || got[0].Source != "Swissquote spot" || got[0].Value.String() != "3110.3500" || got[1].Value.String() != "100.000000" {
		t.Fatal("swissquote fallback quotes", got, e)
	}

	// A correction under the provider's own source, and an archived quote, must survive the next sync.
	at := "2026-10-28T16:00:00Z"
	if _, e = prices.Set(ctx, p, rates.SetInput{Pair: "EUR/USD", Value: ledger.MustDecimal("9.9999"), At: at, Source: "ECB daily reference", Reason: "Bad readout"}); e != nil {
		t.Fatal(e)
	}
	gbp, e := prices.Get(ctx, p, []string{"EUR/GBP"}, at)
	if e != nil {
		t.Fatal(e)
	}
	if e = prices.Archive(ctx, p, gbp[0].ID, true, "Bad quote"); e != nil {
		t.Fatal(e)
	}
	goldDown.Store(false)
	res, e = prices.Sync(ctx, p, true, true)
	if e != nil {
		t.Fatal(e)
	}
	for _, q := range res.Written {
		if q.Pair == "EUR/USD" || q.Pair == "EUR/GBP" {
			t.Fatal("sync rewrote a protected quote", q)
		}
	}
	if q, _ := prices.Get(ctx, p, []string{"EUR/USD"}, at); q[0].Value.String() != "9.9999" || !q[0].Corrected {
		t.Fatal("correction overwritten", q)
	}
	if _, e = prices.Get(ctx, p, []string{"EUR/GBP"}, at); e == nil {
		t.Fatal("archived quote resurrected")
	}
	if g := state("gold-api"); g.Status != "ok" || g.ConsecutiveFailures != 0 || g.Error != "" || g.NewestValue != "100.000000" {
		t.Fatal("success did not reset state", g)
	}
	if s := state("swissquote"); s.Status != "skipped" {
		t.Fatal("fallback not skipped", s)
	}
	revisions := audit.NewService(audit.NewMongoRepository(db))
	before, e := revisions.Revision(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	res, e = prices.Sync(ctx, p, true, false)
	if e != nil || len(res.Written) != 0 {
		t.Fatal("unchanged provider sync", res.Written, e)
	}
	after, e := revisions.Revision(ctx, p)
	if e != nil || after != before {
		t.Fatal("no-op rate sync moved the revision", before, after, e)
	}
	// The request quote cache answers "at or before" exactly like the indexed query, and skips archived quotes.
	cached := rates.WithQuoteCache(ctx)
	for _, when := range []string{"2026-10-27T00:00:00Z", at, "2026-12-01T00:00:00Z"} {
		for _, pair := range []string{"EUR/USD", "EUR/GBP", "XAU_g/USD"} {
			want, we := prices.Get(ctx, p, []string{pair}, when)
			got, ge := prices.Get(cached, p, []string{pair}, when)
			if (we == nil) != (ge == nil) || we == nil && (want[0].ID != got[0].ID || want[0].Value.String() != got[0].Value.String()) {
				t.Fatal("cached quote differs", pair, when, want, we, got, ge)
			}
		}
	}
	stale, e := prices.Stale(ctx, p, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	if e != nil || len(stale) != 2 {
		t.Fatal("stale rates not reported", stale, e)
	}
	fresh, e := prices.Stale(ctx, p, time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	if e != nil || len(fresh) != 0 {
		t.Fatal("fresh rates reported stale", fresh, e)
	}
	future := time.Now().UTC().Add(2 * time.Second).Truncate(time.Second)
	if _, e = prices.Set(ctx, p, rates.SetInput{Pair: "EUR/USD", Value: ledger.MustDecimal("2"), At: future.Format(time.RFC3339), Source: "Synthetic future quote"}); e != nil {
		t.Fatal(e)
	}
	tracked := expressions.WithVolatility(ctx)
	if _, e = prices.Get(tracked, p, []string{"EUR/USD"}, ""); e != nil || !expressions.Expiry(tracked).Equal(future) {
		t.Fatalf("future quote expiry: %v, %v", expressions.Expiry(tracked), e)
	}
	cache, e := dashboard.NewSnapshots(revisions)
	if e != nil {
		t.Fatal(e)
	}
	sources := dashboard.Sources{Rates: prices, Snapshots: cache}
	beforeRate, e := sources.Rows(ctx, p, "rates:EUR/USD")
	if e != nil || beforeRate[0]["value"] != 9.9999 {
		t.Fatalf("rate before scheduled quote: %v, %v", beforeRate, e)
	}
	timer := time.NewTimer(time.Until(future) + 100*time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-timer.C:
	}
	afterRate, e := sources.Rows(ctx, p, "rates:EUR/USD")
	if e != nil || afterRate[0]["value"] != float64(2) {
		t.Fatalf("rate after scheduled quote: %v, %v", afterRate, e)
	}
}
