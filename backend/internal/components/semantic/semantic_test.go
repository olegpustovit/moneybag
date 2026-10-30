package semantic

import (
	"math/big"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
)

func TestParseRangeCoversTheGrammar(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	cases := map[string][2]string{
		"30d": {"2026-10-01", "2026-10-30"}, "12w": {"2026-08-08", "2026-10-30"}, "12mo": {"2025-10-31", "2026-10-30"}, "2y": {"2024-10-31", "2026-10-30"},
		"ytd": {"2026-01-01", "2026-10-30"}, "qtd": {"2026-10-01", "2026-10-30"}, "mtd": {"2026-10-01", "2026-10-30"}, "today": {"2026-10-30", "2026-10-30"}, "all": {"", "2026-10-30"},
		"this month": {"2026-10-01", "2026-10-30"}, "last month": {"2026-09-01", "2026-09-30"}, "this quarter": {"2026-10-01", "2026-10-30"}, "last quarter": {"2026-07-01", "2026-09-30"}, "this year": {"2026-01-01", "2026-10-30"}, "last year": {"2025-01-01", "2025-12-31"},
		"2026": {"2026-01-01", "2026-12-31"}, "2026-Q2": {"2026-04-01", "2026-06-30"}, "2026-03": {"2026-03-01", "2026-03-31"}, "2026-03-14": {"2026-03-14", "2026-03-14"},
		"march": {"2026-03-01", "2026-03-31"}, "december": {"2025-12-01", "2025-12-31"}, "since march": {"2026-03-01", "2026-10-30"}, "since 2026-03": {"2026-03-01", "2026-10-30"}, "aug to oct": {"2026-08-01", "2026-10-31"}, "nov to feb": {"2025-11-01", "2026-02-28"},
		"2026-01 to 2026-03": {"2026-01-01", "2026-03-31"}, "2026-01-15..2026-02-20": {"2026-01-15", "2026-02-20"},
	}
	for text, want := range cases {
		from, to, e := ParseRange(text, now)
		if e != nil || from != want[0] || to != want[1] {
			t.Errorf("%s: %s..%s %v, want %v", text, from, to, e, want)
		}
	}
	for _, example := range TimeGrammar {
		if _, _, e := ParseRange(example, now); e != nil {
			t.Errorf("documented form %q does not parse: %v", example, e)
		}
	}
	for _, bad := range []string{"", "soon", "0d", "2026-13", "since whenever", "2026-05 to 2026-01"} {
		if _, _, e := ParseRange(bad, now); e == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestComparePeriod(t *testing.T) {
	from, to, e := ComparePeriod("previous_period", "2026-04-01", "2026-06-30")
	if e != nil || from != "2025-12-31" || to != "2026-03-31" {
		t.Fatal(from, to, e)
	}
	from, to, e = ComparePeriod("previous_year", "2026-04-01", "2026-06-30")
	if e != nil || from != "2025-04-01" || to != "2025-06-30" {
		t.Fatal(from, to, e)
	}
	if from, _, e = ComparePeriod("none", "2026-04-01", "2026-06-30"); e != nil || from != "" {
		t.Fatal(from, e)
	}
	if _, _, e = ComparePeriod("sideways", "2026-04-01", "2026-06-30"); e == nil {
		t.Fatal("bad compare accepted")
	}
}

func TestFormatValueNeverLeaksFloatNoise(t *testing.T) {
	cases := []struct {
		unit, value, want string
	}{{"eur", "1234.5", "€1,234.50"}, {"usd", "-0.5", "−$0.50"}, {"eur", "-0.001", "€0.00"}, {"pct", "12.34", "12.3%"}, {"mo", "3.25", "3.3 mo"}, {"days", "1", "1 day"}, {"days", "25", "25 days"}, {"int", "1234", "1,234"}}
	for _, c := range cases {
		v, _ := new(big.Rat).SetString(c.value)
		if got := FormatValue(c.unit, v); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.unit, c.value, got, c.want)
		}
	}
}

