package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"time"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var legacyRegistry = func() *bson.Registry {
	registry := bson.NewRegistry()
	registry.RegisterTypeDecoder(reflect.TypeFor[ledger.Decimal](), bson.ValueDecoderFunc(func(_ bson.DecodeContext, reader bson.ValueReader, value reflect.Value) error {
		if reader.Type() == bson.TypeNull {
			if err := reader.ReadNull(); err != nil {
				return err
			}
			value.Set(reflect.ValueOf(ledger.Decimal{}))
			return nil
		}
		raw, err := reader.ReadDecimal128()
		if err != nil {
			return err
		}
		number, err := decimalFromLegacy(raw)
		if err != nil {
			return err
		}
		value.Set(reflect.ValueOf(number))
		return nil
	}))
	return registry
}()

func decimalFromLegacy(raw bson.Decimal128) (ledger.Decimal, error) {
	text := raw.String()
	if strings.ContainsAny(text, "eE") {
		rational, ok := new(big.Rat).SetString(text)
		if !ok {
			return ledger.Decimal{}, fmt.Errorf("non-finite source decimal")
		}
		return ledger.DecimalFromRat(rational)
	}
	return ledger.ParseDecimal(text)
}

func decodeLegacy(raw bson.Raw, out any) error {
	decoder := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(raw)))
	decoder.SetRegistry(legacyRegistry)
	decoder.DefaultDocumentM()
	decoder.ObjectIDAsHexString()
	return decoder.Decode(out)
}

type legacyAmount struct {
	Value     ledger.Decimal `bson:"v"`
	Commodity string         `bson:"c"`
}

func (v legacyAmount) value() ledger.Amount {
	return ledger.Amount{Value: v.Value, Commodity: v.Commodity}
}

type legacyAuditTarget struct {
	Collection string `bson:"coll"`
	ID         string `bson:"id"`
}

// These original anonymous/private records have no exported application type.
type counterRecord struct {
	ID     string `bson:"_id"`
	UserID string `bson:"user_id"`
	Name   string `bson:"name"`
	N      int64  `bson:"n"`
	Rates  int64  `bson:"rates"`
}
type settingRecord struct {
	ID string    `bson:"_id"`
	By string    `bson:"by"`
	At time.Time `bson:"at"`
}
type usageRecord struct {
	ID        string `bson:"_id"`
	UserID    string `bson:"user_id"`
	Date      string `bson:"date"`
	Path      string `bson:"path"`
	Dashboard string `bson:"dashboard"`
	Views     int64  `bson:"views"`
	LastAt    string `bson:"last_at"`
}
type fileRecord struct {
	ID      string `bson:"_id"`
	UserID  string `bson:"user_id"`
	Content []byte `bson:"content"`
	SHA256  string `bson:"sha256"`
}
type incomeRecord struct {
	ID      string              `bson:"_id"`
	UserID  string              `bson:"user_id"`
	Streams []legacyModelIncome `bson:"streams"`
}

func (v incomeRecord) streams() []model.Income {
	if v.Streams == nil {
		return nil
	}
	streams := make([]model.Income, len(v.Streams))
	for i, s := range v.Streams {
		streams[i] = s.value()
	}
	return streams
}

type balanceRecord struct {
	UserID string `bson:"user_id"`
	Dirty  bool   `bson:"dirty"`
}

func snapshotRendering(raw bson.M) (json.RawMessage, error) {
	var rendered dashboard.Rendered
	if raw != nil {
		encoded, err := bson.Marshal(raw)
		if err != nil {
			return nil, err
		}
		if err = decodeLegacy(encoded, &rendered); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(rendered)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	if html, ok := raw["html"]; ok {
		fields["html"], err = json.Marshal(html)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(fields)
}

type tagRecord struct {
	ID      string   `bson:"_id"`
	UserID  string   `bson:"user_id"`
	EntryID string   `bson:"entry_id"`
	Tags    []string `bson:"tags"`
}
type assertionRecord struct {
	reconcile.Assertion `bson:",inline"`
	ID                  string       `bson:"_id"`
	UserID              string       `bson:"user_id"`
	Amount              legacyAmount `bson:"amount"`
	Archived            bool         `bson:"archived"`
}

func (v assertionRecord) amount() ledger.Amount { return v.Amount.value() }

type discrepancyRecord struct {
	reconcile.Discrepancy `bson:",inline"`
	ID                    string       `bson:"_id"`
	UserID                string       `bson:"user_id"`
	AssertionID           string       `bson:"assertion_id"`
	AdjustmentID          string       `bson:"adjustment_id"`
	Diff                  legacyAmount `bson:"diff"`
	Archived              bool         `bson:"archived"`
}

func (v discrepancyRecord) diff() ledger.Amount { return v.Diff.value() }
