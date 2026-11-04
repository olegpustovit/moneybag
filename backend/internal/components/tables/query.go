package tables

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (s *Service) Query(ctx context.Context, p principal.Principal, in QueryInput) (QueryResult, error) {
	return s.query(ctx, p, in, false)
}

func (s *Service) queryFilter(table Table, in QueryInput, p principal.Principal) (bson.M, error) {
	filter := bson.M{"user_id": p.UserID, "table_id": table.ID, "archived": bson.M{"$ne": true}}
	if in.Cursor != "" {
		filter["id"] = bson.M{"$gt": in.Cursor}
	}
	if in.Where == "" {
		return filter, nil
	}
	indexed := map[string]Column{}
	keys := map[string]bool{}
	for _, key := range table.Keys {
		for _, id := range key {
			keys[id] = true
		}
	}
	for _, column := range table.Columns {
		if keys[column.ID] || column.Type == "entry" || column.Type == "ref" || column.Type == "account" {
			indexed[column.ID] = column
		}
	}
	predicates, err := s.expr.RequiredPredicates(in.Where)
	if err != nil {
		return nil, err
	}
	for _, predicate := range predicates {
		field := predicate.Field
		if in.JoinEntry && field == "entry" {
			field = entryColumn(table)
		}
		column, ok := indexed[field]
		if !ok || column.Type == "formula" || column.Type == "json" || column.Type == "list" {
			continue
		}
		values := slices.Clone(predicate.Values)
		valid := true
		if column.Type == "decimal" {
			for i, value := range values {
				var text string
				switch number := value.(type) {
				case int64:
					text = strconv.FormatInt(number, 10)
				case float64:
					text = strconv.FormatFloat(number, 'g', -1, 64)
				default:
					valid = false
				}
				rat, ok := new(big.Rat).SetString(text)
				if !ok {
					valid = false
					break
				}
				decimal, err := ledger.DecimalFromRat(rat)
				if err != nil {
					valid = false
					break
				}
				values[i] = decimal.Decimal128
			}
		}
		if valid {
			filter["cells."+field] = bson.M{"$in": values}
		}
	}
	return filter, nil
}

