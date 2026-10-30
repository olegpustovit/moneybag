package semantic

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

// The contract promises under 150 KB gzipped on 10k entries; this measures flows, the part that grows with the ledger.
func TestCubeFlowsStayUnderTheSizeBudget(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	b := &book{accounts: map[string]ledger.Account{}, labels: map[string]string{}, rateMemo: map[string]*big.Rat{}}
	ids := []string{"assets:checking", "assets:savings", "assets:wallet"}
	for i := range 80 {
		ids = append(ids, fmt.Sprintf("expenses:category_%d", i))
	}
	for _, id := range ids {
		b.accounts[id] = ledger.Account{ID: id, Name: humanize(id), Type: map[bool]string{true: "asset", false: "expense"}[id[:6] == "assets"], Commodities: []string{"EUR"}}
	}
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	tags := [][]string{nil, nil, nil, {"trip:lisbon"}, {"trip:oslo"}, {"work"}}
	for n := range 10000 {
		date := start.AddDate(0, 0, n*1400/10000).Format(day)
		payee := fmt.Sprintf("Synthetic payee %d", rng.IntN(800))
		amount := ledger.MustDecimal(fmt.Sprintf("%d.%02d", rng.IntN(300), rng.IntN(100)))
		expense, asset := ids[3+rng.IntN(80)], ids[rng.IntN(3)]
		tag := tags[rng.IntN(len(tags))]
		b.rows = append(b.rows, row{date: date, payee: payee, name: payee, account: expense, commodity: "EUR", amount: amount, tags: tag, acct: b.accounts[expense]}, row{date: date, payee: payee, name: payee, account: asset, commodity: "EUR", amount: amount.Negate(), tags: tag, acct: b.accounts[asset]})
	}
	b.first = b.rows[0].date
	out := Cube{Start: b.first, Accounts: []CubeAccount{}, Payees: []string{}, TagSets: [][]string{}}
	offset := func(date string) int {
		d, _ := time.Parse(day, date)
		return int(d.Sub(start).Hours() / 24)
	}
	s := &Service{}
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read"}}
	if e := s.cubeFlows(context.Background(), p, b, &out, offset); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(out)
	if e != nil {
		t.Fatal(e)
	}
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	if _, e = w.Write(raw); e != nil {
		t.Fatal(e)
	}
	w.Close()
	t.Logf("%d rows, %d bytes raw, %d bytes gzipped", len(out.Flows.D), len(raw), compressed.Len())
	if compressed.Len() >= 150*1024 {
		t.Fatalf("cube is %d bytes gzipped", compressed.Len())
	}
	if len(out.Flows.D) != len(out.Flows.V) || len(out.Accounts) != len(ids) || out.Unvalued != 0 {
		t.Fatalf("columns disagree: %+v", out.Unvalued)
	}
	// Each entry balances, so the rows must net to zero in cents.
	var total int64
	for _, v := range out.Flows.V {
		total += v
	}
	if total != 0 {
		t.Fatal("flows do not balance", total)
	}
}

// Same fixture as the months checks in ui/tests/cube.test.ts: both sides must agree.
func TestMonthsBetweenCountsDaysOverMonthLength(t *testing.T) {
	for _, c := range []struct {
		from, to string
		want     *big.Rat
	}{
		{"2026-08-02", "2026-10-30", new(big.Rat).Add(big.NewRat(1, 1), big.NewRat(60, 31))},
		{"2026-05-04", "2026-08-01", new(big.Rat).Add(big.NewRat(1, 1), big.NewRat(60, 31))},
		{"2026-07-01", "2026-09-30", big.NewRat(3, 1)},
	} {
		if got := monthsBetween(c.from, c.to); got.Cmp(c.want) != 0 {
			t.Errorf("%s..%s = %s, want %s", c.from, c.to, got.RatString(), c.want.RatString())
		}
	}
}

func TestTidyPayee(t *testing.T) {
	for raw, want := range map[string]string{
		"Home and repair, CROATIA, terminal 50121946, terminal address -, document No. X": "Home and repair",
		"MUELLER TRG BURZE 5059, ZAGREB":     "Mueller Trg Burze",
		"KONZUM P-1232, ZAGREB":              "Konzum",
		"X CORP. PAID FEATURES, 865 FM 1209": "X Corp. Paid Features",
		"Wolt":                               "Wolt",
		"12345":                              "12345",
	} {
		if got := tidyPayee(raw); got != want {
			t.Errorf("tidyPayee(%q) = %q, want %q", raw, got, want)
		}
	}
}
