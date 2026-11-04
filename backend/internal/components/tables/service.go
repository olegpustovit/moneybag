package tables

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Column struct {
	Table       string `json:"table,omitempty" bson:"table,omitempty"`
	Description string `json:"description,omitempty" bson:"description,omitempty"`
	Required    bool   `json:"required,omitempty" bson:"required,omitempty"`
	Enum        []any  `json:"enum,omitempty" bson:"enum,omitempty"`
	Pattern     string `json:"pattern,omitempty" bson:"pattern,omitempty"`
	ID          string `json:"id" bson:"id"`
	Type        string `json:"type" bson:"type"`
	Formula     string `json:"formula,omitempty" bson:"formula,omitempty"`
}
type Table struct {
	Keys        [][]string        `json:"keys,omitempty" bson:"keys,omitempty"`
	Checks      []RowCheck        `json:"checks,omitempty" bson:"checks,omitempty"`
	Ledger      *LedgerConstraint `json:"ledger,omitempty" bson:"ledger,omitempty"`
	Description string            `json:"description,omitempty" bson:"description,omitempty"`
	Archived    bool              `json:"archived,omitempty" bson:"archived,omitempty"`
	Key         string            `json:"-" bson:"_id"`
	UserID      string            `json:"-" bson:"user_id"`
	ID          string            `json:"id" bson:"id"`
	Name        string            `json:"name" bson:"name"`
	Columns     []Column          `json:"columns" bson:"columns"`
}

func (t *Table) UnmarshalBSON(data []byte) error {
	type table Table
	decoder := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(data)))
	decoder.DefaultDocumentM()
	return decoder.Decode((*table)(t))
}

type Row struct {
	Entry     *JoinedEntry `json:"entry,omitempty" bson:"-"`
	joined    bool
	KeyHashes []string       `json:"-" bson:"key_hashes,omitempty"`
	Archived  bool           `json:"archived,omitempty" bson:"archived,omitempty"`
	Key       string         `json:"-" bson:"_id"`
	UserID    string         `json:"-" bson:"user_id"`
	TableID   string         `json:"-" bson:"table_id"`
	ID        string         `json:"id" bson:"id"`
	Cells     map[string]any `json:"cells" bson:"cells"`
	Version   int            `json:"version" bson:"version"`
}

// Decode nested cells as maps so CEL can address JSON fields by name.
func (r *Row) UnmarshalBSON(data []byte) error {
	type row Row
	decoder := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(data)))
	decoder.DefaultDocumentM()
	if err := decoder.Decode((*row)(r)); err != nil {
		return err
	}
	for id, value := range r.Cells {
		if decimal, ok := value.(bson.Decimal128); ok {
			r.Cells[id] = ledger.Decimal{Decimal128: decimal}
		}
	}
	return nil
}

type CreateInput struct {
	Template    string            `json:"template,omitempty"`
	Keys        [][]string        `json:"keys,omitempty"`
	Checks      []RowCheck        `json:"checks,omitempty"`
	Ledger      *LedgerConstraint `json:"ledger,omitempty"`
	Description string            `json:"description,omitempty"`
	Name        string            `json:"name,omitempty"`
	Columns     []Column          `json:"columns,omitempty"`
}
type QueryInput struct {
	JoinEntry bool     `json:"join_entry,omitempty"`
	Expand    []string `json:"expand,omitempty"`
	Cursor    string   `json:"cursor,omitempty"`
	DB        string   `json:"db"`
	Where     string   `json:"where,omitempty"`
	Sort      []string `json:"sort,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}
type QueryResult struct {
	NextCursor string   `json:"next_cursor,omitempty"`
	Truncated  bool     `json:"truncated,omitempty"`
	Rows       []Row    `json:"rows"`
	Columns    []Column `json:"columns"`
}
type CellUpdate struct {
	RowID   string `json:"row_id"`
	Column  string `json:"column"`
	Value   any    `json:"value"`
	Version *int   `json:"version,omitempty"`
}
type UpdateInput struct {
	DB      string       `json:"db"`
	Updates []CellUpdate `json:"updates"`
}
type UpdateResult struct {
	Warnings             []Violation `json:"warnings,omitempty"`
	Rows                 []Row       `json:"rows"`
	DependentsRecomputed []string    `json:"dependents_recomputed"`
	EventID              string      `json:"event_id"`
}
type MongoRepository struct {
	db *mongo.Database
}
type Service struct {
	Ledger *ledger.Service
	Rates  *rates.Service
	repo   *MongoRepository
	audit  *audit.Service
	expr   *expressions.Engine
}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db: db} }
func NewService(r *MongoRepository, a *audit.Service, e *expressions.Engine) *Service {
	return &Service{repo: r, audit: a, expr: e}
}

var fieldID = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,99}$`)
var ColumnTypes = []string{"number", "string", "bool", "date", "formula", "decimal", "datetime", "entry", "account", "ref", "list", "json"}
var datetimePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)

