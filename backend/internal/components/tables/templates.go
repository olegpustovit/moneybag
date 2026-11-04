package tables

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Template struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Tables      []Table `json:"tables"`
	Playbook    string  `json:"playbook"`
}
type CreateResult struct {
	Table
	Tables  []Table `json:"tables,omitempty"`
	EventID string  `json:"event_id,omitempty"`
}

func Templates() []Template {
	country := Column{ID: "country", Type: "string", Required: true, Pattern: "^[A-Z]{2}$", Description: "ISO 3166-1 alpha-2 country code."}
	entry := Column{ID: "entry", Type: "entry", Required: true, Description: "Existing ledger entry id; do not duplicate date, payee or postings."}
	attrs := Column{ID: "attrs", Type: "json", Description: "Source-specific details, tax breakdowns, fiscal signatures and loyalty savings."}
	tables := []Table{
		{ID: "merchants", Name: "Merchants", Description: "Legal merchants; use natural row id <country>:<tax_id> when known, otherwise a stable agent-chosen id. Do not invent missing tax ids or legal names.", Keys: [][]string{{"country", "tax_id"}}, Columns: []Column{country, {ID: "tax_id", Type: "string"}, {ID: "tax_scheme", Type: "string"}, {ID: "legal_name", Type: "string"}, {ID: "brand", Type: "string"}, attrs}},
		{ID: "stores", Name: "Stores", Description: "Physical or online stores; natural row id <merchant>:<store_code>. Reuse existing stores.", Keys: [][]string{{"merchant", "store_code"}}, Columns: []Column{{ID: "merchant", Type: "ref", Table: "merchants", Required: true}, {ID: "store_code", Type: "string", Required: true}, {ID: "label", Type: "string", Required: true}, {ID: "address", Type: "string"}, {ID: "city", Type: "string"}, {ID: "postcode", Type: "string"}, country, {ID: "lat", Type: "decimal"}, {ID: "lon", Type: "decimal"}, {ID: "payees", Type: "list"}, {ID: "external_ids", Type: "json", Description: "Other systems' store ids, including external price-list ids."}}},
		{ID: "products", Name: "Products", Description: "Shared products; natural row id gtin:<gtin>, or a stable id chosen by the agent when no GTIN exists.", Keys: [][]string{{"gtin"}}, Checks: []RowCheck{{Expr: `row.gtin == "" || gtin_valid(row.gtin)`, Message: "GTIN checksum is invalid", Level: "error"}}, Columns: []Column{{ID: "gtin", Type: "string"}, {ID: "name", Type: "string", Required: true}, {ID: "brand", Type: "string"}, {ID: "size", Type: "decimal"}, {ID: "size_unit", Type: "string", Enum: []any{"g", "ml", "pc"}}, {ID: "category", Type: "string"}, {ID: "aliases", Type: "list"}, {ID: "external_ids", Type: "json"}}},
		{ID: "receipts", Name: "Receipts", Description: "Receipt headers attached to ledger entries; row id is the entry id. Store fiscal and country-specific extras in attrs.", Keys: [][]string{{"entry"}, {"store", "receipt_ref"}}, Ledger: &LedgerConstraint{Entry: "entry", Amount: "total", Accounts: []string{"expenses:"}}, Columns: []Column{entry, {ID: "store", Type: "ref", Table: "stores", Required: true}, {ID: "issued_at", Type: "datetime"}, {ID: "receipt_ref", Type: "string"}, {ID: "currency", Type: "string", Required: true, Pattern: "^[A-Z]{3}$", Description: "ISO 4217 currency code."}, {ID: "total", Type: "decimal", Required: true}, {ID: "payment", Type: "string", Enum: []any{"cash", "card", "transfer", "other"}}, {ID: "source", Type: "string", Enum: []any{"photo", "e-receipt", "manual"}}, attrs}},
		{ID: "receipt_items", Name: "Receipt items", Description: "Receipt lines; natural row id <entry>:<line>. Include discounts, deposits, tips, fees and rounding so amounts match the ledger.", Keys: [][]string{{"entry", "line"}}, Ledger: &LedgerConstraint{Entry: "entry", Amount: "amount", Accounts: []string{"expenses:"}}, Checks: []RowCheck{{Expr: `row.qty == null || row.unit_price == null || row.qty * row.unit_price == row.amount`, Message: "Quantity times unit price differs from line amount; verify discounts or rounding", Level: "warn"}}, Columns: []Column{entry, {ID: "line", Type: "number", Required: true}, {ID: "raw_name", Type: "string", Required: true}, {ID: "product", Type: "ref", Table: "products"}, {ID: "qty", Type: "decimal"}, {ID: "unit", Type: "string"}, {ID: "unit_price", Type: "decimal"}, {ID: "amount", Type: "decimal", Required: true}, {ID: "kind", Type: "string", Required: true, Enum: []any{"item", "discount", "deposit", "rounding", "tip", "fee"}}, {ID: "category", Type: "account"}, attrs}},
	}
	return []Template{{ID: "receipts", Description: "Five ordinary ledger-aware tables for receipts in any country.", Tables: tables, Playbook: "Find the entry with entries_list explained_by:'!receipt_items'. Reuse merchants and stores with db_query on their keys and use natural row ids. Keep country-specific extras in attrs and external catalog ids in external_ids. Write merchant, store, products, receipt and all lines together in one db_batch; dry_run first, correct every violation, then commit. Use decimal strings for money. Match expense postings exactly, including discounts, deposits, rounding, tips and fees. Choose the correct expense account prefixes with db_alter if your ledger uses a different convention. Query joined fields with join_entry and expand; table: dashboard sources join automatically."}}
}

