package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

func encodedAt(t *testing.T, asOf string, value int) *viewEntry {
	t.Helper()
	raw, err := json.Marshal(PageView{Values: map[string]any{"k": value, "tricky": `"as_of":"x","stale":true`}, AsOf: asOf})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeView(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &viewEntry{encoded: encoded}
}

func TestViewETagIgnoresRenderTimeOnly(t *testing.T) {
	first, later := encodedAt(t, "2026-01-01T00:00:00Z", 1), encodedAt(t, "2026-01-01T00:00:01Z", 1)
	if first.encoded.ETag != later.encoded.ETag {
		t.Fatal("render time changed the ETag")
	}
	if first.encoded.ETag == encodedAt(t, "2026-01-01T00:00:00Z", 2).encoded.ETag {
		t.Fatal("new numbers kept the ETag")
	}
	stale, err := first.staleEncoded()
	if err != nil {
		t.Fatal(err)
	}
	if stale.ETag == first.encoded.ETag {
		t.Fatal("the stale flag kept the ETag")
	}
	raw, err := stale.Raw()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), `"as_of":"2026-01-01T00:00:00Z","stale":true}`) {
		t.Fatalf("stale body: %s", raw)
	}
}
