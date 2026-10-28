package rates

import "testing"

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
