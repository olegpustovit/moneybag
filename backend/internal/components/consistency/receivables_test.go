package consistency

import (
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
)

func receivableEntry(date string, seq int64, amount, currency string) ledger.Entry {
	return ledger.Entry{Date: date, Seq: seq, Postings: []ledger.Posting{{
		Account: "assets:reimbursable",
		Amount:  ledger.Amount{Value: ledger.MustDecimal(amount), Commodity: currency},
	}}}
}

func TestReceivablesKeepCurrenciesSeparate(t *testing.T) {
	entries := []ledger.Entry{
		receivableEntry("2026-10-01", 1, "100", "EUR"),
		receivableEntry("2026-10-02", 2, "-100", "USD"),
	}
	accounts := []ledger.Account{{ID: "assets:reimbursable", Commodities: []string{"EUR", "USD"}}}
	got := receivables(entries, accounts)
	if len(got) != 2 {
		t.Fatalf("opposite EUR and USD amounts must not cancel: %+v", got)
	}
	if got[0].Commodity != "EUR" || got[0].Balance != "100.00" || got[0].Since != "2026-10-01" ||
		got[1].Commodity != "USD" || got[1].Balance != "-100.00" || got[1].Since != "2026-10-02" {
		t.Fatalf("balances and aging must stay in their own currencies: %+v", got)
	}
}

func TestReceivableSettlementUsesSameDayJournalOrder(t *testing.T) {
	// The API returns newest sequence first. A settled bill followed by a new
	// bill on the same day must start a new aging period.
	entries := []ledger.Entry{
		receivableEntry("2026-10-02", 3, "50", "USD"),
		receivableEntry("2026-10-02", 2, "-100", "USD"),
		receivableEntry("2026-10-01", 1, "100", "USD"),
	}
	got := receivables(entries, []ledger.Account{{ID: "assets:reimbursable", Commodities: []string{"USD"}}})
	if len(got) != 1 || got[0].Balance != "50.00" || got[0].Since != "2026-10-02" {
		t.Fatalf("settlement and reopening must follow journal sequence: %+v", got)
	}
}
