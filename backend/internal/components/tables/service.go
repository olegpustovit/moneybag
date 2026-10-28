package tables

import (
	"context"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Column struct {
	ID      string `json:"id" bson:"id"`
	Type    string `json:"type" bson:"type"`
	Formula string `json:"formula,omitempty" bson:"formula,omitempty"`
}
type Table struct {
	Archived bool     `json:"archived,omitempty" bson:"archived,omitempty"`
	Key      string   `json:"-" bson:"_id"`
	UserID   string   `json:"-" bson:"user_id"`
	ID       string   `json:"id" bson:"id"`
	Name     string   `json:"name" bson:"name"`
	Columns  []Column `json:"columns" bson:"columns"`
}
type Row struct {
	Archived bool           `json:"archived,omitempty" bson:"archived,omitempty"`
	Key      string         `json:"-" bson:"_id"`
	UserID   string         `json:"-" bson:"user_id"`
	TableID  string         `json:"-" bson:"table_id"`
	ID       string         `json:"id" bson:"id"`
	Cells    map[string]any `json:"cells" bson:"cells"`
	Version  int            `json:"version" bson:"version"`
}
type CreateInput struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
}
type QueryInput struct {
	DB    string   `json:"db"`
	Where string   `json:"where,omitempty"`
	Sort  []string `json:"sort,omitempty"`
	Limit int      `json:"limit,omitempty"`
}
type QueryResult struct {
	Rows    []Row    `json:"rows"`
	Columns []Column `json:"columns"`
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
	Rows                 []Row    `json:"rows"`
	DependentsRecomputed []string `json:"dependents_recomputed"`
	EventID              string   `json:"event_id"`
}
type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo  *MongoRepository
	audit *audit.Service
	expr  *expressions.Engine
}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func NewService(r *MongoRepository, a *audit.Service, e *expressions.Engine) *Service {
	return &Service{r, a, e}
}

