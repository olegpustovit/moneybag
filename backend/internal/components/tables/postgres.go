package tables

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func rowFrom(rows pgx.CollectableRow, table Table) (Row, error) {
	stored, err := pgx.RowToStructByName[struct {
		Row
		DecimalCells []string `db:"decimal_cells"`
	}](rows)
	if err != nil {
		return Row{}, err
	}
	for _, id := range stored.DecimalCells {
		value, err := decimalCell(stored.Cells[id])
		if err != nil {
			return Row{}, err
		}
		stored.Cells[id] = value
	}
	if err = normalizeCells(table.Columns, stored.Cells); err != nil {
		return Row{}, err
	}
	return stored.Row, nil
}

func (r *PostgresRepository) table(ctx context.Context, user, id string, includeArchived bool) (Table, error) {
	rows, err := r.db.Query(ctx, `SELECT * FROM tables WHERE user_id=@user AND id=@id AND (@archived OR NOT archived)`, pgx.NamedArgs{"user": user, "id": id, "archived": includeArchived})
	if err != nil {
		return Table{}, err
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByName[Table])
}

func (r *PostgresRepository) row(ctx context.Context, user string, table Table, id string) (Row, error) {
	rows, err := r.db.Query(ctx, `SELECT * FROM rows WHERE user_id=@user AND table_id=@table AND id=@id`, pgx.NamedArgs{"user": user, "table": table.ID, "id": id})
	if err != nil {
		return Row{}, err
	}
	return pgx.CollectOneRow(rows, func(row pgx.CollectableRow) (Row, error) { return rowFrom(row, table) })
}

func (r *PostgresRepository) insertTable(ctx context.Context, t Table) (pgconn.CommandTag, error) {
	return r.db.Exec(ctx, `INSERT INTO tables(storage_key,user_id,id,name,columns,keys,checks,ledger,description,archived) VALUES (@key,@user,@id,@name,@columns,@keys,@checks,@ledger,@description,@archived)`, pgx.NamedArgs{"key": t.Key, "user": t.UserID, "id": t.ID, "name": t.Name, "columns": t.Columns, "keys": t.Keys, "checks": t.Checks, "ledger": t.Ledger, "description": t.Description, "archived": t.Archived})
}
