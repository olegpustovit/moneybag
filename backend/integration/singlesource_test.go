package integration

import (
	"context"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

// testSingleSource covers account classes/groups, the effective journal, postings, bulk tags and budget windows.
func testSingleSource(t *testing.T, ctx context.Context, p principal.Principal, book *ledger.Service, budgets *budget.Service, sources *dashboard.Sources) {
	t.Helper()
	if _, e := sources.Rates.Set(ctx, p, rates.SetInput{Pair: "EUR/USD", Value: ledger.MustDecimal("1.1"), At: "2025-01-01T00:00:00Z", Source: "Synthetic quote"}); e != nil {
		t.Fatal(e)
	}
	for _, q := range []rates.SetInput{{Pair: "EUR/USD", Value: ledger.MustDecimal("1.2"), At: "2025-02-01T00:00:00Z", Source: "Synthetic quote"}, {Pair: "GBP/CHF", Value: ledger.MustDecimal("2"), At: "2025-01-05T00:00:00Z", Source: "Synthetic quote"}} {
		if _, e := sources.Rates.Set(ctx, p, q); e != nil {
			t.Fatal(e)
		}
	}
	if h, e := sources.Rows(ctx, p, "rate_history:EUR/USD"); e != nil || len(h) != 2 || h[0]["at"].(string) > h[1]["at"].(string) || h[1]["value"] != 1.2 {
		t.Fatal("rate history", h, e)
	}
	if h, e := sources.Rows(ctx, p, "rate_history:CHF/GBP"); e != nil || len(h) != 1 || h[0]["value"] != 0.5 {
		t.Fatal("inverse rate history", h, e)
	}
	food := map[string]string{"category": "Food"}
	for _, a := range []ledger.Account{
		{ID: "assets:ws2bank", Type: "asset", Name: "Bank", Class: "bank", Commodities: []string{"EUR"}},
		{ID: "expenses:ws2a", Type: "expense", Name: "A", Class: "expense", Groups: food, Commodities: []string{"EUR"}},
		{ID: "expenses:ws2b", Type: "expense", Name: "B", Class: "expense", Groups: food, Commodities: []string{"EUR"}},
		{ID: "expenses:ws2c", Type: "expense", Name: "C", Class: "expense", Groups: map[string]string{"category": "Other"}, Commodities: []string{"EUR"}},
	} {
		if _, e := book.CreateAccount(ctx, p, a); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := book.CreateAccount(ctx, p, ledger.Account{ID: "expenses:ws2bad", Type: "expense", Name: "X", Class: "nonsense", Commodities: []string{"EUR"}}); e == nil {
		t.Fatal("invalid class accepted")
	}
	post := func(date, payee, account, amount string) ledger.Entry {
		r, e := book.Post(ctx, p, ledger.EntryInput{Date: date, Payee: payee, Commit: true, Postings: []ledger.Posting{
			{Account: account, Amount: ledger.Amount{Value: ledger.MustDecimal(amount), Commodity: "EUR"}},
			{Account: "assets:ws2bank", Amount: ledger.Amount{Value: ledger.MustDecimal("-" + amount), Commodity: "EUR"}}}})
		if e != nil {
			t.Fatal(e)
		}
		return r.Entry
	}
	first := post("2025-03-05", "WS2 A", "expenses:ws2a", "10")
	post("2025-03-06", "WS2 B", "expenses:ws2b", "5")
	post("2025-03-07", "WS2 C", "expenses:ws2c", "3")
	_, e := book.Reverse(ctx, p, ledger.ReverseInput{ID: first.ID, Reason: "synthetic fix", Replacement: &ledger.EntryInput{Date: "2025-03-05", Payee: "WS2 A", Commit: true, Postings: []ledger.Posting{
		{Account: "expenses:ws2a", Amount: ledger.Amount{Value: ledger.MustDecimal("12"), Commodity: "EUR"}},
		{Account: "assets:ws2bank", Amount: ledger.Amount{Value: ledger.MustDecimal("-12"), Commodity: "EUR"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	window := ledger.EntriesInput{From: "2025-03-01", To: "2025-03-31"}
	all, _ := book.Entries(ctx, p, window)
	window.Effective = true
	eff, e := book.Entries(ctx, p, window)
	if e != nil || len(all.Items) != 5 || len(eff.Items) != 3 {
		t.Fatal("effective view", len(all.Items), len(eff.Items), e)
	}
	// Undo/redo chains: the original is effective after an even number of reversals and gone after an odd one.
	chain := ledger.EntriesInput{From: "2025-04-01", To: "2025-04-30", Effective: true}
	last := post("2025-04-02", "WS2 D", "expenses:ws2a", "7").ID
	for i, want := range []int{0, 1, 0} {
		r, e := book.Reverse(ctx, p, ledger.ReverseInput{ID: last, Reason: "synthetic undo/redo"})
		if e != nil {
			t.Fatal(e)
		}
		last = r.Reversal.ID
		if got, e := book.Entries(ctx, p, chain); e != nil || len(got.Items) != want {
			t.Fatal("reversal chain", i, len(got.Items), e)
		}
	}

	rows, e := sources.Rows(context.Background(), p, "postings")
	if e != nil {
		t.Fatal(e)
	}
	sum := 0.0
	for _, r := range rows {
		if strings.HasPrefix(r["account"].(string), "expenses:ws2") {
			if r["class"] != "expense" {
				t.Fatal("posting class", r)
			}
			sum += r["value_eur"].(float64)
		}
	}
	if math.Abs(sum-20) > 0.001 {
		t.Fatal("expense postings sum", sum)
	}

	tags := ledger.TagInput{Where: `row.payee == "WS2 B"`, DryRun: true, Add: []string{"trip:x", "flow:out"}}
	if r, e := book.SetTags(ctx, p, tags); e != nil || r.Matched != 1 || r.Updated != 0 {
		t.Fatal("tags dry run", r, e)
	}
	tags.DryRun = false
	if r, e := book.SetTags(ctx, p, tags); e != nil || r.Updated != 1 {
		t.Fatal("tags where", r, e)
	}
	if page, e := book.Entries(ctx, p, ledger.EntriesInput{Tag: "trip:x"}); e != nil || len(page.Items) != 1 {
		t.Fatal("namespaced tag", page, e)
	}
	rows, e = sources.Rows(ctx, p, "postings")
	if e != nil {
		t.Fatal(e)
	}
	tagged := 0
	for _, row := range rows {
		if row["payee"] == "WS2 B" {
			for _, tag := range row["tags"].([]any) {
				if tag == "trip:x" {
					tagged++
				}
			}
		}
	}
	if tagged != 2 {
		t.Fatalf("tag overlay on both postings: got %d", tagged)
	}

	ref := &budget.GroupRef{Dimension: "category", Value: "Food"}
	if _, e = budgets.Set(ctx, p, budget.SetInput{ID: "ws2food", GroupRef: ref}); e != nil {
		t.Fatal(e)
	}
	status, e := budgets.GetWindow(ctx, p, "2025-03", budget.Window{Months: 2, IncludeCurrent: true})
	if e != nil || status.Window.Label != "Feb–Mar" || status.Window.From != "2025-02-01" || status.Window.To != "2025-03-31" {
		t.Fatal("budget window", status.Window, e)
	}
	for _, l := range status.Lines {
		if l.ID == "ws2food" && (math.Abs(l.Actual-17) > 0.001 || !slices.Contains(l.Accounts, "expenses:ws2b")) {
			t.Fatal("group_ref line", l)
		}
	}
	if !slices.Contains(status.Unmapped, "expenses:ws2c") || slices.Contains(status.Unmapped, "expenses:ws2a") {
		t.Fatal("unmapped", status.Unmapped)
	}
	if status, e = budgets.Get(ctx, p, "2025-03"); e != nil || status.Window.Label != "Sep–Feb" {
		t.Fatal("default window", status.Window, e)
	}
}
