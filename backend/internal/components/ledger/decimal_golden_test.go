package ledger

import (
	"encoding/json"
	"os"
	"testing"
)

// Captured by running ParseDecimal and String on dev at 55c5d50, before the persistence port.
func TestDecimalMongoGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/decimal-mongo-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input  string `json:"input"`
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 30 {
		t.Fatal("incomplete decimal golden fixture")
	}
	for _, tc := range cases {
		t.Run(tc.Input, func(t *testing.T) {
			got, err := ParseDecimal(tc.Input)
			if tc.Error != "" {
				if err == nil || err.Error() != tc.Error {
					t.Fatalf("ParseDecimal(%q) error = %v, want %q", tc.Input, err, tc.Error)
				}
				return
			}
			if err != nil || got.String() != tc.Output {
				t.Fatalf("ParseDecimal(%q) = %q, %v; want %q", tc.Input, got.String(), err, tc.Output)
			}
		})
	}
}