func (s *Service) Create(ctx context.Context, p principal.Principal, in CreateInput) (Table, error) {
	return s.CreateNamed(ctx, p, ulid.Make().String(), in)
}
func (s *Service) CreateNamed(ctx context.Context, p principal.Principal, id string, in CreateInput) (result Table, err error) {
	err = s.audit.Workflow(ctx, func(locked context.Context) error { result, err = s.createNamed(locked, p, id, in); return err })
	return result, err
}
func (s *Service) createNamed(ctx context.Context, p principal.Principal, id string, in CreateInput) (Table, error) {
	if e := p.Require("model:write"); e != nil {
		return Table{}, e
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 150 || len(in.Columns) < 1 || len(in.Columns) > 50 {
		return Table{}, fmt.Errorf("name and 1..50 columns required")
	}
	if len(in.Description) > 10000 {
		return Table{}, fmt.Errorf("table description exceeds 10000 bytes")
	}
	if e := s.validateColumns(in.Columns); e != nil {
		return Table{}, e
	}
	if err := s.referenceSchemas(ctx, p, id, in.Columns); err != nil {
		return Table{}, err
	}
	if err := s.columnIndexes(ctx, in.Columns, in.Keys...); err != nil {
		return Table{}, err
	}
	if err := s.uniqueIndex(ctx); err != nil {
		return Table{}, err
	}
	t := Table{Key: p.UserID + ":" + id, UserID: p.UserID, ID: id, Name: in.Name, Description: in.Description, Columns: in.Columns, Keys: in.Keys, Checks: in.Checks, Ledger: in.Ledger}
	if err := s.validateDefinition(t); err != nil {
		return Table{}, err
	}
	_, e := s.audit.Write(ctx, p, "db_create", audit.Target{Collection: "tables", ID: id}, "", nil, t, func(tx context.Context) error {
		_, err := s.repo.db.Collection("tables").InsertOne(tx, t)
		if mongo.IsDuplicateKeyError(err) {
			return principal.ErrConflict
		}
		return err
	})
	return t, e
}
func (s *Service) validateColumns(columns []Column) error {
	seen := map[string]bool{}
	for _, c := range columns {
		if !fieldID.MatchString(c.ID) || seen[c.ID] {
			return fmt.Errorf("column ids must be unique identifiers")
		}
		seen[c.ID] = true
		if len(c.Description) > 10000 {
			return fmt.Errorf("column %s description exceeds 10000 bytes", c.ID)
		}
		if !slices.Contains(ColumnTypes, c.Type) {
			return fmt.Errorf("unknown column type %q", c.Type)
		}
		if c.Type == "ref" {
			if c.Table == "" || len(c.Table) > 100 {
				return fmt.Errorf("column %s ref requires a target table id", c.ID)
			}
		} else if c.Table != "" {
			return fmt.Errorf("column %s table is only valid for ref columns", c.ID)
		}
		if len(c.Enum) > 100 {
			return fmt.Errorf("column %s enum exceeds 100 values", c.ID)
		}
		for _, value := range c.Enum {
			valid := validValue(Column{Type: c.Type}, value)
			if c.Type == "formula" {
				encoded, err := json.Marshal(value)
				valid = err == nil && len(encoded) <= 16*1024
			}
			if value == nil || !valid {
				return fmt.Errorf("column %s enum contains a value of the wrong type", c.ID)
			}
		}
		if c.Pattern != "" {
			if c.Type != "string" && c.Type != "date" && c.Type != "datetime" && c.Type != "decimal" && c.Type != "entry" && c.Type != "account" && c.Type != "ref" && c.Type != "formula" {
				return fmt.Errorf("column %s pattern requires a string-valued type", c.ID)
			}
			if len(c.Pattern) > 2000 {
				return fmt.Errorf("column %s pattern exceeds 2000 bytes", c.ID)
			}
			if _, err := regexp.Compile(c.Pattern); err != nil {
				return fmt.Errorf("column %s pattern: %w", c.ID, err)
			}
		}
		if c.Formula != "" {
			if _, e := s.expr.Compile(c.Formula); e != nil {
				return fmt.Errorf("formula %s: %w", c.ID, e)
			}
		} else if c.Type == "formula" {
			return fmt.Errorf("formula column requires an expression")
		}
	}
	return nil
}
func (s *Service) Get(ctx context.Context, p principal.Principal, id string) (Table, error) {
	if e := p.Valid(); e != nil {
		return Table{}, e
	}
	var t Table
	e := s.repo.db.Collection("tables").FindOne(ctx, bson.M{"user_id": p.UserID, "id": id, "archived": bson.M{"$ne": true}}).Decode(&t)
	if errors.Is(e, mongo.ErrNoDocuments) {
		e = principal.ErrNotFound
	}
	return t, e
}
func (s *Service) List(ctx context.Context, p principal.Principal) ([]Table, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	cur, e := s.repo.db.Collection("tables").Find(ctx, bson.M{"user_id": p.UserID, "archived": bson.M{"$ne": true}}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}).SetLimit(501))
	if e != nil {
		return nil, e
	}
	defer cur.Close(ctx)
	out := []Table{}
	e = cur.All(ctx, &out)
	if len(out) > 500 {
		return nil, fmt.Errorf("too many databases")
	}
	return out, e
}
func asNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
func validValue(c Column, v any) bool {
	if v == nil {
		return !c.Required
	}
	if !validConstraints(c, v) {
		return false
	}
	switch c.Type {
	case "entry", "account", "ref":
		text, ok := v.(string)
		return ok && len(text) <= 200
	case "decimal":
		_, err := decimalCell(v)
		return err == nil
	case "datetime":
		text, ok := v.(string)
		if !ok || !datetimePattern.MatchString(text) {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, text)
		return err == nil
	case "list":
		switch values := v.(type) {
		case []string:
			for _, value := range values {
				if len(value) > 10000 {
					return false
				}
			}
			return true
		case []any:
			for _, value := range values {
				text, ok := value.(string)
				if !ok || len(text) > 10000 {
					return false
				}
			}
			return true
		case bson.A:
			return validValue(c, []any(values))
		}
		return false
	case "json":
		switch v.(type) {
		case map[string]any, bson.M:
		default:
			return false
		}
		encoded, err := json.Marshal(v)
		return err == nil && len(encoded) <= 16*1024
	case "number":
		n, ok := asNumber(v)
		return ok && !math.IsNaN(n) && !math.IsInf(n, 0)
	case "bool":
		_, ok := v.(bool)
		return ok
	case "string":
		s, ok := v.(string)
		return ok && len(s) <= 10000
	case "date":
		s, ok := v.(string)
		if !ok {
			return false
		}
		_, e := time.Parse("2006-01-02", s)
		return e == nil
	}
	return false
}

