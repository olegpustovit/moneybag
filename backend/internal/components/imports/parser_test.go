package imports

import (
	"encoding/json"
	"github.com/xuri/excelize/v2"
	"testing"
)

func TestStatements(t *testing.T) {
	for _, tc := range []struct{ format, content, amount string }{{"wise_csv", "Date,Amount,Currency,Description,TransferWise ID\n2026-10-01,-12.34,EUR,\"Coffee, lunch\",ID-1\n", "-12.34"}, {"generic_csv", "Дата;Сума;Валюта;Опис\n01.10.2026;\"-1 234,56\";EUR;Coffee\n", "-1234.56"}} {
		rows, e := Parse([]byte(tc.content), tc.format, Mapping{})
		if e != nil || len(rows) != 1 || rows[0].Amount.String() != tc.amount || rows[0].Date != "2026-10-01" {
			t.Fatalf("parse %s: %+v %v", tc.format, rows, e)
		}
		again, e := Parse([]byte(tc.content), tc.format, Mapping{})
		if e != nil || rows[0].DedupeKey != again[0].DedupeKey {
			t.Fatal("dedupe unstable")
		}
	}
	for _, s := range []string{"Date,Amount,Currency\n2026-10-01,NaN,EUR\n", "Date,Amount\n2026-10-01,2\n", "Date,Amount,Currency\nnot-a-date,2,EUR\n"} {
		if _, e := Parse([]byte(s), "generic_csv", Mapping{}); e == nil {
			t.Fatalf("accepted invalid statement %s", s)
		}
	}
	book := excelize.NewFile()
	defer book.Close()
	_ = book.SetSheetRow("Sheet1", "A1", &[]any{"Дата", "Сума", "Валюта", "Опис"})
	_ = book.SetSheetRow("Sheet1", "A2", &[]any{"01.10.2026", "-23.40", "EUR", "Synthetic merchant"})
	b, e := book.WriteToBuffer()
	if e != nil {
		t.Fatal(e)
	}
	rows, e := Parse(b.Bytes(), "privatbank_xlsx", Mapping{})
	if e != nil || len(rows) != 1 || rows[0].Amount.String() != "-23.40" {
		t.Fatal(rows, e)
	}
}
func TestFeedSecretIsolation(t *testing.T) {
	s := Service{}
	s.SetSecretKey([]byte("01234567890123456789012345678901"))
	encoded, e := s.seal("alice", "wise", "bank-secret")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := s.open("alice", "wise", encoded)
	if e != nil || raw != "bank-secret" {
		t.Fatal(e)
	}
	if _, e = s.open("bob", "wise", encoded); e == nil {
		t.Fatal("another user decrypted a credential")
	}
	b, e := json.Marshal(Feed{URL: "https://example.test/?token=secret", TokenEncrypted: encoded})
	if e != nil || string(b) != `{"id":"","account":"","format":""}` {
		t.Fatal("private feed material leaked", string(b), e)
	}
}

func TestPlaidJSON(t *testing.T) {
	input := `{"transactions":[{"transaction_id":"one","account_id":"bank","date":"2026-10-01","amount":23.40,"iso_currency_code":"EUR","name":"Coffee"},{"transaction_id":"two","account_id":"bank","date":"2026-10-02","amount":-100.01,"iso_currency_code":"EUR","name":"Income"},{"pending":true}],"total_transactions":3}`
	rows, e := Parse([]byte(input), "plaid", Mapping{})
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	if rows[0].Amount.String() != "-23.40" || rows[1].Amount.String() != "100.01" || rows[0].Raw["raw_json"] == "" {
		t.Fatal("Plaid sign, precision or provenance", rows)
	}
	if _, e = Parse([]byte(`[{"transaction_id":"x","account_id":"a","date":"2026-10-01","amount":1,"iso_currency_code":"EUR"},{"transaction_id":"y","account_id":"b","date":"2026-10-01","amount":1,"iso_currency_code":"EUR"}]`), "plaid", Mapping{}); e == nil {
		t.Fatal("mixed accounts accepted")
	}
	if _, e = Parse([]byte(`{"transactions":[],"total_transactions":2}`), "plaid", Mapping{}); e == nil {
		t.Fatal("incomplete pagination accepted")
	}
}

func TestAmbiguousStatementDatesRequireMapping(t *testing.T) {
	csv := []byte("Date,Amount,Currency\n01/02/2026,-12.34,EUR\n")
	if _, e := Parse(csv, "generic_csv", Mapping{}); e == nil {
		t.Fatal("ambiguous bank date silently interpreted")
	}
	rows, e := Parse(csv, "generic_csv", Mapping{DateFormat: "mdy"})
	if e != nil || rows[0].Date != "2026-01-02" {
		t.Fatal(rows, e)
	}
	rows, e = Parse(csv, "generic_csv", Mapping{DateFormat: "dmy"})
	if e != nil || rows[0].Date != "2026-02-01" {
		t.Fatal(rows, e)
	}
}
