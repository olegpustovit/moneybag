package tables

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (s *Service) validateLedger(ctx context.Context, p principal.Principal, before, after BatchState, definitions map[string]Table) ([]Violation, error) {
	issues := []Violation{}
	for _, db := range slices.Sorted(maps.Keys(definitions)) {
		constraint := definitions[db].Ledger
		if constraint == nil {
			continue
		}
		if s.Ledger == nil {
			return issues, fmt.Errorf("ledger constraints are not configured")
		}
		affected := map[string]bool{}
		for stage, state := range []BatchState{before, after} {
			for _, row := range state.Tables[db] {
				id, _ := row.Cells[constraint.Entry].(string)
				if id != "" {
					affected[id] = true
				} else if stage == 1 && !row.Archived {
					issues = append(issues, Violation{DB: db, RowID: row.ID, Column: constraint.Entry, Message: "ledger constraint requires an entry id"})
				}
			}
		}
		pending := map[string]Row{}
		for _, row := range after.Tables[db] {
			pending[row.ID] = row
		}
		for _, id := range slices.Sorted(maps.Keys(affected)) {
			entry, err := s.Ledger.Get(ctx, p, id)
			if errors.Is(err, principal.ErrNotFound) {
				continue
			}
			if err != nil {
				return issues, err
			}
			if entry.Status == "posted" {
				effective, err := s.Ledger.IsEffective(ctx, p, entry)
				if err != nil {
					return issues, err
				}
				if !effective {
					continue
				}
			}
			cursor, err := s.repo.db.Collection("rows").Find(ctx, bson.M{"user_id": p.UserID, "table_id": db, "cells." + constraint.Entry: id, "archived": bson.M{"$ne": true}})
			if err != nil {
				return issues, err
			}
			rows := map[string]Row{}
			for cursor.Next(ctx) {
				var row Row
				if err = cursor.Decode(&row); err != nil {
					break
				}
				rows[row.ID] = row
			}
			cursorErr := cursor.Err()
			_ = cursor.Close(ctx)
			if err != nil {
				return issues, err
			}
			if cursorErr != nil {
				return issues, cursorErr
			}
			for key, row := range pending {
				rows[key] = row
			}
			sum := new(big.Rat)
			count, scale := 0, 2
			for _, row := range rows {
				entryID, _ := row.Cells[constraint.Entry].(string)
				if row.Archived || entryID != id {
					continue
				}
				amount, err := decimalCell(row.Cells[constraint.Amount])
				if err != nil {
					issues = append(issues, Violation{DB: db, RowID: row.ID, Column: constraint.Amount, Message: "ledger constraint requires an exact decimal amount"})
					continue
				}
				sum.Add(sum, amount.Rat())
				count++
				if _, fraction, ok := strings.Cut(amount.String(), "."); ok {
					scale = max(scale, len(fraction))
				}
			}
			// With no rows the entry is unexplained; this permits undoing a whole receipt.
			if count == 0 {
				continue
			}
			totals := map[string]*big.Rat{}
			for _, posting := range entry.Postings {
				matches := false
				for _, prefix := range constraint.Accounts {
					if strings.HasPrefix(posting.Account, prefix) {
						matches = true
						break
					}
				}
				if !matches {
					continue
				}
				commodity := posting.Amount.Commodity
				if totals[commodity] == nil {
					totals[commodity] = new(big.Rat)
				}
				totals[commodity].Add(totals[commodity], posting.Amount.Value.Rat())
				if _, fraction, ok := strings.Cut(posting.Amount.Value.String(), "."); ok {
					scale = max(scale, len(fraction))
				}
			}
			if len(totals) != 1 {
				issues = append(issues, Violation{DB: db, Column: constraint.Amount, Message: fmt.Sprintf("entry %s must have postings in exactly one commodity under the configured account prefixes; got %d", id, len(totals))})
				continue
			}
			for commodity, want := range totals {
				if sum.Cmp(want) == 0 {
					continue
				}
				difference := new(big.Rat).Sub(want, sum)
				issues = append(issues, Violation{DB: db, Column: constraint.Amount, Message: fmt.Sprintf("entry %s rows sum to %s %s; entry postings are %s %s. Add or correct rows for the difference %s %s", id, sum.FloatString(scale), commodity, want.FloatString(scale), commodity, difference.FloatString(scale), commodity)})
			}
		}
	}
	return issues, nil
}
