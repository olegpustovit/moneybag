package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func legacyDocument(t *testing.T, v any) bson.Raw {
	t.Helper()
	raw, err := bson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestLegacyAdaptersKeepPrivateFieldsAndExactMoney(t *testing.T) {
	number, err := bson.ParseDecimal128("10000000000000001.01")
	if err != nil {
		t.Fatal(err)
	}
	raw := legacyDocument(t, bson.M{"_id": "entry", "user_id": "owner", "seq": int64(1), "created_at": time.Date(2026, 1, 1, 0, 0, 0, 123000000, time.UTC), "postings": bson.A{bson.M{"account": "assets:bank", "amount": bson.M{"v": number, "c": "EUR"}}}})
	var record legacyLedgerEntry
	if err = decodeLegacy(raw, &record); err != nil {
		t.Fatal(err)
	}
	entry := record.value()
	if entry.ID != "entry" || entry.UserID != "owner" || entry.CreatedAt.Nanosecond() != 123000000 || len(entry.Postings) != 1 || entry.Postings[0].Amount.Value.String() != "10000000000000001.01" || entry.Postings[0].Amount.Commodity != "EUR" {
		t.Fatalf("incomplete entry mapping: %+v", entry)
	}
	var account legacyLedgerAccount
	if err = decodeLegacy(legacyDocument(t, bson.M{"_id": "owner:assets:bank", "user_id": "owner", "id": "assets:bank", "display_name": "Bank"}), &account); err != nil {
		t.Fatal(err)
	}
	a := account.value()
	if a.Key != "owner:assets:bank" || a.ID != "assets:bank" || a.UserID != "owner" || a.DisplayName != "Bank" {
		t.Fatalf("incomplete account: %+v", a)
	}
	var income legacyModelIncome
	if err = decodeLegacy(legacyDocument(t, bson.M{"id": "salary", "growth_pct": float64(3), "payer_match": "row.payee == 'Bank'", "lands_in": "assets:bank"}), &income); err != nil {
		t.Fatal(err)
	}
	if v := income.value(); v.GrowthPct != 3 || v.PayerMatch == "" || v.LandsIn != "assets:bank" {
		t.Fatalf("incomplete stream: %+v", v)
	}
}

func TestLegacyAuditBytesAndFrozenHTMLSurvive(t *testing.T) {
	payload := []byte(`{"z":9007199254740993,"a":"1.50"}`)
	var record legacyAuditEvent
	if err := decodeLegacy(legacyDocument(t, bson.M{"_id": "event", "user_id": "owner", "before": payload, "target": bson.M{"coll": "journal", "id": "entry"}, "prev_hash": "previous"}), &record); err != nil {
		t.Fatal(err)
	}
	event := record.value()
	if !bytes.Equal(event.Before, payload) || event.Target.Collection != "journal" || event.PrevHash != "previous" {
		t.Fatal("audit payload or target changed")
	}
	rendered, err := snapshotRendering(bson.M{"html": "<p>Frozen</p>", "values": bson.M{"count": float64(1)}})
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]any
	if err = json.Unmarshal(rendered, &restored); err != nil {
		t.Fatal(err)
	}
	if restored["html"] != "<p>Frozen</p>" {
		t.Fatal("frozen HTML lost")
	}
}
