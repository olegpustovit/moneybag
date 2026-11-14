package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
)

func TestNext(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	for _, name := range []string{"", "alice", ":journal", "alice:"} {
		if _, err := postgres.Next(ctx, pool, name); err == nil || err.Error() != "counter requires owner and name" {
			t.Fatalf("Next(%q): %v", name, err)
		}
	}
	for _, name := range []string{"alice:journal", "bob:journal"} {
		for want := int64(1); want <= 2; want++ {
			if got, err := postgres.Next(ctx, pool, name); err != nil || got != want {
				t.Fatalf("Next(%q) = %d, %v; want %d", name, got, err, want)
			}
		}
	}
	const count = 30
	var wg sync.WaitGroup
	values := make(chan int64, count)
	for range count {
		wg.Go(func() {
			got, err := postgres.Next(ctx, pool, "concurrent:journal")
			if err != nil {
				t.Error(err)
			}
			values <- got
		})
	}
	wg.Wait()
	close(values)
	seen := map[int64]bool{}
	for n := range values {
		if n < 1 || n > count || seen[n] {
			t.Fatalf("non-contiguous or duplicate counter %d", n)
		}
		seen[n] = true
	}
}

func TestConcurrentMigrate(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "DROP TABLE counters, goose_db_version"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := postgres.Migrate(ctx, pool); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=1 AND is_applied").Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration count = %d: %v", count, err)
	}
}

func TestErrorHelpers(t *testing.T) {
	if !postgres.IsNotFound(fmt.Errorf("wrapped: %w", pgx.ErrNoRows)) || postgres.IsNotFound(errors.New("other")) {
		t.Fatal("not-found classification")
	}
	if !postgres.IsUniqueViolation(fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23505"})) || postgres.IsUniqueViolation(&pgconn.PgError{Code: "23503"}) {
		t.Fatal("unique classification")
	}
	_, err := postgres.Open(context.Background(), "postgres://secret:password@localhost:invalid/db")
	if err == nil || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("invalid URL error leaked credentials: %v", err)
	}
}

func TestWorkflow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Hold the lock so cancellation cannot race with acquisition.
	if err := postgres.Workflow(context.Background(), func(outer context.Context) error {
		if !postgres.InWorkflow(outer) {
			t.Fatal("missing workflow marker")
		}
		if err := postgres.Workflow(outer, func(inner context.Context) error {
			if inner != outer {
				t.Fatal("nested workflow changed context")
			}
			return nil
		}); err != nil {
			return err
		}
		return postgres.Workflow(ctx, func(context.Context) error { t.Fatal("canceled workflow ran"); return nil })
	}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
