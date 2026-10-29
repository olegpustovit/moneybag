package rates

import (
	"context"
	"encoding/csv"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/safefetch"
	"go.mongodb.org/mongo-driver/v2/bson"
	"net/url"
	"slices"
	"strings"
	"time"
)

const ecbDataURL = "https://data-api.ecb.europa.eu/service/data/EXR"

// BackfillECB loads ECB daily reference history for EUR/<CCY> pairs from a date; the 90-day feed covers only recent days.
// Writes go through put, so corrected and archived quotes are kept.
func (s *Service) BackfillECB(ctx context.Context, p principal.Principal, from string, pairs []string) ([]Quote, error) {
	if e := p.Require("ledger:write"); e != nil {
		return nil, e
	}
	start, e := time.Parse("2006-01-02", from)
	if e != nil || start.Before(time.Now().AddDate(-10, 0, 0)) {
		return nil, fmt.Errorf("backfill_from must be YYYY-MM-DD within the last 10 years")
	}
	if len(pairs) == 0 {
		pairs = []string{"EUR/USD"}
	}
	var quotes []ecbQuote
	for _, pair := range slices.Compact(slices.Sorted(slices.Values(pairs))) {
		ccy, ok := strings.CutPrefix(pair, "EUR/")
		if !ok || len(ccy) != 3 || strings.ToUpper(ccy) != ccy {
			return nil, fmt.Errorf("backfill pairs must be EUR/<CCY>, got %q", pair)
		}
		b, _, e := safefetch.Read(ctx, s.http, s.urls.ecbData+"/D."+ccy+".EUR.SP00.A?format=csvdata&startPeriod="+url.QueryEscape(from), 4*maxFeedBytes)
		if e != nil {
			return nil, fmt.Errorf("ECB history %s: %w", pair, e)
		}
		q, e := parseECBData(pair, b)
		if e != nil {
			return nil, fmt.Errorf("ECB history %s: %w", pair, e)
		}
		quotes = append(quotes, q...)
	}
	if len(quotes) == 0 {
		return []Quote{}, nil
	}
	have, e := s.existing(ctx, p, bson.M{"at": bson.M{"$gte": from}})
	if e != nil {
		return nil, e
	}
	out := []Quote{}
	e = s.audit.Workflow(ctx, func(tx context.Context) error {
		out = out[:0]
		for _, q := range quotes {
			v, e := ledger.ParseDecimal(q.Value)
			if e != nil {
				return e
			}
			w, e := s.put(tx, p, have, SetInput{Pair: q.Pair, Value: v, At: q.At, Source: ecbSource})
			if e != nil {
				return e
			}
			if w != nil {
				out = append(out, *w)
			}
		}
		return nil
	})
	return out, e
}

// parseECBData reads the ECB data API csvdata format (TIME_PERIOD, OBS_VALUE columns); empty observations are skipped.
func parseECBData(pair string, b []byte) ([]ecbQuote, error) {
	rows, e := csv.NewReader(strings.NewReader(string(b))).ReadAll()
	if e != nil || len(rows) == 0 {
		return nil, fmt.Errorf("unreadable csv")
	}
	day, val := slices.Index(rows[0], "TIME_PERIOD"), slices.Index(rows[0], "OBS_VALUE")
	if day < 0 || val < 0 {
		return nil, fmt.Errorf("csv lacks TIME_PERIOD or OBS_VALUE")
	}
	out := []ecbQuote{}
	for _, r := range rows[1:] {
		if len(r) <= max(day, val) || r[val] == "" {
			continue
		}
		if _, e := time.Parse("2006-01-02", r[day]); e != nil {
			return nil, fmt.Errorf("bad date %q", r[day])
		}
		out = append(out, ecbQuote{pair, r[day] + "T16:00:00Z", r[val]})
	}
	return out, nil
}
