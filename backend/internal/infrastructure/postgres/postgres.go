package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		// Parse errors can contain the URL and its password.
		return nil, errors.New("open PostgreSQL: invalid database URL")
	}
	config.MaxConns = 5
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("migration files: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate PostgreSQL: %w", err)
	}
	return nil
}

type Querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

const nextSQL = `INSERT INTO counters (id, user_id, name, n) VALUES (@id, @owner, @name, 1)
ON CONFLICT (id) DO UPDATE SET n = counters.n + 1 RETURNING n`

func Next(ctx context.Context, db Querier, name string) (int64, error) {
	owner, key, ok := strings.Cut(name, ":")
	if !ok || owner == "" || key == "" {
		return 0, fmt.Errorf("counter requires owner and name")
	}
	var n int64
	err := db.QueryRow(ctx, nextSQL, pgx.NamedArgs{"id": name, "owner": owner, "name": key}).Scan(&n)
	return n, err
}

func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func IsUniqueViolation(err error) bool {
	var pgerr *pgconn.PgError
	return errors.As(err, &pgerr) && pgerr.Code == "23505"
}

type workflowKey struct{}

var workflowLock = make(chan struct{}, 1)

// InWorkflow reports whether ctx is inside a serialized write workflow.
func InWorkflow(ctx context.Context) bool { return ctx.Value(workflowKey{}) != nil }

// shortcut: standalone writes are serialized in one API process; coordinate writers before adding API replicas.
func Workflow(ctx context.Context, fn func(context.Context) error) error {
	if ctx.Value(workflowKey{}) != nil {
		return fn(ctx)
	}
	select {
	case workflowLock <- struct{}{}:
		defer func() { <-workflowLock }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return fn(context.WithValue(ctx, workflowKey{}, true))
}
