package model

import (
	"context"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Metrics lists the valid scenario_compare metric names; cash, gold, wise and kyiv are month-12 values.
var Metrics = []string{"spendUsd", "endNetUsd", "runway", "cash", "gold", "wise", "kyiv", "savings_usd_mo", "kyiv_empty_month", "runway_ex_gold"}

type CompareInput struct {
	ScenarioIDs []string `json:"scenario_ids" jsonschema:"1..20 scenario ids"`
	Metrics     []string `json:"metrics,omitempty" jsonschema:"metric names; default spendUsd, endNetUsd, runway"`
	AsOf        string   `json:"as_of,omitempty" jsonschema:"start date YYYY-MM-DD for the live ledger start; default today"`
}
type CompareResult struct {
	Table []map[string]any `json:"table"`
}

func (s *Service) Compare(ctx context.Context, p principal.Principal, in CompareInput) (CompareResult, error) {
	if len(in.Metrics) == 0 {
		in.Metrics = []string{"spendUsd", "endNetUsd", "runway"}
	}
	for _, m := range in.Metrics {
		if !slices.Contains(Metrics, m) {
			return CompareResult{}, fmt.Errorf("unknown metric %s; valid metrics: %s", m, strings.Join(Metrics, ", "))
		}
	}
	series, e := s.Forecast(ctx, p, ForecastInput{ScenarioIDs: in.ScenarioIDs, HorizonMonths: 12, AsOf: in.AsOf})
	if e != nil {
		return CompareResult{}, e
	}
	out := CompareResult{Table: []map[string]any{}}
	for _, r := range series {
		row := map[string]any{"id": r.Scenario, "name": r.Name}
		last := r.Months[len(r.Months)-1]
		for _, m := range in.Metrics {
			switch m {
			case "spendUsd":
				row[m] = r.SpendUSD
			case "endNetUsd":
				row[m] = r.EndNetUSD
			case "runway":
				row[m] = r.Runway
			case "cash":
				row[m] = last.Cash
			case "gold":
				row[m] = last.Gold
			case "wise":
				row[m] = last.Wise
			case "kyiv":
				row[m] = last.Kyiv
			case "savings_usd_mo":
				row[m] = r.SavingsUSDMo
			case "kyiv_empty_month":
				row[m] = r.KyivEmptyMonth
			case "runway_ex_gold":
				row[m] = r.RunwayExGold
			}
		}
		out.Table = append(out.Table, row)
	}
	return out, nil
}

type Shock struct {
	Commodity string  `json:"commodity"`
	Pct       float64 `json:"pct"`
}
type ShockInput struct {
	Shocks     []Shock `json:"shocks"`
	ScenarioID string  `json:"scenario_id,omitempty"`
}
type PotShock struct {
	Pot   string  `json:"pot"`
	Delta float64 `json:"delta"`
}
type ShockResult struct {
	DeltaNet      ledger.Amount `json:"delta_net"`
	ByPot         []PotShock    `json:"by_pot"`
	ScenarioID    string        `json:"scenario_id,omitempty"`
	ShockedEndNet *float64      `json:"shocked_end_net_usd,omitempty"`
}

func (s *Service) Shock(ctx context.Context, p principal.Principal, in ShockInput, book *ledger.Service, valuer ledger.Valuer) (ShockResult, error) {
	if e := p.Require("ledger:read"); e != nil {
		return ShockResult{}, e
	}
	if len(in.Shocks) < 1 || len(in.Shocks) > 20 {
		return ShockResult{}, fmt.Errorf("1..20 shocks required")
	}
	shocks := map[string]float64{}
	for _, shock := range in.Shocks {
		if shock.Commodity == "" || shock.Pct < -100 || math.IsInf(shock.Pct, 0) || math.IsNaN(shock.Pct) {
			return ShockResult{}, fmt.Errorf("finite shock percentage at least -100 required")
		}
		if _, ok := shocks[shock.Commodity]; ok {
			return ShockResult{}, fmt.Errorf("commodity specified twice")
		}
		shocks[shock.Commodity] = shock.Pct
	}
	balances, e := book.Balances(ctx, p, ledger.BalancesInput{}, nil)
	if e != nil {
		return ShockResult{}, e
	}
	byPot := map[string]float64{}
	total := 0.0
	for _, row := range balances.Rows {
		account, e := book.Account(ctx, p, row.Key)
		if e != nil {
			return ShockResult{}, e
		}
		if account.Type != "asset" && account.Type != "liability" {
			continue
		}
		pot := account.Pot
		if pot == "" {
			pot = account.ID
		}
		for _, a := range row.Amounts {
			pct, ok := shocks[a.Commodity]
			if !ok {
				continue
			}
			v, e := valuer.Convert(ctx, p, a, "USD", balances.AsOf+"T23:59:59Z")
			if e != nil {
				return ShockResult{}, e
			}
			delta := v.Value.Float() * pct / 100
			byPot[pot] += delta
			total += delta
		}
	}
	value, e := ledger.ParseDecimal(strconv.FormatFloat(total, 'f', 2, 64))
	if e != nil {
		return ShockResult{}, e
	}
	out := ShockResult{DeltaNet: ledger.Amount{Value: value, Commodity: "USD"}, ByPot: []PotShock{}, ScenarioID: in.ScenarioID}
	for pot, delta := range byPot {
		out.ByPot = append(out.ByPot, PotShock{Pot: pot, Delta: round(delta)})
	}
	if in.ScenarioID != "" {
		series, e := s.Forecast(ctx, p, ForecastInput{ScenarioIDs: []string{in.ScenarioID}, HorizonMonths: 12})
		if e != nil {
			return ShockResult{}, e
		}
		out.ShockedEndNet = ptr(round(series[0].EndNetUSD + total))
	}
	return out, nil
}
