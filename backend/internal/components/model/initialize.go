package model

import (
	"context"
	"errors"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func (s *Service) Initialize(ctx context.Context, p principal.Principal) error {
	if e := p.Require("model:write"); e != nil {
		return e
	}
	if _, e := s.tables.Get(ctx, p, "assumptions"); errors.Is(e, principal.ErrNotFound) {
		if _, e = s.tables.CreateNamed(ctx, p, "assumptions", tables.CreateInput{Name: "Assumptions", Columns: []tables.Column{{ID: "key", Type: "string"}, {ID: "value", Type: "number"}, {ID: "unit", Type: "string"}}}); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	rows, e := s.tables.Query(ctx, p, tables.QueryInput{DB: "assumptions"})
	if e != nil {
		return e
	}
	existing := map[string]bool{}
	for _, row := range rows.Rows {
		existing[row.ID] = true
	}
	defaults := []struct {
		key, unit string
		value     float64
	}{{"start_net_usd", "USD", 0}, {"wise_usd", "USD", 0}, {"kyiv_usd", "USD", 0}, {"income_usd", "USD/month", 0}, {"spend_eur", "EUR/month", 0}, {"eurusd", "USD/EUR", 1}, {"gold_g", "grams", 0}, {"gold_usd_g", "USD/gram", 0}, {"gold_shock_pct", "percent", 0}, {"horizon", "months", 12}}
	updates := []tables.CellUpdate{}
	for _, v := range defaults {
		if existing[v.key] {
			continue
		}
		updates = append(updates, tables.CellUpdate{RowID: v.key, Column: "key", Value: v.key}, tables.CellUpdate{RowID: v.key, Column: "value", Value: v.value}, tables.CellUpdate{RowID: v.key, Column: "unit", Value: v.unit})
	}
	if len(updates) > 0 {
		if _, e = s.tables.Update(ctx, p, tables.UpdateInput{DB: "assumptions", Updates: updates}); e != nil {
			return e
		}
	}
	state, e := s.Get(ctx, p)
	if e != nil {
		return e
	}
	if len(state.Scenarios) == 0 {
		_, e = s.Upsert(ctx, p, Scenario{ID: "base", Name: "Base", Overrides: []Override{}})
	}
	return e
}