var fieldID = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,99}$`)

func (s *Service) Create(ctx context.Context, p principal.Principal, in CreateInput) (Table, error) {
	return s.CreateNamed(ctx, p, ulid.Make().String(), in)
}
func (s *Service) CreateNamed(ctx context.Context, p principal.Principal, id string, in CreateInput) (Table, error) {
	if e := p.Require("model:write"); e != nil {
		return Table{}, e
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 150 || len(in.Columns) < 1 || len(in.Columns) > 50 {
		return Table{}, fmt.Errorf("name and 1..50 columns required")
	}
	seen := map[string]bool{}
	for _, c := range in.Columns {
		if !fieldID.MatchString(c.ID) || seen[c.ID] {
			return Table{}, fmt.Errorf("column ids must be unique identifiers")
		}
		seen[c.ID] = true
		switch c.Type {
		case "number", "string", "bool", "date", "formula":
		default:
			return Table{}, fmt.Errorf("unknown column type %q", c.Type)
		}
		if c.Formula != "" {
			if _, e := s.expr.Compile(c.Formula); e != nil {
				return Table{}, fmt.Errorf("formula %s: %w", c.ID, e)
			}
		} else if c.Type == "formula" {
			return Table{}, fmt.Errorf("formula column requires an expression")
		}
	}
	t := Table{Key: p.UserID + ":" + id, UserID: p.UserID, ID: id, Name: in.Name, Columns: in.Columns}
	_, e := s.audit.Write(ctx, p, "db_create", audit.Target{Collection: "tables", ID: id}, "", nil, t, func(tx context.Context) error {
		_, e := s.repo.db.Collection("tables").InsertOne(tx, t)
		if mongo.IsDuplicateKeyError(e) {
			return principal.ErrConflict
		}
		return e
	})
	return t, e
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
func (s *Service) Query(ctx context.Context, p principal.Principal, in QueryInput) (QueryResult, error) {
	if e := p.Require("ledger:read"); e != nil {
		return QueryResult{}, e
	}
	if in.Limit == 0 {
		in.Limit = 5000
	}
	if in.Limit < 1 || in.Limit > 5000 || len(in.Sort) > 10 {
		return QueryResult{}, fmt.Errorf("limit must be 1..5000; at most 10 sort fields")
	}
	t, e := s.Get(ctx, p, in.DB)
	if e != nil {
		return QueryResult{}, e
	}
	cur, e := s.repo.db.Collection("rows").Find(ctx, bson.M{"user_id": p.UserID, "table_id": in.DB, "archived": bson.M{"$ne": true}}, options.Find().SetSort(bson.D{{Key: "id", Value: 1}}).SetLimit(5001))
	if e != nil {
		return QueryResult{}, e
	}
	defer cur.Close(ctx)
	rows := []Row{}
	bytes := 0
	for cur.Next(ctx) {
		bytes += len(cur.Current)
		if bytes > 8*1024*1024 {
			return QueryResult{}, fmt.Errorf("database query exceeds 8 MiB; reduce table data")
		}
		var row Row
		if e = cur.Decode(&row); e != nil {
			return QueryResult{}, e
		}
		rows = append(rows, row)
	}
	if e = cur.Err(); e != nil {
		return QueryResult{}, e
	}
	if len(rows) > 5000 {
		return QueryResult{}, fmt.Errorf("database exceeds the 5000-row expression budget")
	}
	out := QueryResult{Rows: []Row{}, Columns: t.Columns}
	for _, r := range rows {
		if in.Where != "" {
			result, e := s.expr.Eval(ctx, in.Where, map[string]any{"row": r.Cells})
			if e != nil {
				return out, e
			}
			ok, isBool := result.Value.(bool)
			if !isBool {
				return out, fmt.Errorf("where must return boolean")
			}
			if !ok {
				continue
			}
		}
		out.Rows = append(out.Rows, r)
	}
	for i := len(in.Sort) - 1; i >= 0; i-- {
		key := strings.TrimPrefix(strings.TrimPrefix(in.Sort[i], "-"), "+")
		if !fieldID.MatchString(key) {
			return QueryResult{}, fmt.Errorf("invalid sort field")
		}
		desc := strings.HasPrefix(in.Sort[i], "-")
		sort.SliceStable(out.Rows, func(a, b int) bool {
			x, y := out.Rows[a].Cells[key], out.Rows[b].Cells[key]
			less := fmt.Sprint(x) < fmt.Sprint(y)
			if n, ok := asNumber(x); ok {
				if m, ok := asNumber(y); ok {
					less = n < m
				}
			}
			if desc {
				return x != nil && fmt.Sprint(x) != fmt.Sprint(y) && !less
			}
			return less
		})
	}
	if len(out.Rows) > in.Limit {
		out.Rows = out.Rows[:in.Limit]
	}
	return out, nil
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
		return true
	}
	switch c.Type {
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
			delete(pending, id)
			progress = true
		}
		if !progress {
			return fmt.Errorf("formula dependency cycle or invalid reference: %w", last)
		}
	}
	return nil
}
func (s *Service) Update(ctx context.Context, p principal.Principal, in UpdateInput) (UpdateResult, error) {
	if e := p.Require("model:write"); e != nil {
		return UpdateResult{}, e
	}
	if len(in.Updates) < 1 || len(in.Updates) > 500 {
		return UpdateResult{}, fmt.Errorf("1..500 cell updates required")
	}
	table, e := s.Get(ctx, p, in.DB)
	if e != nil {
		return UpdateResult{}, e
	}
	cols := map[string]Column{}
	for _, c := range table.Columns {
		cols[c.ID] = c
	}
	for _, u := range in.Updates {
		c, ok := cols[u.Column]
		if !ok || c.Formula != "" || !validValue(c, u.Value) || len(u.RowID) == 0 || len(u.RowID) > 100 {
			return UpdateResult{}, fmt.Errorf("invalid row, column, value or attempted formula edit")
		}
	}
	result := UpdateResult{Rows: []Row{}, DependentsRecomputed: []string{}}
	before := []Row{}
	id, e := s.audit.Write(ctx, p, "db_update_cells", audit.Target{Collection: "tables", ID: in.DB}, "", &before, &result.Rows, func(tx context.Context) error {
		result.Rows = []Row{}
		before = []Row{}
		rows := map[string]*Row{}
		for _, u := range in.Updates {
			r := rows[u.RowID]
			if r == nil {
				r = &Row{}
				e := s.repo.db.Collection("rows").FindOne(tx, bson.M{"user_id": p.UserID, "table_id": in.DB, "id": u.RowID}).Decode(r)
				if e != nil && !errors.Is(e, mongo.ErrNoDocuments) {
					return e
				}
				if errors.Is(e, mongo.ErrNoDocuments) {
					*r = Row{Key: p.UserID + ":" + in.DB + ":" + u.RowID, UserID: p.UserID, TableID: in.DB, ID: u.RowID, Cells: map[string]any{}}
				}
				snapshot := *r
				snapshot.Cells = map[string]any{}
				for k, v := range r.Cells {
					snapshot.Cells[k] = v
				}
				before = append(before, snapshot)
				rows[u.RowID] = r
			}
			if u.Version != nil && *u.Version != r.Version {
				return principal.ErrConflict
			}
			r.Cells[u.Column] = u.Value
		}
		for _, r := range rows {
			if e := s.formulas(tx, table.Columns, r.Cells); e != nil {
				return e
			}
			r.Version++
			r.Archived = false
			_, e := s.repo.db.Collection("rows").ReplaceOne(tx, bson.M{"user_id": p.UserID, "table_id": in.DB, "id": r.ID}, r, options.Replace().SetUpsert(true))
			if e != nil {
				return e
			}
			result.Rows = append(result.Rows, *r)
		}
		return nil
	})
	result.EventID = id
	for _, c := range table.Columns {
		if c.Formula != "" {
			result.DependentsRecomputed = append(result.DependentsRecomputed, c.ID)
		}
	}
	return result, e
}
