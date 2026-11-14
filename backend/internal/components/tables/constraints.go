package tables

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

type RowCheck struct {
	Expr    string `json:"expr"`
	Message string `json:"message"`
	Level   string `json:"level,omitempty"`
}
type LedgerConstraint struct {
	Entry    string   `json:"entry"`
	Amount   string   `json:"amount"`
	Accounts []string `json:"accounts"`
}

var ledgerPrefix = regexp.MustCompile(`^(assets|liabilities|income|expenses|equity):[a-z0-9_:-]*$`)

func (s *Service) validateDefinition(table Table) error {
	if err := s.validateColumns(table.Columns); err != nil {
		return err
	}
	if len(table.Columns) < 1 || len(table.Columns) > 50 || len(table.Keys) > 10 || len(table.Checks) > 50 {
		return fmt.Errorf("require 1..50 columns, at most 10 keys and 50 checks")
	}
	columns := map[string]Column{}
	for _, column := range table.Columns {
		columns[column.ID] = column
	}
	seen := map[string]bool{}
	for _, key := range table.Keys {
		if len(key) < 1 || len(key) > 10 {
			return fmt.Errorf("a key needs 1..10 column ids")
		}
		fields := slices.Clone(key)
		slices.Sort(fields)
		for i, id := range fields {
			if _, ok := columns[id]; !ok {
				return fmt.Errorf("key column %s does not exist", id)
			}
			if i > 0 && fields[i-1] == id {
				return fmt.Errorf("key repeats column %s", id)
			}
		}
		name := strings.Join(fields, ",")
		if seen[name] {
			return fmt.Errorf("duplicate key %s", name)
		}
		seen[name] = true
	}
	for _, check := range table.Checks {
		if check.Message == "" || len(check.Message) > 1000 || (check.Level != "" && check.Level != "error" && check.Level != "warn") {
			return fmt.Errorf("checks need a message up to 1000 bytes and level error or warn")
		}
		result := s.expr.Check(check.Expr)
		if !result.OK || (result.Type != "bool" && result.Type != "dyn") {
			return fmt.Errorf("check must compile and return boolean: %s", check.Expr)
		}
	}
	if table.Ledger != nil {
		constraint := table.Ledger
		if columns[constraint.Entry].Type != "entry" || columns[constraint.Amount].Type != "decimal" || len(constraint.Accounts) < 1 || len(constraint.Accounts) > 20 {
			return fmt.Errorf("ledger needs entry/decimal columns and 1..20 account prefixes")
		}
		for _, prefix := range constraint.Accounts {
			if len(prefix) > 200 || !ledgerPrefix.MatchString(prefix) {
				return fmt.Errorf("invalid ledger account prefix %q", prefix)
			}
		}
	}
	return nil
}

func keyHashes(table Table, cells map[string]any) ([]string, error) {
	hashes := []string{}
	for _, key := range table.Keys {
		fields := slices.Clone(key)
		slices.Sort(fields)
		values := []any{}
		present := true
		for _, field := range fields {
			value := cells[field]
			if value == nil {
				present = false
				break
			}
			if text, ok := value.(string); ok && text == "" {
				present = false
				break
			}
			if decimal, ok := value.(ledger.Decimal); ok {
				value = decimal.Rat().RatString()
			}
			if number, ok := asNumber(value); ok && number == 0 {
				value = 0
			}
			values = append(values, value)
		}
		if !present {
			continue
		}
		encoded, err := json.Marshal(struct {
			Columns []string `json:"columns"`
			Values  []any    `json:"values"`
		}{fields, values})
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(encoded)
		hashes = append(hashes, hex.EncodeToString(hash[:]))
	}
	slices.Sort(hashes)
	return hashes, nil
}

func (s *Service) rowChecks(ctx context.Context, table Table, row Row) ([]Violation, []Violation) {
	errors, warnings := []Violation{}, []Violation{}
	if row.Archived {
		return errors, warnings
	}
	cells := maps.Clone(row.Cells)
	for _, column := range table.Columns {
		if _, ok := cells[column.ID]; !ok {
			if column.Type == "string" || column.Type == "entry" || column.Type == "account" || column.Type == "ref" {
				cells[column.ID] = ""
			} else {
				cells[column.ID] = nil
			}
		}
	}
	for _, check := range table.Checks {
		result, err := s.expr.Eval(ctx, check.Expr, map[string]any{"row": cells})
		ok, boolean := result.Value.(bool)
		if err == nil && boolean && ok {
			continue
		}
		issue := Violation{DB: table.ID, RowID: row.ID, Message: check.Message}
		if err != nil {
			issue.Message += "; check evaluation failed: " + err.Error()
		} else if !boolean {
			issue.Message += "; check must return boolean"
		}
		if check.Level == "warn" && err == nil && boolean {
			warnings = append(warnings, issue)
		} else {
			errors = append(errors, issue)
		}
	}
	return errors, warnings
}

