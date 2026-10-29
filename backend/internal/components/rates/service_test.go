package rates

import (
	"testing"
	"time"
)

func TestParseECBReadsEveryDay(t *testing.T) {
	doc := `<?xml version="1.0"?><gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref"><Cube>
<Cube time="2026-10-28"><Cube currency="USD" rate="1.1133"/><Cube currency="GBP" rate="0.83785"/></Cube>
<Cube time="2026-10-27"><Cube currency="USD" rate="1.1101"/></Cube>
</Cube></gesmes:Envelope>`
	q, e := parseECB([]byte(doc))
	if e != nil || len(q) != 3 || q[2] != (ecbQuote{"EUR/USD", "2026-10-27T16:00:00Z", "1.1101"}) {
		t.Fatal(q, e)
	}
	if _, e = parseECB([]byte(`<Envelope><Cube></Cube></Envelope>`)); e == nil {
		t.Fatal("empty feed accepted")
	}
}

func TestParseGoldPayloads(t *testing.T) {
	oz, at, e := parseGoldAPI([]byte(`{"name":"Gold","price":2650.45,"symbol":"XAU","updatedAt":"2026-10-29T10:00:00.000Z"}`))
	if e != nil || oz.FloatString(2) != "2650.45" || at.UTC().Format(time.RFC3339) != "2026-10-29T10:00:00Z" {
		t.Fatal(oz, at, e)
	}
	if _, _, e = parseGoldAPI([]byte(`{"price":0,"updatedAt":"x"}`)); e == nil {
		t.Fatal("bad payload accepted")
	}
	mid, at, e := parseSwissquote([]byte(`[{"topo":{"platform":"MT5"},"spreadProfilePrices":[{"spreadProfile":"premium","bid":2650.10,"ask":2650.90},{"spreadProfile":"standard","bid":1,"ask":2}],"ts":1761732000000}]`))
	if e != nil || mid.FloatString(2) != "2650.50" || at.UnixMilli() != 1761732000000 {
		t.Fatal(mid, at, e)
	}
	if _, at, e = parseSwissquote([]byte(`[{"spreadProfilePrices":[{"bid":1,"ask":3}]}]`)); e != nil || time.Since(at) > time.Minute {
		t.Fatal("missing ts should default to now", e)
	}
	if _, _, e = parseSwissquote([]byte(`[]`)); e == nil {
		t.Fatal("empty payload accepted")
	}
}

func TestWeekdaysSinceIgnoresWeekends(t *testing.T) {
	thu := time.Date(2026, 10, 22, 16, 0, 0, 0, time.UTC)
	if n := weekdaysSince(thu, time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)); n != 3 {
		t.Fatal(n)
	}
}

func TestParseECBData(t *testing.T) {
	csv := "KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE,OBS_STATUS\nEXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2026-10-20,1.1215,A\nEXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2026-10-21,,M\n"
	q, e := parseECBData("EUR/USD", []byte(csv))
	if e != nil || len(q) != 1 || q[0] != (ecbQuote{"EUR/USD", "2026-10-20T16:00:00Z", "1.1215"}) {
		t.Fatal(q, e)
	}
	if _, e = parseECBData("EUR/USD", []byte("a,b\n1,2\n")); e == nil {
		t.Fatal("csv without ECB columns accepted")
	}
}
