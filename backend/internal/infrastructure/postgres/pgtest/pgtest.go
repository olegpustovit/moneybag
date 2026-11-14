// Package pgtest gives each integration test an isolated, migrated PostgreSQL database.
package pgtest

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
)

var template struct {
	sync.Once
	name string
	err  error
}

func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("MONEYBAG_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set MONEYBAG_TEST_DATABASE_URL to a dedicated PostgreSQL test server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := postgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	template.Do(func() {
		// shortcut: one template per test process remains on the disposable server until its container is removed.
		template.name = "moneybag_template_" + ulid.Make().String()
		_, template.err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{template.name}.Sanitize()+" TEMPLATE template0 LC_COLLATE 'C' LC_CTYPE 'C'")
		if template.err != nil {
			return
		}
		config := admin.Config().Copy()
		config.ConnConfig.Database = template.name
		var pool *pgxpool.Pool
		pool, template.err = pgxpool.NewWithConfig(ctx, config)
		if template.err == nil {
			template.err = postgres.Migrate(ctx, pool)
			pool.Close()
		}
	})
	if template.err != nil {
		t.Fatal(template.err)
	}
	name := "test_" + ulid.Make().String()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template.name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		admin, err := postgres.Open(ctx, url)
		if err != nil {
			t.Error(err)
			return
		}
		defer admin.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	config := admin.Config().Copy()
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
