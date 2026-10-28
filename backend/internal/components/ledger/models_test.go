package ledger

import (
	"encoding/json"
	"go.mongodb.org/mongo-driver/v2/bson"
	"math/big"
	"strings"
	"testing"
)

func TestNegatePreservesDecimalRange(t *testing.T) {
	for _, text := range []string{"0", "-0.00", "23.40", "-0.000001", "1" + strings.Repeat("0", 79)} {
		d := MustDecimal(text)
		neg := d.Negate()
		if neg.Rat().Cmp(new(big.Rat).Neg(d.Rat())) != 0 || neg.Negate().Decimal128 != d.Decimal128 {
			t.Fatalf("negate failed for %s", text)
		}
		if _, err := ParseDecimal(neg.String()); err != nil {
			t.Fatalf("negated decimal rejected: %s: %v", neg.String(), err)
		}
	}
}

func TestExactBalanceAndSerialization(t *testing.T) {
	in := EntryInput{Date: "2026-10-01", Postings: []Posting{{Account: "expenses:food", Amount: Amount{MustDecimal("0.1"), "EUR"}}, {Account: "expenses:food", Amount: Amount{MustDecimal("0.2"), "EUR"}}, {Account: "assets:bank", Amount: Amount{MustDecimal("-0.3"), "EUR"}}}}
	if e := Validate(in); e != nil {
		t.Fatal(e)
	}
	in.Postings[2].Amount.Commodity = "USD"
	if Validate(in) == nil {
		t.Fatal("cross-commodity imbalance accepted")
	}
	for _, s := range []string{"NaN", "Infinity", "1e500", "", "0.12345678901234567890123456789012345"} {
		if _, e := ParseDecimal(s); e == nil {
			t.Fatalf("invalid decimal %q accepted", s)
		}
	}
	amount := Amount{MustDecimal("23.40"), "EUR"}
	b, e := json.Marshal(amount)
	if e != nil || string(b) != `{"v":"23.40","c":"EUR"}` {
		t.Fatalf("JSON: %s %v", b, e)
	}
	var out Amount
	raw, e := bson.Marshal(amount)
	if e != nil {
		t.Fatal(e)
	}
	if e = bson.Unmarshal(raw, &out); e != nil {
		t.Fatal(e)
	}
	if out.Value.Rat().Cmp(big.NewRat(234, 10)) != 0 {
		t.Fatal(out)
	}
	var v bson.Raw = raw
	if v.Lookup("v").Type != bson.TypeDecimal128 {
		t.Fatal("money not stored as Decimal128")
	}
}
