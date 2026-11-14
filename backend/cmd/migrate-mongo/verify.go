package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func projection(table mapping) string {
	columns := make([]string, len(table.columns))
	for i, name := range table.columns {
		column := pgx.Identifier{name}.Sanitize()
		switch columnKinds[table.name][name] {
		case kindNumeric:
			column += "::text"
		case kindBytes:
			column = "encode(" + column + ",'hex')"
		case kindTime:
			column = `to_char(` + column + ` AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')`
		}
		columns[i] = column
	}
	return "SELECT jsonb_build_array(" + strings.Join(columns, ",") + ") FROM " + pgx.Identifier{table.name}.Sanitize()
}
func (m migration) verify(ctx context.Context) error {
	if err := m.checkCollections(ctx); err != nil {
		return err
	}
	if err := m.checkCollation(ctx); err != nil {
		return err
	}
	if err := m.verifyProfile(ctx); err != nil {
		return err
	}
	if err := m.checkDuplicates(ctx); err != nil {
		return err
	}
	mismatches := 0
	var expectedKeys int64
	for _, table := range mappings {
		expected := map[[32]byte]int{}
		ids := map[[32]byte]string{}
		var sourceCount int64
		err := m.sourceRows(ctx, table, func(raw bson.Raw, row []any) error {
			canonical, err := canonicalRow(table, row)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(canonical)
			expected[hash]++
			// Fingerprints identify source documents without printing private label/session keys.
			id := sha256.Sum256(raw.Lookup("_id").Value)
			ids[hash] = fmt.Sprintf("%x", id[:8])
			sourceCount++
			if table.name == "rows" {
				hashes, _ := row[columnIndex(table, "key_hashes")].([]string)
				expectedKeys += int64(len(hashes))
			}
			return nil
		})
		if err != nil {
			return err
		}
		rows, err := m.target.Query(ctx, projection(table))
		if err != nil {
			return err
		}
		var targetCount int64
		extra := 0
		for rows.Next() {
			var encoded []byte
			if err = rows.Scan(&encoded); err != nil {
				rows.Close()
				return err
			}
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.UseNumber()
			var row []any
			if err = decoder.Decode(&row); err != nil {
				rows.Close()
				return err
			}
			canonical, err := canonicalRow(table, row)
			if err != nil {
				rows.Close()
				return err
			}
			hash := sha256.Sum256(canonical)
			if expected[hash] > 0 {
				expected[hash]--
			} else {
				extra++
			}
			targetCount++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		missing := 0
		shown := 0
		for hash, count := range expected {
			if count > 0 {
				missing += count
				if shown < 20 {
					fmt.Fprintf(m.out, "mismatch %s source-id-sha256:%s\n", table.name, ids[hash])
					shown++
				}
			}
		}
		if missing > 0 || extra > 0 || sourceCount != targetCount {
			mismatches++
			fmt.Fprintf(m.out, "FAIL %s source=%d target=%d missing=%d extra=%d\n", table.name, sourceCount, targetCount, missing, extra)
		} else {
			fmt.Fprintf(m.out, "verified %s %d\n", table.name, sourceCount)
		}
	}
	var actualKeys int64
	if err := m.target.QueryRow(ctx, `SELECT count(*) FROM row_keys`).Scan(&actualKeys); err != nil {
		return err
	}
	if actualKeys != expectedKeys {
		mismatches++
		fmt.Fprintf(m.out, "FAIL row_keys source=%d target=%d\n", expectedKeys, actualKeys)
	} else {
		fmt.Fprintf(m.out, "verified row_keys %d\n", actualKeys)
	}
	var keyDifferences int64
	if err := m.target.QueryRow(ctx, `WITH expected AS (SELECT user_id,table_id,id AS row_id,unnest(key_hashes) AS key_hash FROM rows), difference AS (
 (SELECT user_id,table_id,row_id,key_hash FROM expected EXCEPT SELECT user_id,table_id,row_id,key_hash FROM row_keys)
 UNION ALL
 (SELECT user_id,table_id,row_id,key_hash FROM row_keys EXCEPT SELECT user_id,table_id,row_id,key_hash FROM expected)
 ) SELECT count(*) FROM difference`).Scan(&keyDifferences); err != nil {
		return err
	}
	if keyDifferences != 0 {
		return fmt.Errorf("row_keys mapping differs from row metadata")
	}
	if err := m.checkCounters(ctx); err != nil {
		return err
	}
	if err := m.verifyMoney(ctx); err != nil {
		return err
	}
	if mismatches > 0 {
		return fmt.Errorf("verification failed in %d tables", mismatches)
	}
	fmt.Fprintln(m.out, "verification passed: counts, documents, money, keys and counters")
	return nil
}
