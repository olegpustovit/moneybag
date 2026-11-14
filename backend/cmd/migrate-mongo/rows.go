package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
)

type columnKind byte

const (
	kindText columnKind = iota
	kindJSON
	kindNumeric
	kindTime
	kindBytes
	kindInteger
	kindBool
	kindArray
)

type mapping struct {
	name       string
	columns    []string
	sourceKeys []string
}

func prepareRow(m mapping, row []any) ([]any, error) {
	for i, column := range m.columns {
		switch columnKinds[m.name][column] {
		case kindJSON:
			encoded, err := json.Marshal(row[i])
			if err != nil {
				return nil, err
			}
			row[i] = encoded
		case kindNumeric:
			switch v := row[i].(type) {
			case ledger.Decimal:
				row[i] = v.String()
			case string:
				if _, ok := new(big.Rat).SetString(v); !ok {
					return nil, fmt.Errorf("invalid decimal column %s", column)
				}
			default:
				return nil, fmt.Errorf("unsupported numeric column %s", column)
			}
		case kindBytes:
			if v, ok := row[i].(json.RawMessage); ok {
				row[i] = []byte(v)
			}
		case kindTime:
			switch v := row[i].(type) {
			case time.Time:
				row[i] = v.UTC().Truncate(time.Millisecond)
			case *time.Time:
				if v == nil {
					row[i] = nil
				} else {
					row[i] = v.UTC().Truncate(time.Millisecond)
				}
			case string:
				at, err := time.Parse(time.RFC3339Nano, v)
				if err != nil {
					return nil, fmt.Errorf("invalid timestamp column %s", column)
				}
				row[i] = at.UTC().Truncate(time.Millisecond)
			default:
				return nil, fmt.Errorf("unsupported timestamp column %s", column)
			}
		}
	}
	return row, nil
}

func canonicalRow(m mapping, row []any) ([]byte, error) {
	values := make([]any, len(row))
	for i, column := range m.columns {
		value := row[i]
		switch columnKinds[m.name][column] {
		case kindJSON:
			if encoded, ok := value.([]byte); ok {
				var decoded any
				decoder := json.NewDecoder(strings.NewReader(string(encoded)))
				decoder.UseNumber()
				if err := decoder.Decode(&decoded); err != nil {
					return nil, err
				}
				value = decoded
			}
		case kindNumeric:
			rational, ok := new(big.Rat).SetString(fmt.Sprint(value))
			if !ok {
				return nil, fmt.Errorf("invalid numeric projection")
			}
			value = rational.RatString()
		case kindTime:
			switch v := value.(type) {
			case time.Time:
				value = v.UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano)
			case string:
				t, err := time.Parse(time.RFC3339Nano, v)
				if err != nil {
					return nil, err
				}
				value = t.UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano)
			}
		case kindBytes:
			if b, ok := value.([]byte); ok {
				if b == nil {
					value = nil
				} else {
					value = hex.EncodeToString(b)
				}
			}
		}
		values[i] = value
	}
	return json.Marshal(values)
}