func (s *Service) validateKeys(ctx context.Context, p principal.Principal, state BatchState, definitions map[string]Table) ([]Violation, error) {
	issues := []Violation{}
	for _, db := range slices.Sorted(maps.Keys(state.Tables)) {
		pending := map[string]Row{}
		seen := map[string]string{}
		for _, row := range state.Tables[db] {
			pending[row.ID] = row
		}
		for _, row := range state.Tables[db] {
			if row.Archived || len(row.KeyHashes) == 0 {
				continue
			}
			for _, hash := range row.KeyHashes {
				if prior, ok := seen[hash]; ok && prior != row.ID {
					issues = append(issues, Violation{DB: db, RowID: row.ID, Message: "unique key also used by row " + prior})
				}
				seen[hash] = row.ID
			}
			args := pgx.NamedArgs{"user": p.UserID, "table": db, "hashes": row.KeyHashes}
			matches := []string{`EXISTS(SELECT 1 FROM row_keys k WHERE k.user_id=r.user_id AND k.table_id=r.table_id AND k.row_id=r.id AND k.key_hash=ANY(@hashes::text[]))`}
			for i, key := range definitions[db].Keys {
				values := []string{}
				for j, column := range key {
					value := row.Cells[column]
					if value == nil || value == "" {
						continue
					}
					cast := "text"
					for _, c := range definitions[db].Columns {
						if c.ID == column {
							switch c.Type {
							case "decimal", "number":
								cast = "numeric"
							case "bool":
								cast = "boolean"
							case "datetime":
								cast = "timestamptz"
							}
						}
					}
					col, val := fmt.Sprintf("key%dcol%d", i, j), fmt.Sprintf("key%dval%d", i, j)
					args[col], args[val] = column, fmt.Sprint(value)
					values = append(values, fmt.Sprintf("(r.cells->>@%s)::%s=@%s::%s", col, cast, val, cast))
				}
				if len(values) == len(key) {
					matches = append(matches, "("+strings.Join(values, " AND ")+")")
				}
			}
			cursor, err := s.repo.db.Query(ctx, `SELECT r.* FROM rows r WHERE r.user_id=@user AND r.table_id=@table AND NOT r.archived AND (`+strings.Join(matches, " OR ")+`)`, args)
			if err != nil {
				return issues, err
			}
			for cursor.Next() {
				var other Row
				if other, err = rowFrom(cursor, definitions[db]); err != nil {
					break
				}
				if next, ok := pending[other.ID]; ok {
					if other.ID != row.ID {
						for _, hash := range row.KeyHashes {
							if slices.Contains(other.KeyHashes, hash) {
								issues = append(issues, Violation{DB: db, RowID: row.ID, Message: "key is still held by row " + other.ID + "; key swaps or transfers must use a separate temporary key before this batch"})
								break
							}
						}
					}
					other = next
				}
				computed, hashErr := keyHashes(definitions[db], other.Cells)
				if hashErr != nil {
					err = hashErr
					break
				}
				other.KeyHashes = computed
				if other.ID == row.ID || other.Archived {
					continue
				}
				for _, hash := range row.KeyHashes {
					if slices.Contains(other.KeyHashes, hash) {
						issues = append(issues, Violation{DB: db, RowID: row.ID, Message: "unique key already used by row " + other.ID})
						break
					}
				}
			}
			cursorErr := cursor.Err()
			cursor.Close()
			if err != nil {
				return issues, err
			}
			if cursorErr != nil {
				return issues, cursorErr
			}
		}
	}
	return issues, nil
}

func (s *Service) stateConstraints(ctx context.Context, p principal.Principal, before BatchState, after *BatchState, definitions map[string]Table) ([]Violation, error) {
	issues := []Violation{}
	if definitions == nil {
		definitions = map[string]Table{}
	}
	for _, db := range slices.Sorted(maps.Keys(after.Tables)) {
		table, ok := definitions[db]
		if !ok {
			var err error
			table, err = s.Get(ctx, p, db)
			if err != nil {
				return issues, err
			}
			definitions[db] = table
		}
		for i := range after.Tables[db] {
			row := &after.Tables[db][i]
			row.KeyHashes = nil
			if row.Archived {
				continue
			}
			var err error
			row.KeyHashes, err = keyHashes(table, row.Cells)
			if err != nil {
				return issues, err
			}
			failed, warnings := s.rowChecks(ctx, table, *row)
			issues = append(issues, failed...)
			after.warnings = append(after.warnings, warnings...)
		}
	}
	references, err := s.validateReferences(ctx, p, *after, definitions)
	if err != nil {
		return issues, err
	}
	issues = append(issues, references...)
	keys, err := s.validateKeys(ctx, p, *after, definitions)
	if err != nil {
		return issues, err
	}
	issues = append(issues, keys...)
	ledgerIssues, err := s.validateLedger(ctx, p, before, *after, definitions)
	if err != nil {
		return issues, err
	}
	issues = append(issues, ledgerIssues...)
	return issues, nil
}