func TestHumanizeKeepsRawKeysOffTheScreen(t *testing.T) {
	for in, want := range map[string]string{"cash_home": "Cash home", "est_hist": "Est hist", "Groceries": "Groceries", "Air Transat": "Air Transat", "lisbon": "Lisbon"} {
		if got := humanize(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}

func TestBucketsAndPolarity(t *testing.T) {
	if got := bucketOf("2026-03-14", "week"); got != "2026-03-09" {
		t.Fatal(got)
	}
	if got := bucketOf("2026-03-15", "week"); got != "2026-03-09" {
		t.Fatal(got)
	}
	ends := bucketEnds("2026-01-15", "2026-03-10", "month")
	if len(ends) != 3 || ends[0] != "2026-01-31" || ends[2] != "2026-03-10" {
		t.Fatal(ends)
	}
	if !lowerIsGood(Metric{Accounts: []string{"expenses:"}, Sign: 1}) || lowerIsGood(Metric{Accounts: []string{"income:"}, Sign: -1}) || lowerIsGood(Metric{Accounts: []string{"assets:"}, Sign: 1}) || lowerIsGood(Metric{Accounts: []string{"expenses:"}, Sign: -1}) || !lowerIsGood(Metric{Accounts: []string{"liabilities:"}, Sign: -1}) {
		t.Fatal("polarity")
	}
}

func TestFactsAreDeterministicEnglishWithoutIDs(t *testing.T) {
	share := estimateShare(Metric{Label: "Spending"}, Result{Estimates: EstimateInfo{Policy: "include", SharePct: 12.4, Total: "120", Text: "€120.00"}})
	if share.Sentence != "Estimates make up 12% of spending." {
		t.Fatal(share.Sentence)
	}
	none := estimateShare(Metric{Label: "EUR income"}, Result{Estimates: EstimateInfo{Policy: "include"}})
	if none.Sentence != "None of EUR income is estimated." {
		t.Fatal(none.Sentence)
	}
	if got := longDate("2026-03-12"); got != "12 March 2026" {
		t.Fatal(got)
	}
	if got := wholePercent(0.2); got != "less than 1%" {
		t.Fatal(got)
	}
}

func TestPointsFoldPastTheLargestNames(t *testing.T) {
	a := aggregate{cells: map[cellKey]*big.Rat{}, names: map[string]*big.Rat{}}
	for i := 0; i < maxSeriesName+3; i++ {
		name := string(rune('A' + i))
		v := big.NewRat(int64(100-i), 1)
		a.cells[cellKey{"2026-01", name, false}] = v
		a.names[name] = v
	}
	got, truncated := points(Metric{Unit: "eur", Label: "Spending"}, a, true, maxSeriesRows)
	if truncated || len(got) != maxSeriesName+1 || got[0].Name != "A" || got[len(got)-1].Name != "Other" {
		t.Fatalf("%+v", got)
	}
	if got[len(got)-1].Value != "237" {
		t.Fatal(got[len(got)-1])
	}
	if _, truncated = points(Metric{Unit: "eur"}, a, true, 3); !truncated {
		t.Fatal("row cap not reported")
	}
}

func TestDimensionValuesAndAccountFlags(t *testing.T) {
	b := &book{labels: map[string]string{"tag:trip:lisbon": "Lisbon trip"}, accounts: map[string]ledger.Account{"expenses:food": {ID: "expenses:food", Name: "Food", DisplayName: "Groceries"}}}
	r := &row{account: "expenses:food", acct: b.accounts["expenses:food"], tags: []string{"trip:lisbon", "work"}, name: "Lidl"}
	if got := b.values(r, "trip"); len(got) != 1 || got[0] != "lisbon" {
		t.Fatal(got)
	}
	if got := b.values(r, "category"); got[0] != "Groceries" {
		t.Fatal(got)
	}
	if got := b.display("trip", "lisbon"); got != "Lisbon trip" {
		t.Fatal(got)
	}
	if !b.passes(r, []Filter{{"trip", "in", []string{"lisbon"}}, {"payee", "not_in", []string{"Aldi"}}, {"account", "prefix", []string{"expenses:"}}}) || b.passes(r, []Filter{{"tag", "in", []string{"home"}}}) {
		t.Fatal("filters")
	}
	if (ledger.Account{ID: "expenses:estimated:hist"}).IsEstimated() != true || (ledger.Account{ID: "expenses:estimatedish"}).IsEstimated() || (ledger.Account{ID: "expenses:food"}).IsEstimated() {
		t.Fatal("estimated default")
	}
	off := false
	if (ledger.Account{ID: "expenses:estimated:x", Estimated: &off}).IsEstimated() {
		t.Fatal("explicit flag ignored")
	}
}
