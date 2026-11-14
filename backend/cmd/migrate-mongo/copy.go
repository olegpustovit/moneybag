package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (m migration) checkCollections(ctx context.Context) error {
	names, err := m.source.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, table := range mappings {
		known[table.name] = true
	}
	for _, name := range names {
		if name == "system.profile" {
			if m.profileArchive == "" {
				return fmt.Errorf("system.profile requires --profile-archive; it must not be silently skipped")
			}
			continue
		}
		if !known[name] {
			return fmt.Errorf("unmapped source collection %q; no collections may be silently skipped", name)
		}
	}
	return nil
}
func appTables() []string {
	names := make([]string, 0, len(mappings)+1)
	for _, m := range mappings {
		names = append(names, pgx.Identifier{m.name}.Sanitize())
	}
	return append(names, pgx.Identifier{"row_keys"}.Sanitize())
}
func (m migration) checkEmpty(ctx context.Context) error {
	for _, name := range appTables() {
		var anyRows bool
		if err := m.target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+name+` LIMIT 1)`).Scan(&anyRows); err != nil {
			return err
		}
		if anyRows {
			return fmt.Errorf("target application tables must be empty; use --truncate to replace them explicitly")
		}
	}
	return nil
}
func (m migration) sourceRows(ctx context.Context, table mapping, fn func(bson.Raw, []any) error) error {
	cursor, err := m.source.Collection(table.name).Find(ctx, bson.M{}, options.Find().SetBatchSize(1000))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	known := map[string]bool{}
	for _, name := range table.sourceKeys {
		known[name] = true
	}
	ordinal := 0
	for cursor.Next(ctx) {
		ordinal++
		elements, err := cursor.Current.Elements()
		if err != nil {
			return err
		}
		for _, field := range elements {
			if !known[field.Key()] {
				return fmt.Errorf("%s document %d has unmapped field %q", table.name, ordinal, field.Key())
			}
		}
		row, err := decodeRecord(table.name, cursor.Current)
		if err != nil {
			return fmt.Errorf("%s document %d cannot decode: %w", table.name, ordinal, err)
		}
		if len(row) != len(table.columns) {
			return fmt.Errorf("%s has an incomplete column mapping", table.name)
		}
		row, err = prepareRow(table, row)
		if err != nil {
			return fmt.Errorf("%s document %d cannot convert: %w", table.name, ordinal, err)
		}
		if err = fn(cursor.Current, row); err != nil {
			return err
		}
	}
	return cursor.Err()
}
func (m migration) copy(ctx context.Context, truncate bool) error {
	if err := m.checkCollections(ctx); err != nil {
		return err
	}
	if err := m.checkCollation(ctx); err != nil {
		return err
	}
	if err := m.archiveProfile(ctx); err != nil {
		return err
	}
	// Validate all records and unique keys before an explicitly requested replacement.
	for _, table := range mappings {
		if err := m.sourceRows(ctx, table, func(bson.Raw, []any) error { return nil }); err != nil {
			return err
		}
	}
	if err := m.checkDuplicates(ctx); err != nil {
		return err
	}
	if !truncate {
		if err := m.checkEmpty(ctx); err != nil {
			return err
		}
	} else {
		if _, err := m.target.Exec(ctx, "TRUNCATE "+strings.Join(appTables(), ",")+" RESTART IDENTITY CASCADE"); err != nil {
			return err
		}
	}
	for _, table := range mappings {
		batch := make([][]any, 0, 1000)
		var count int64
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			n, err := m.copyBatch(ctx, table, batch)
			if err != nil {
				return fmt.Errorf("copy %s stopped after %d confirmed rows: %w", table.name, count, err)
			}
			count += n
			batch = batch[:0]
			return nil
		}
		err := m.sourceRows(ctx, table, func(_ bson.Raw, row []any) error {
			batch = append(batch, row)
			if len(batch) == 1000 {
				return flush()
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err = flush(); err != nil {
			return err
		}
		fmt.Fprintf(m.out, "copied %s %d\n", table.name, count)
	}
	if err := m.checkCounters(ctx); err != nil {
		return err
	}
	fmt.Fprintln(m.out, "copy complete; verify before starting the application")
	return nil
}
func columnIndex(table mapping, name string) int {
	for i, column := range table.columns {
		if column == name {
			return i
		}
	}
	panic("missing migration column " + table.name + "." + name)
}
func (m migration) copyBatch(ctx context.Context, table mapping, rows [][]any) (int64, error) {
	if table.name != "rows" {
		return m.target.CopyFrom(ctx, pgx.Identifier{table.name}, table.columns, pgx.CopyFromRows(rows))
	}
	var copied int64
	err := pgx.BeginFunc(ctx, m.target, func(tx pgx.Tx) error {
		var err error
		copied, err = tx.CopyFrom(ctx, pgx.Identifier{table.name}, table.columns, pgx.CopyFromRows(rows))
		if err != nil {
			return err
		}
		keys := [][]any{}
		for _, row := range rows {
			hashes, _ := row[columnIndex(table, "key_hashes")].([]string)
			for _, hash := range hashes {
				keys = append(keys, []any{row[columnIndex(table, "user_id")], row[columnIndex(table, "table_id")], hash, row[columnIndex(table, "id")]})
			}
		}
		if len(keys) > 0 {
			_, err = tx.CopyFrom(ctx, pgx.Identifier{"row_keys"}, []string{"user_id", "table_id", "key_hash", "row_id"}, pgx.CopyFromRows(keys))
		}
		return err
	})
	return copied, err
}
func (m migration) checkCounters(ctx context.Context) error {
	for _, table := range []struct{ name, key string }{{"journal", "journal"}, {"events", "audit"}} {
		rows, err := m.target.Query(ctx, `SELECT j.user_id,max(j.seq),COALESCE(c.n,0) FROM `+pgx.Identifier{table.name}.Sanitize()+` j LEFT JOIN counters c ON c.id=j.user_id||@suffix GROUP BY j.user_id,c.n`, pgx.NamedArgs{"suffix": ":" + table.key})
		if err != nil {
			return err
		}
		for rows.Next() {
			var owner string
			var maximum, counter int64
			if err = rows.Scan(&owner, &maximum, &counter); err != nil {
				rows.Close()
				return err
			}
			if maximum > counter {
				rows.Close()
				return fmt.Errorf("%s maximum sequence exceeds its owner counter", table.name)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (m migration) checkCollation(ctx context.Context) error {
	var collate, ctype string
	if err := m.target.QueryRow(ctx, `SELECT datcollate,datctype FROM pg_database WHERE datname=current_database()`).Scan(&collate, &ctype); err != nil {
		return err
	}
	if collate != "C" || ctype != "C" {
		return fmt.Errorf("target database must use LC_COLLATE=C and LC_CTYPE=C")
	}
	return nil
}
