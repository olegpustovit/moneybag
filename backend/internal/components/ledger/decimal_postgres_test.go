package ledger

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres/pgtest"
)

func TestDecimalScan(t *testing.T) {
	for _, input := range []any{"23.40", []byte("23.40")} {
		var d Decimal
		if err := d.Scan(input); err != nil || d.String() != "23.40" {
			t.Fatalf("Scan(%v) = %s, %v", input, d.String(), err)
		}
		value, err := d.Value()
		if err != nil || value != "23.40" {
			t.Fatalf("Value() = %v, %v", value, err)
		}
	}
	for _, input := range []any{nil, 23.4, int64(23), "NaN", []byte("1e2")} {
		d := MustDecimal("1.00")
		if err := d.Scan(input); err == nil || d.String() != "1.00" {
			t.Fatalf("invalid Scan(%v) changed value: %s, %v", input, d.String(), err)
		}
	}
}

func TestDecimalNumericRoundTrip(t *testing.T) {
	pool := pgtest.New(t)
	for _, text := range []string{"23.40", "-23.40", "0.1000000000000000000000000000000001", "1234567890123456789012345678901234", "0.000000000000000000000000000000000000000000000000000000000000000000000000001"} {
		var got Decimal
		err := pool.QueryRow(context.Background(), "SELECT @amount::numeric", pgx.NamedArgs{"amount": MustDecimal(text)}).Scan(&got)
		if err != nil || got.String() != MustDecimal(text).String() {
			t.Fatalf("numeric roundtrip %q = %s, %v", text, got.String(), err)
		}
	}
}
