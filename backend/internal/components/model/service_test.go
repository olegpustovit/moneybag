package model

import (
	"testing"
	"time"
)

func TestProjectionPortAndEvents(t *testing.T) {
	a := map[string]float64{"start_net_usd": 10000, "income_usd": 2000, "spend_eur": 1000, "eurusd": 1, "wise_usd": 2000, "kyiv_usd": 500, "gold_g": 10, "gold_usd_g": 100}
	sc := Scenario{ID: "baseline", Name: "Baseline", Legacy: &LegacyInputs{Spend: []SpendLine{{USD: 1000}}, SalaryBiweeklyUSD: 1200, ReimbursementBiweeklyUSD: 120, BufferUSD: 2000, YieldPct: 12}}
	r, e := Project(sc, a, nil, 3, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if e != nil {
		t.Fatal(e)
	}
	if r.Months[1].Kyiv != 240 || r.Months[1].Wise != 3878.6 || r.Months[2].Kyiv != 0 || r.KyivEmptyMonth != 2 {
		t.Fatalf("port differs: %+v", r)
	}
	if r.Months[0].Net != 10000 {
		t.Fatal("opening net changed")
	}
	sc.Legacy = nil
	sc.Events = []Event{{Month: 2, Amount: -500}}
	a["gold_shock_pct"] = -20
	r, e = Project(sc, a, nil, 2, time.Now())
	if e != nil || r.EndNetUSD != 11300 {
		t.Fatal(r, e)
	}
}

func TestLegacyExplicitOverridesAndIncomeStreams(t *testing.T) {
	sc := Scenario{ID: "legacy", Legacy: &LegacyInputs{SalaryBiweeklyUSD: 120, Spend: []SpendLine{{USD: 100}}}, Overrides: []Override{{Assumption: "income_usd", Value: ptr(50)}, {Assumption: "spend_eur", Value: ptr(10)}}}
	a := map[string]float64{"start_net_usd": 1000, "wise_usd": 1000, "eurusd": 2, "income_usd": 50, "spend_eur": 10}
	out, e := Project(sc, a, []Income{{ID: "salary", Amount: 999, Currency: "USD", Frequency: "monthly"}}, 1, time.Now())
	if e != nil || out.SpendUSD != 20 || out.EndNetUSD != 1030 {
		t.Fatal("legacy overrides ignored", out, e)
	}
	sc.Overrides = nil
	out, e = Project(sc, a, []Income{{ID: "salary", Amount: 200, Currency: "USD", Frequency: "monthly"}}, 1, time.Now())
	if e != nil || out.EndNetUSD != 1100 {
		t.Fatal("legacy stream update ignored", out, e)
	}
}

func TestScenarioEventUsesSelectedCashPot(t *testing.T) {
	sc := Scenario{ID: "base", Events: []Event{{Month: 1, Amount: 200, Account: "kyiv"}}}
	a := map[string]float64{"start_net_usd": 1000, "wise_usd": 900, "kyiv_usd": 100, "eurusd": 1}
	out, e := Project(sc, a, nil, 1, time.Now())
	if e != nil || out.Months[1].Kyiv != 300 || out.Months[1].Wise != 900 || out.EndNet != 1200 || out.Currency != "USD" {
		t.Fatal(out, e)
	}
}