func validConstraints(c Column, value any) bool {
	if value == nil {
		return !c.Required
	}
	text, isText := value.(string)
	if c.Type == "decimal" {
		decimal, err := decimalCell(value)
		if err != nil {
			return false
		}
		text, isText = decimal.String(), true
	}
	if c.Required && isText && text == "" {
		return false
	}
	if c.Pattern != "" {
		pattern, err := regexp.Compile(c.Pattern)
		if err != nil || !isText || !pattern.MatchString(text) {
			return false
		}
	}
	if len(c.Enum) > 0 {
		encoded, err := json.Marshal(value)
		if err != nil {
			return false
		}
		for _, allowed := range c.Enum {
			if c.Type == "decimal" {
				a, ae := decimalCell(value)
				b, be := decimalCell(allowed)
				if ae == nil && be == nil && a.Rat().Cmp(b.Rat()) == 0 {
					return true
				}
				continue
			}
			other, err := json.Marshal(allowed)
			if err == nil && bytes.Equal(encoded, other) {
				return true
			}
		}
		return false
	}
	return true
}

func decimalCell(value any) (ledger.Decimal, error) {
	switch v := value.(type) {
	case string:
		return ledger.ParseDecimal(v)
	case ledger.Decimal:
		return ledger.ParseDecimal(v.String())
	}
	return ledger.Decimal{}, fmt.Errorf("decimal cells require finite decimal strings")
}

func normalizeCells(columns []Column, cells map[string]any) error {
	for _, column := range columns {
		if column.Type != "decimal" || cells[column.ID] == nil {
			continue
		}
		value, err := decimalCell(cells[column.ID])
		if err != nil {
			return fmt.Errorf("column %s: %w", column.ID, err)
		}
		cells[column.ID] = value
	}
	return nil
}

var typeHints = map[string]string{
	"decimal":  `a decimal string such as "2.52"`,
	"datetime": `an RFC 3339 string with offset such as "2026-11-03T18:42:00+01:00"`,
	"date":     `a "YYYY-MM-DD" string`,
	"number":   "a finite number",
	"bool":     "true or false",
	"string":   "a string up to 10000 bytes",
	"entry":    "a ledger entry id string",
	"account":  "an account id string such as expenses:food",
	"ref":      "a row id string from the referenced table",
	"list":     "an array of strings",
	"json":     "an object up to 16 KB",
}