func (s *Service) query(ctx context.Context, p principal.Principal, in QueryInput, source bool) (QueryResult, error) {
	ctx = reqcache.With(ctx)
	if err := p.Require("ledger:read"); err != nil {
		return QueryResult{}, err
	}
	if in.Limit == 0 {
		in.Limit = 5000
	}
	if source {
		in.Limit = 250000
	}
	if in.Limit < 1 || (!source && in.Limit > 5000) || len(in.Sort) > 10 || len(in.Cursor) > 100 || in.Cursor != "" && len(in.Sort) > 0 {
		return QueryResult{}, fmt.Errorf("limit must be 1..5000; cursor uses row-id order without custom sort")
	}
	for _, key := range in.Sort {
		if !fieldID.MatchString(strings.TrimPrefix(strings.TrimPrefix(key, "-"), "+")) {
			return QueryResult{}, fmt.Errorf("invalid sort field")
		}
	}
	table, err := s.Get(ctx, p, in.DB)
	if err != nil {
		return QueryResult{}, err
	}
	if in.JoinEntry {
		column := entryColumn(table)
		if column == "" {
			return QueryResult{}, fmt.Errorf("join_entry requires an entry column")
		}
		for _, c := range table.Columns {
			if c.ID == "entry" && column != "entry" {
				return QueryResult{}, fmt.Errorf("join_entry alias entry conflicts with the existing entry column; linked column is %s", column)
			}
		}
	}
	for _, id := range in.Expand {
		found := false
		for _, column := range table.Columns {
			if column.ID == id && column.Type == "ref" {
				found = true
			}
		}
		if !found {
			return QueryResult{}, fmt.Errorf("expand column %s must have type ref", id)
		}
	}
	filter, err := s.queryFilter(table, in, p)
	if err != nil {
		return QueryResult{}, err
	}
	program, err := s.expr.Compile("true")
	if in.Where != "" {
		program, err = s.expr.Compile(in.Where)
	}
	if err != nil {
		return QueryResult{}, err
	}
	cursor, err := s.repo.db.Collection("rows").Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "id", Value: 1}}).SetBatchSize(250))
	if err != nil {
		return QueryResult{}, err
	}
	defer cursor.Close(ctx)
	out := QueryResult{Rows: []Row{}, Columns: table.Columns}
	bytes, scanned, outputBytes := 0, 0, 0
	byteLimit := 8 * 1024 * 1024
	if source {
		byteLimit = 64 * 1024 * 1024
	}
	page := []Row{}
	flush := func() error {
		if len(page) == 0 {
			return nil
		}
		if err := s.enrich(ctx, p, table, page, in.JoinEntry, in.Expand); err != nil {
			return err
		}
		for _, row := range page {
			if source && row.joined && (row.Entry == nil || !row.Entry.Effective) {
				continue
			}
			if in.Where != "" {
				result, err := expressions.EvalProgramRow(ctx, in.Where, program, nil, row.SourceCells())
				if err != nil {
					return err
				}
				ok, boolean := result.Value.(bool)
				if !boolean {
					return fmt.Errorf("where must return boolean")
				}
				if !ok {
					continue
				}
			}
			if in.JoinEntry || len(in.Expand) > 0 {
				encoded, err := json.Marshal(row)
				if err != nil {
					return err
				}
				outputBytes += len(encoded)
				if outputBytes > byteLimit {
					return fmt.Errorf("joined table query exceeds %d MiB output budget; narrow filters or reduce limit", byteLimit/(1024*1024))
				}
			}
			out.Rows = append(out.Rows, row)
			if !source && len(in.Sort) == 0 && len(out.Rows) > in.Limit {
				break
			}
		}
		page = nil
		return nil
	}
	for cursor.Next(ctx) {
		bytes += len(cursor.Current)
		scanned++
		if bytes > byteLimit || scanned > 250000 {
			return QueryResult{}, fmt.Errorf("table query exceeds its 250000-row/%d MiB scan budget; narrow indexed filters", byteLimit/(1024*1024))
		}
		var row Row
		if err = cursor.Decode(&row); err != nil {
			return QueryResult{}, err
		}
		page = append(page, row)
		if len(page) == 250 {
			if err = flush(); err != nil {
				return QueryResult{}, err
			}
			if !source && len(in.Sort) == 0 && len(out.Rows) > in.Limit {
				break
			}
		}
	}
	if err = cursor.Err(); err != nil {
		return QueryResult{}, err
	}
	if err = flush(); err != nil {
		return QueryResult{}, err
	}
	for i := len(in.Sort) - 1; i >= 0; i-- {
		key := strings.TrimPrefix(strings.TrimPrefix(in.Sort[i], "-"), "+")
		desc := strings.HasPrefix(in.Sort[i], "-")
		sort.SliceStable(out.Rows, func(a, b int) bool {
			x, y := out.Rows[a].Cells[key], out.Rows[b].Cells[key]
			if reference, ok := x.(interface{ ReferenceID() string }); ok {
				x = reference.ReferenceID()
			}
			if reference, ok := y.(interface{ ReferenceID() string }); ok {
				y = reference.ReferenceID()
			}
			if dx, ok := x.(ledger.Decimal); ok {
				if dy, ok := y.(ledger.Decimal); ok {
					comparison := dx.Rat().Cmp(dy.Rat())
					if desc {
						return comparison > 0
					}
					return comparison < 0
				}
			}
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
		out.Truncated = true
		if len(in.Sort) == 0 {
			out.NextCursor = out.Rows[len(out.Rows)-1].ID
		}
	}
	return out, nil
}

// SourceRows reads complete bounded analytic sources; it never silently truncates.
func (s *Service) SourceRows(ctx context.Context, p principal.Principal, id string) ([]map[string]any, error) {
	table, err := s.Get(ctx, p, id)
	if err != nil {
		return nil, err
	}
	in := QueryInput{DB: id}
	for _, column := range table.Columns {
		if column.Type == "entry" {
			in.JoinEntry = true
		}
		if column.Type == "ref" {
			in.Expand = append(in.Expand, column.ID)
		}
	}
	result, err := s.query(ctx, p, in, true)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		cells := row.SourceCells()
		cells["_row_id"] = row.ID
		rows = append(rows, cells)
	}
	return rows, nil
}

func (row Row) SourceCells() map[string]any {
	cells := maps.Clone(row.Cells)
	if cells == nil {
		cells = map[string]any{}
	}
	if row.joined {
		cells["entry"] = nil
		if row.Entry != nil {
			cells["entry"] = expressions.Reference(row.Entry.ID, row.Entry.fields())
		}
	}
	return cells
}
