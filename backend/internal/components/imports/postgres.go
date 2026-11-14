package imports

import (
	"context"
	"github.com/jackc/pgx/v5"
)

func (r *PostgresRepository) batch(ctx context.Context, user, id string) (Batch, error) {
	rows, err := r.db.Query(ctx, `SELECT * FROM import_batches WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"id": id, "user": user})
	if err != nil {
		return Batch{}, err
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByName[Batch])
}

func (r *PostgresRepository) feed(ctx context.Context, user, id string) (Feed, error) {
	rows, err := r.db.Query(ctx, `SELECT * FROM feeds WHERE storage_key=@key AND user_id=@user`, pgx.NamedArgs{"key": user + ":" + id, "user": user})
	if err != nil {
		return Feed{}, err
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByName[Feed])
}
