package integration

import (
	"context"
	"encoding/json"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
)

// Raw fixtures deliberately bypass service validation to exercise read boundaries.
func insertJournal(ctx context.Context, db *pgxpool.Pool, entries ...ledger.Entry) (int64, error) {
	rows := make([][]any, 0, len(entries))
	for _, e := range entries {
		postings, err := json.Marshal(e.Postings)
		if err != nil {
			return 0, err
		}
		links, err := json.Marshal(e.Links)
		if err != nil {
			return 0, err
		}
		actor, err := json.Marshal(e.Actor)
		if err != nil {
			return 0, err
		}
		rows = append(rows, []any{e.UserID, e.ID, e.Seq, e.Date, e.Kind, e.Payee, postings, e.Tags, links, e.Status, actor, e.CreatedAt})
	}
	return db.CopyFrom(ctx, pgx.Identifier{"journal"}, []string{"user_id", "id", "seq", "date", "kind", "payee", "postings", "tags", "links", "status", "actor", "created_at"}, pgx.CopyFromRows(rows))
}

func insertAccount(ctx context.Context, db *pgxpool.Pool, a ledger.Account) (int64, error) {
	groups, err := json.Marshal(a.Groups)
	if err != nil {
		return 0, err
	}
	tag, err := db.Exec(ctx, `INSERT INTO accounts (storage_key,user_id,id,type,name,pot,commodities,location,class,groups,closed_at,display_name,note,estimated) VALUES (@key,@user,@id,@type,@name,@pot,@commodities,@location,@class,@groups,@closed,@display,@note,@estimated)`, pgx.NamedArgs{"key": a.Key, "user": a.UserID, "id": a.ID, "type": a.Type, "name": a.Name, "pot": a.Pot, "commodities": a.Commodities, "location": a.Location, "class": a.Class, "groups": groups, "closed": a.ClosedAt, "display": a.DisplayName, "note": a.Note, "estimated": a.Estimated})
	return tag.RowsAffected(), err
}

func insertTags(ctx context.Context, db *pgxpool.Pool, user, id string, tags []string) (int64, error) {
	tag, err := db.Exec(ctx, `INSERT INTO entry_tags (id,user_id,entry_id,tags) VALUES (@key,@user,@id,@tags)`, pgx.NamedArgs{"key": user + ":" + id, "user": user, "id": id, "tags": tags})
	return tag.RowsAffected(), err
}

type queryObserver func(string)

func (o queryObserver) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	o(data.SQL)
	return ctx
}
func (o queryObserver) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func tracedPool(t testing.TB, tracer pgx.QueryTracer) *pgxpool.Pool {
	t.Helper()
	config := pgtest.New(t).Config().Copy()
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func insertAssertions(ctx context.Context, db *pgxpool.Pool, assertions ...reconcile.Assertion) (int64, error) {
	rows := make([][]any, 0, len(assertions))
	for _, a := range assertions {
		amount, err := json.Marshal(a.Amount)
		if err != nil {
			return 0, err
		}
		rows = append(rows, []any{a.ID, a.UserID, a.Account, a.Date, amount, a.Source, a.OK, a.Diff.String()})
	}
	return db.CopyFrom(ctx, pgx.Identifier{"balance_assertions"}, []string{"id", "user_id", "account", "date", "amount", "source", "ok", "diff"}, pgx.CopyFromRows(rows))
}
