package dashboard

import (
	"context"
	"maps"
	"slices"
	"sort"

	"github.com/olegpustovit/moneybag/backend/internal/components/semantic"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"github.com/tiendc/go-deepcopy"
)

type CatalogSource struct {
	Name   string            `json:"name"`
	Label  string            `json:"label"`
	Fields map[string]string `json:"fields"`
}
type Catalog struct {
	Revision   int64                  `json:"revision"`
	Functions  []expressions.Function `json:"functions"`
	Aggregates []expressions.Function `json:"aggregates"`
	Variables  []string               `json:"variables"`
	Sources    []CatalogSource        `json:"sources"`
	Components []ComponentDoc         `json:"components"`
	Metrics    []semantic.Metric      `json:"metrics"`
	Dimensions []string               `json:"dimensions"`
	Facts      []string               `json:"facts"`
	TimeRanges []string               `json:"time_ranges"`
	Params     map[string]string      `json:"params"`
	Enums      map[string][]string    `json:"enums"`
	LintRules  []LintRule             `json:"lint_rules"`
}

// Enums are the closed values of the spec; the schema resource and catalog_get both read them.
var Enums = map[string][]string{"mark": Marks, "format": Formats, "tone": Tones, "column_type": ColumnTypes, "estimates": Policies, "compare": Compares, "x_type": {"time", "category"}, "drill_to": {"entries"}, "color_by": {"entity"}, "component_type": ComponentTypes}

var paramsDoc = map[string]string{
	"from":         "Start of the page clock as YYYY-MM-DD, empty for all history. Set by scope.time; a date picker overrides it.",
	"to":           "End of the page clock as YYYY-MM-DD.",
	"compare_from": "Start of the comparison period, empty when there is none. Set by scope.compare.",
	"compare_to":   "End of the comparison period, empty when there is none.",
	"compare":      "The active comparison: previous_period, previous_year or none.",
	"month":        "YYYY-MM selected by a month picker; reads as the months and budget_lines sources' month.",
}

func (s *Service) Catalog(ctx context.Context, p principal.Principal, tableService *tables.Service) (Catalog, error) {
	if err := p.Require("ledger:read"); err != nil {
		return Catalog{}, err
	}
	ctx = reqcache.With(ctx)
	build := func() (any, error) {
		revision, err := s.audit.Revision(ctx, p)
		if err != nil {
			return nil, err
		}
		out := Catalog{Revision: revision, Functions: s.engine.Expr.Functions(), Variables: []string{"row", "data", "params", "computed", "rates"}, Aggregates: []expressions.Function{}, Sources: []CatalogSource{}, Components: ComponentCatalog, Metrics: []semantic.Metric{}, Dimensions: semantic.Dimensions, Facts: semantic.FactKinds, TimeRanges: semantic.TimeGrammar, Params: paramsDoc, Enums: Enums, LintRules: LintRules}
		if s.engine.Metrics != nil {
			if out.Metrics, err = s.engine.Metrics.Metrics(ctx, p); err != nil {
				return nil, err
			}
			if guard, ok := s.engine.Reader.(interface{ AllowsMetric(string) bool }); ok {
				out.Metrics = slices.DeleteFunc(out.Metrics, func(m semantic.Metric) bool { return !guard.AllowsMetric(m.ID) })
			}
		}
		for _, name := range []string{"sum", "avg", "mean", "min", "max", "count", "last", "median", "count_distinct", "percentile", "linear_slope"} {
			args := "expr"
			if name == "count" {
				args = ""
			}
			if name == "percentile" {
				args = "expr, p"
			}
			if name == "linear_slope" {
				args = "x, y"
			}
			out.Aggregates = append(out.Aggregates, expressions.Function{Name: name, Signature: name + "(" + args + ")", Description: "Aggregate " + name + " across the group."})
		}
		for name, fields := range sourceFields {
			out.Sources = append(out.Sources, CatalogSource{Name: name, Label: name, Fields: maps.Clone(fields)})
		}
		ts, err := tableService.List(ctx, p)
		if err != nil {
			return nil, err
		}
		tableFields := map[string]map[string]string{}
		for _, table := range ts {
			fields := map[string]string{"_row_id": "string"}
			for _, col := range table.Columns {
				typ := col.Type
				switch typ {
				case "number":
					typ = "double"
				case "boolean":
					typ = "bool"
				case "formula":
					typ = "dyn"
				}
				fields[col.ID] = typ
			}
			out.Sources = append(out.Sources, CatalogSource{Name: "table:" + table.ID, Label: table.Name, Fields: fields})
			tableFields["table:"+table.ID] = fields
		}
		views, err := s.Views(ctx, p)
		if err != nil {
			return nil, err
		}
		viewQueries := map[string]Query{}
		for _, view := range views {
			viewQueries["view:"+view.ID] = view.Query
		}
		var fieldsFor func(string, map[string]bool) map[string]string
		fieldsFor = func(name string, seen map[string]bool) map[string]string {
			fields := map[string]string{}
			if seen[name] {
				return fields
			}
			if query, ok := viewQueries[name]; ok {
				seen[name] = true
				if len(query.Group) == 0 && len(query.Agg) == 0 {
					maps.Copy(fields, fieldsFor(query.From, seen))
				}
				for _, key := range query.Group {
					fields[key] = "dyn"
				}
				for key := range query.Agg {
					fields[key] = "dyn"
				}
				for key := range query.Derive {
					fields[key] = "dyn"
				}
				delete(seen, name)
			} else if fieldsFromTable, ok := tableFields[name]; ok {
				maps.Copy(fields, fieldsFromTable)
			} else {
				maps.Copy(fields, sourceFields[name])
			}
			return fields
		}
		for _, view := range views {
			fields := fieldsFor("view:"+view.ID, map[string]bool{})
			out.Sources = append(out.Sources, CatalogSource{Name: "view:" + view.ID, Label: view.ID, Fields: fields})
		}
		sort.Slice(out.Sources, func(i, j int) bool { return out.Sources[i].Name < out.Sources[j].Name })
		return out, nil
	}
	if s.snapshots == nil {
		value, err := build()
		if err != nil {
			return Catalog{}, err
		}
		return value.(Catalog), nil
	}
	value, err := s.snapshots.read(ctx, p, "catalog", "expressions", nil, build, nil)
	if err != nil {
		return Catalog{}, err
	}
	var out Catalog
	if err := deepcopy.Copy(&out, value); err != nil {
		return Catalog{}, err
	}
	return out, nil
}
