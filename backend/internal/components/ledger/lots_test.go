package ledger

import "testing"

func TestCostLotsRetainCostsAndReversals(t *testing.T) {
	post := func(q, c string) Posting {
		p := Posting{Account: "assets:gold", Amount: Amount{Value: MustDecimal(q), Commodity: "XAU_g"}}
		if c != "" {
			p.Cost = &Amount{Value: MustDecimal(c), Commodity: "USD"}
		}
		return p
	}
	entries := []Entry{{Postings: []Posting{post("10", "60"), post("5", "70"), post("2", "")}}, {Postings: []Posting{post("-3", "60.00"), post("-1", "70")}}, {Postings: []Posting{post("1", "70")}}}
	lots, err := costLots(entries)
	if err != nil || len(lots) != 3 {
		t.Fatal(lots, err)
	}
	var total string
	for _, lot := range lots {
		if lot.UnitCost == nil {
			if lot.CostBasis != nil || lot.Amount.Value.String() != "2" {
				t.Fatal(lot)
			}
			continue
		}
		if lot.UnitCost.Value.Rat().Cmp(MustDecimal("60").Rat()) == 0 {
			total = lot.CostBasis.Value.String()
			if lot.Amount.Value.String() != "7" {
				t.Fatal(lot)
			}
		}
	}
	if total != "420" {
		t.Fatal("cost basis lost", total)
	}
}