// cellHint tells the caller which rule a rejected cell broke and what to send instead.
func cellHint(c Column, v any) string {
	if v == nil || v == "" {
		if c.Required {
			return "required"
		}
	}
	plain := c
	plain.Enum, plain.Pattern = nil, ""
	if c.Type != "formula" && !validValue(plain, v) {
		return "expects " + typeHints[c.Type]
	}
	if len(c.Enum) > 0 {
		allowed, _ := json.Marshal(c.Enum)
		return "must be one of " + string(allowed)
	}
	if c.Pattern != "" {
		return "must match pattern " + c.Pattern
	}
	return "invalid value"
}

func validateCells(columns []Column, cells map[string]any) error {
	for _, column := range columns {
		valid := validValue(column, cells[column.ID])
		if column.Type == "formula" {
			valid = validConstraints(column, cells[column.ID])
		}
		if !valid {
			return fmt.Errorf("column %s: %s", column.ID, cellHint(column, cells[column.ID]))
		}
	}
	return nil
}
func (s *Service) formulas(ctx context.Context, columns []Column, cells map[string]any) error {
	pending := map[string]string{}
	for _, c := range columns {
		if c.Formula != "" {
			pending[c.ID] = c.Formula
			delete(cells, c.ID)
		}
	}
	for len(pending) > 0 {
		progress := false
		var last error
		for id, formula := range pending {
			r, e := s.expr.Eval(ctx, formula, map[string]any{"row": cells})
			if e != nil {
				last = e
				continue
			}
			cells[id] = r.Value
			if decimal, ok := r.Value.(interface{ Rat() *big.Rat }); ok {
				value, err := ledger.DecimalFromRat(decimal.Rat())
				if err != nil {
					return fmt.Errorf("formula %s: %w", id, err)
				}
				cells[id] = value
			}
			delete(pending, id)
			progress = true
		}
		if !progress {
			return fmt.Errorf("formula dependency cycle or invalid reference: %w", last)
		}
	}
	return normalizeCells(columns, cells)
}
func (s *Service) Update(ctx context.Context, p principal.Principal, in UpdateInput) (result UpdateResult, err error) {
	err = s.audit.Workflow(ctx, func(tx context.Context) error {
		result, err = s.update(tx, p, in)
		return err
	})
	return result, err
}

func (s *Service) update(ctx context.Context, p principal.Principal, in UpdateInput) (UpdateResult, error) {
	if err := p.Require("model:write"); err != nil {
		return UpdateResult{}, err
	}
	if len(in.Updates) < 1 || len(in.Updates) > 500 {
		return UpdateResult{}, fmt.Errorf("1..500 cell updates required")
	}
	ops := make([]BatchOp, 0, len(in.Updates))
	for _, update := range in.Updates {
		ops = append(ops, BatchOp{DB: in.DB, Upsert: &RowUpsert{RowID: update.RowID, Cells: map[string]any{update.Column: update.Value}, Version: update.Version}, revive: true})
	}
	return s.changeRows(ctx, p, in.DB, ops)
}

func (s *Service) changeRows(ctx context.Context, p principal.Principal, db string, ops []BatchOp) (UpdateResult, error) {
	table, err := s.Get(ctx, p, db)
	if err != nil {
		return UpdateResult{}, err
	}
	result := UpdateResult{Rows: []Row{}, DependentsRecomputed: []string{}}
	before := []Row{}
	event, err := s.audit.Write(ctx, p, "db_update_cells", audit.Target{Collection: "tables", ID: db}, "", &before, &result.Rows, func(tx context.Context) error {
		return s.audit.Workflow(tx, func(snapshot context.Context) error {
			prior, next, issues, err := s.prepareBatch(snapshot, p, ops)
			if err != nil {
				return err
			}
			if len(issues) > 0 {
				for _, issue := range issues {
					if issue.Message == "version conflict; read the row and retry" {
						return principal.ErrConflict
					}
				}
				return fmt.Errorf("%s", batchMessage(issues))
			}
			if len(next.Tables[db]) == 0 {
				return fmt.Errorf("1..500 rows required")
			}
			before = prior.Tables[db]
			result.Rows = next.Tables[db]
			result.Warnings = next.warnings
			return s.writeBatch(snapshot, p, next)
		})
	})
	result.EventID = event
	var partial *partialWriteError
	if errors.As(err, &partial) {
		partial.eventID = event
	}
	for _, column := range table.Columns {
		if column.Formula != "" {
			result.DependentsRecomputed = append(result.DependentsRecomputed, column.ID)
		}
	}
	return result, err
}