func (s *Service) CreateDatabase(ctx context.Context, p principal.Principal, in CreateInput) (CreateResult, error) {
	if in.Template == "" {
		table, err := s.Create(ctx, p, in)
		return CreateResult{Table: table}, err
	}
	if err := p.Require("model:write"); err != nil {
		return CreateResult{}, err
	}
	if in.Name != "" || len(in.Columns) > 0 || in.Description != "" || len(in.Keys) > 0 || len(in.Checks) > 0 || in.Ledger != nil {
		return CreateResult{}, fmt.Errorf("template cannot be combined with a custom schema; use db_alter afterwards")
	}
	var template Template
	for _, candidate := range Templates() {
		if candidate.ID == in.Template {
			template = candidate
		}
	}
	if template.ID == "" {
		return CreateResult{}, fmt.Errorf("unknown template %q; use db_templates", in.Template)
	}
	for i := range template.Tables {
		table := &template.Tables[i]
		table.Key = p.UserID + ":" + table.ID
		table.UserID = p.UserID
		if err := s.validateColumns(table.Columns); err != nil {
			return CreateResult{}, err
		}
		if err := s.validateDefinition(*table); err != nil {
			return CreateResult{}, err
		}
		if err := s.columnIndexes(ctx, table.Columns, table.Keys...); err != nil {
			return CreateResult{}, err
		}
	}
	if err := s.uniqueIndex(ctx); err != nil {
		return CreateResult{}, err
	}
	result := CreateResult{Tables: template.Tables}
	for _, table := range template.Tables {
		if table.ID == "receipts" {
			result.Table = table
		}
	}
	err := s.audit.Workflow(ctx, func(locked context.Context) error {
		missing := []Table{}
		for _, table := range template.Tables {
			var existing Table
			err := s.repo.db.Collection("tables").FindOne(locked, bson.M{"user_id": p.UserID, "id": table.ID}).Decode(&existing)
			if errors.Is(err, mongo.ErrNoDocuments) {
				missing = append(missing, table)
				continue
			}
			if err != nil {
				return err
			}
			if existing.Archived || !reflect.DeepEqual(schemaState(existing), schemaState(table)) {
				return fmt.Errorf("template table %s already exists with a different schema; existing tables were left unchanged: %w", table.ID, principal.ErrConflict)
			}
		}
		if len(missing) == 0 {
			return nil
		}

		id, err := s.audit.Write(locked, p, "db_template_create", audit.Target{Collection: "tables", ID: template.ID}, "", nil, missing, func(tx context.Context) error {
			return s.audit.Workflow(tx, func(tx context.Context) error {
				for i, table := range missing {
					if _, err := s.repo.db.Collection("tables").InsertOne(tx, table); err != nil {
						return &partialWriteError{written: i, total: len(missing), kind: "tables", cause: err}
					}
				}
				return nil
			})
		})
		result.EventID = id
		var partial *partialWriteError
		if errors.As(err, &partial) {
			partial.eventID = id
		}
		return err
	})
	if err != nil {
		return CreateResult{}, err
	}
	return result, nil
}

func (s *Service) ArchiveTemplate(ctx context.Context, p principal.Principal, created []Table, reason string) error {
	if err := p.Require("model:write"); err != nil {
		return err
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		selected := map[string]bool{}
		for _, table := range created {
			selected[table.ID] = true
		}
		definitions, err := s.allTables(tx, p)
		if err != nil {
			return err
		}
		for _, table := range definitions {
			if selected[table.ID] {
				continue
			}
			for _, column := range table.Columns {
				if column.Type == "ref" && selected[column.Table] {
					return fmt.Errorf("table %s column %s references template table %s; remove that reference before undo", table.ID, column.ID, column.Table)
				}
			}
		}
		for i, table := range slices.Backward(created) {
			if err := s.Archive(tx, p, table.ID, true, reason); err != nil {
				return &partialWriteError{written: len(created) - 1 - i, total: len(created), kind: "table archives", cause: err}
			}
		}
		return nil
	})
}
