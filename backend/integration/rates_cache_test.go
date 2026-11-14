package integration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestQuoteCacheLoadsRequestedPairsOnce(t *testing.T) {
	db := pgtest.New(t)
	ctx := context.Background()
	var reads atomic.Int64
	config := db.Config().Copy()
	config.ConnConfig.Tracer = sqlQueryCounter{reads: &reads}
	traced, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	other := p
	other.UserID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	docs := []rates.Quote{
		rates.Quote{ID: "a", UserID: p.UserID, Pair: "EUR/USD", At: "2026-01-01T00:00:00Z", Value: ledger.MustDecimal("1")},
		rates.Quote{ID: "b", UserID: p.UserID, Pair: "EUR/USD", At: "2026-02-01T00:00:00Z", Value: ledger.MustDecimal("2")},
		rates.Quote{ID: "c", UserID: p.UserID, Pair: "EUR/USD", At: "2026-03-01T00:00:00Z", Value: ledger.MustDecimal("3"), Archived: true},
		rates.Quote{ID: "d", UserID: p.UserID, Pair: "GBP/EUR", At: "2026-01-01T00:00:00Z", Value: ledger.MustDecimal("2")},
		rates.Quote{ID: "e", UserID: other.UserID, Pair: "EUR/USD", At: "2026-01-01T00:00:00Z", Value: ledger.MustDecimal("9")},
	}
	for i := 0; i < 30; i++ {
		docs = append(docs, rates.Quote{ID: fmt.Sprintf("extra-%d", i), UserID: p.UserID, Pair: fmt.Sprintf("C%02d/USD", i), At: "2026-01-01T00:00:00Z", Value: ledger.MustDecimal("4")})
	}
	for _, q := range docs {
		_, err = db.Exec(ctx, `INSERT INTO prices (id,user_id,pair,at,value,archived) VALUES (@id,@user,@pair,@at::timestamptz,@value,@archived)`, pgx.NamedArgs{"id": q.ID, "user": q.UserID, "pair": q.Pair, "at": q.At, "value": q.Value, "archived": q.Archived})
		if err != nil {
			t.Fatal(err)
		}
	}
	service := rates.NewService(rates.NewPostgresRepository(traced), nil)
	cached := rates.WithQuoteCache(ctx)
	reads.Store(0)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, e := service.Get(cached, p, []string{"EUR/USD"}, "2026-12-01T00:00:00Z")
			if e != nil {
				errs <- e
			} else if len(q) != 1 || q[0].ID != "b" {
				errs <- fmt.Errorf("wrong quote: %v", q)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("same pair used %d queries", n)
	}
	q, err := service.Get(cached, p, []string{"EUR/USD"}, "2026-01-15T00:00:00Z")
	if err != nil || q[0].ID != "a" || reads.Load() != 1 {
		t.Fatalf("historical: %v %v %d", q, err, reads.Load())
	}
	inverse, err := service.Get(cached, p, []string{"EUR/GBP"}, "2026-12-01T00:00:00Z")
	if err != nil || inverse[0].Value.String() != "0.5" || reads.Load() != 3 {
		t.Fatalf("inverse: %v %v %d", inverse, err, reads.Load())
	}
	theirs, err := service.Get(cached, other, []string{"EUR/USD"}, "2026-12-01T00:00:00Z")
	if err != nil || theirs[0].ID != "e" || reads.Load() != 4 {
		t.Fatalf("user isolation: %v %v %d", theirs, err, reads.Load())
	}
	_, err = service.Get(cached, p, []string{"EUR/GBP"}, "2025-01-01T00:00:00Z")
	if err == nil || reads.Load() != 4 {
		t.Fatalf("missing historical quote: %v %d", err, reads.Load())
	}
}

type sqlQueryCounter struct{ reads, writes *atomic.Int64 }

func (c sqlQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	fields := strings.Fields(data.SQL)
	if len(fields) > 0 {
		switch strings.ToUpper(fields[0]) {
		case "SELECT", "WITH", "EXPLAIN":
			if c.reads != nil {
				c.reads.Add(1)
			}
		case "INSERT", "UPDATE", "DELETE":
			if c.writes != nil {
				c.writes.Add(1)
			}
		}
	}
	return ctx
}
func (c sqlQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
