package model

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

func TestTrackerProjectionParity(t *testing.T) {
	path := os.Getenv("MONEYBAG_TRACKER_FIXTURE")
	if path == "" {
		t.Skip("set MONEYBAG_TRACKER_FIXTURE to the private tracker JSON; never commit financial fixtures")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var data struct {
		NetUSD    float64 `json:"netUsd"`
		LiveUSD   float64 `json:"liveUsd"`
		KyivUSD   float64 `json:"kyivUsdCash"`
		GoldGrams float64 `json:"goldGrams"`
		GoldPrice float64 `json:"goldUsdPerGram"`
		Scenarios []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Months int    `json:"months"`
			LegacyInputs
		}
		Results []struct {
			ID             string  `json:"id"`
			EndNetUSD      float64 `json:"endNetUsd"`
			EndWiseUSD     float64 `json:"endWiseUsd"`
			EndKyivUSD     float64 `json:"endKyivUsd"`
			KyivEmptyMonth int     `json:"kyivEmptyMonth"`
		}
	}
	if e = json.Unmarshal(b, &data); e != nil {
		t.Fatal(e)
	}
	if len(data.Scenarios) != 8 || len(data.Results) != 8 {
		t.Fatal("expected all eight tracker scenarios")
	}
	a := map[string]float64{"start_net_usd": data.NetUSD, "wise_usd": data.LiveUSD, "kyiv_usd": data.KyivUSD, "gold_g": data.GoldGrams, "gold_usd_g": data.GoldPrice}
	for _, sc := range data.Scenarios {
		r, e := Project(Scenario{ID: sc.ID, Name: sc.Name, Legacy: &sc.LegacyInputs}, a, nil, sc.Months, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, expected := range data.Results {
			if expected.ID == sc.ID {
				found = true
				last := r.Months[len(r.Months)-1]
				if math.Abs(r.EndNetUSD-expected.EndNetUSD) > 0.011 || math.Abs(last.Wise-expected.EndWiseUSD) > 0.011 || math.Abs(last.Kyiv-expected.EndKyivUSD) > 0.011 || r.KyivEmptyMonth != expected.KyivEmptyMonth {
					t.Errorf("scenario %s differs from tracker", sc.ID)
				}
			}
		}
		if !found {
			t.Fatalf("scenario %s has no expected result", sc.ID)
		}
	}
}
