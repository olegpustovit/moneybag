package expressions

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestChecksums(t *testing.T) {
	engine, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		function, value string
		valid           bool
	}{
		{"gtin_valid", "96385074", true},
		{"gtin_valid", "036000291452", true},
		{"gtin_valid", "6291041500213", true},
		{"gtin_valid", "10012345000017", true},
		{"gtin_valid", "6291041500212", false},
		{"gtin_valid", "03600029145", false},
		{"luhn", "79927398713", true},
		{"luhn", "79927398714", false},
		{"luhn", "18", true},
		{"luhn", "1234567812345670", true},
		{"mod11_10", "12345678903", true},
		{"mod11_10", "12345678904", false},
		{"mod11_10", "0792", true},
		{"mod97", "3214282912345698765432161182", true},
		{"mod97", "3214282912345698765432161183", false},
		{"mod97", "98", true},
		{"mod97", "97", false},
		{"iban_valid", "GB82 WEST 1234 5698 7654 32", true},
		{"iban_valid", "de89370400440532013000", true},
		{"iban_valid", "HR1210010051863000160", true},
		{"iban_valid", "GB83WEST12345698765432", false},
		{"iban_valid", "GB82WEST12345698765432!", false},
		{"iban_valid", "1282WEST12345698765432", false},
		{"iban_valid", "GBXXWEST12345698765432", false},
		{"iban_valid", "GB82\tWEST12345698765432", false},
		{"iban_valid", "GB00WEST12345698765432", false},
		{"iban_valid", "GB01WEST12345698765432", false},
		{"iban_valid", "GB99WEST12345698765432", false},
	} {
		t.Run(tc.function+"/"+tc.value, func(t *testing.T) {
			result, err := engine.Eval(context.Background(), fmt.Sprintf("%s(row.value)", tc.function), map[string]any{"row": map[string]any{"value": tc.value}})
			if err != nil || result.Value != tc.valid {
				t.Fatalf("got %v, %v; want %v", result.Value, err, tc.valid)
			}
		})
	}
	for _, function := range []string{"gtin_valid", "iban_valid", "luhn", "mod11_10", "mod97"} {
		for _, invalid := range []string{"", "0", "１２３４", "123x", "-98", "9 8", strings.Repeat("1", 1025)} {
			result, err := engine.Eval(context.Background(), function+"(row.value)", map[string]any{"row": map[string]any{"value": invalid}})
			if err != nil || result.Value != false {
				t.Fatalf("%s(%q) = %v, %v", function, invalid, result.Value, err)
			}
		}
		for _, invalid := range []any{nil, true, float64(123), []string{"123"}} {
			if _, err := engine.Eval(context.Background(), function+"(row.value)", map[string]any{"row": map[string]any{"value": invalid}}); err == nil {
				t.Fatalf("%s accepted %T", function, invalid)
			}
		}
	}
}

func TestChecksumDetectsSingleDigitChanges(t *testing.T) {
	for _, tc := range []struct {
		check func(string) bool
		value string
	}{
		{gtinValid, "6291041500213"}, {luhnValid, "79927398713"},
		{mod1110Valid, "12345678903"}, {mod97Valid, "3214282912345698765432161182"},
	} {
		for i := range tc.value {
			for digit := byte('0'); digit <= '9'; digit++ {
				if digit == tc.value[i] {
					continue
				}
				changed := []byte(tc.value)
				changed[i] = digit
				if tc.check(string(changed)) {
					t.Fatalf("single digit change accepted: %s -> %s", tc.value, changed)
				}
			}
		}
	}
}
