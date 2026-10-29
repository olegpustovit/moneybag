package rates

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/safefetch"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Quote struct {
	Archived bool           `json:"archived,omitempty" bson:"archived,omitempty"`
	ID       string         `json:"id" bson:"_id"`
	UserID   string         `json:"-" bson:"user_id"`
	Pair     string         `json:"pair" bson:"pair"`
	Base     string         `json:"base" bson:"base"`
	Quote    string         `json:"quote" bson:"quote"`
	At       string         `json:"at" bson:"at"`
	Value    ledger.Decimal `json:"value" bson:"value"`
	Source   string         `json:"source" bson:"source"`
}
type SetInput struct {
	Pair   string         `json:"pair"`
	Value  ledger.Decimal `json:"value"`
	At     string         `json:"at"`
	Source string         `json:"source"`
}
type Result struct {
	Quote   Quote  `json:"quote"`
	EventID string `json:"event_id"`
}
type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo  *MongoRepository
	audit *audit.Service
}

func NewMongoRepository(db *mongo.Database) *MongoRepository {
	return &MongoRepository{db}
}
func NewService(r *MongoRepository, a *audit.Service) *Service { return &Service{r, a} }

var pairPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,19}/[A-Za-z][A-Za-z0-9_]{0,19}$`)

func Pair(raw string) (string, string, error) {
	if !strings.Contains(raw, "/") && len(raw) > 3 {
		raw = raw[:len(raw)-3] + "/" + raw[len(raw)-3:]
	}
	if !pairPattern.MatchString(raw) {
		return "", "", fmt.Errorf("pair must be BASE/QUOTE, for example EUR/USD or XAU_g/USD")
	}
	a, b, _ := strings.Cut(raw, "/")
	if a == b {
		return "", "", fmt.Errorf("pair currencies must differ")
	}
	return a, b, nil
}
func (s *Service) Set(ctx context.Context, p principal.Principal, in SetInput) (Result, error) {
	if e := p.Require("ledger:write"); e != nil {
		return Result{}, e
	}
	base, quote, e := Pair(in.Pair)
	if e != nil {
		return Result{}, e
	}
	at, e := time.Parse(time.RFC3339, in.At)
	if e != nil || in.Value.Rat().Sign() <= 0 || strings.TrimSpace(in.Source) == "" || len(in.Source) > 300 {
		return Result{}, fmt.Errorf("positive decimal quote, RFC3339 timestamp and source required")
	}
	q := Quote{ID: ulid.Make().String(), UserID: p.UserID, Pair: base + "/" + quote, Base: base, Quote: quote, At: at.UTC().Format(time.RFC3339), Value: in.Value, Source: in.Source}
	f := bson.M{"user_id": p.UserID, "pair": q.Pair, "at": q.At}
	var old Quote
	e = s.repo.db.Collection("prices").FindOne(ctx, f).Decode(&old)
	if e != nil && !errors.Is(e, mongo.ErrNoDocuments) {
		return Result{}, e
	}
	if e == nil {
		q.ID = old.ID
	}
	id, e := s.audit.Write(ctx, p, "rate_set", audit.Target{Collection: "prices", ID: q.ID}, "", old, q, func(tx context.Context) error {
		_, e := s.repo.db.Collection("prices").ReplaceOne(tx, f, q, options.Replace().SetUpsert(true))
		return e
	})
	return Result{q, id}, e
}
func (s *Service) Get(ctx context.Context, p principal.Principal, pairs []string, at string) ([]Quote, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	if len(pairs) > 50 {
		return nil, fmt.Errorf("at most 50 pairs")
	}
	if at == "" {
		at = time.Now().UTC().Format(time.RFC3339)
	}
	if _, e := time.Parse(time.RFC3339, at); e != nil {
		return nil, fmt.Errorf("at must be RFC3339")
	}
	out := []Quote{}
	for _, pair := range pairs {
		a, b, e := Pair(pair)
		if e != nil {
			return nil, e
		}
		q, e := s.quote(ctx, p, a, b, at)
		if errors.Is(e, mongo.ErrNoDocuments) {
			inv, ie := s.quote(ctx, p, b, a, at)
			if ie != nil {
				return nil, fmt.Errorf("no quote for %s at or before %s", pair, at)
			}
			v, ie := ledger.ParseDecimal(strings.TrimRight(strings.TrimRight(new(big.Rat).Inv(inv.Value.Rat()).FloatString(10), "0"), "."))
			if ie != nil {
				return nil, ie
			}
			q = Quote{ID: inv.ID, Pair: a + "/" + b, Base: a, Quote: b, At: inv.At, Value: v, Source: "inverse of " + inv.Pair + " (" + inv.Source + ")"}
		} else if e != nil {
			return nil, e
		}
		out = append(out, q)
	}
	return out, nil
}

// History returns every stored, non-archived quote for the pairs, oldest first, optionally bounded by from/to (RFC3339 or YYYY-MM-DD); at most 5000.
// A pair with no direct quotes falls back to the inverted quotes of the opposite pair, as Get does.
func (s *Service) History(ctx context.Context, p principal.Principal, pairs []string, from, to string) ([]Quote, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	if len(pairs) > 50 {
		return nil, fmt.Errorf("at most 50 pairs")
	}
	bound := func(v string, end bool) (string, error) {
		if v == "" {
			return "", nil
		}
		if t, e := time.Parse(time.RFC3339, v); e == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
		t, e := time.Parse("2006-01-02", v)
		if e != nil {
			return "", fmt.Errorf("from and to must be RFC3339 or YYYY-MM-DD")
		}
		if end {
			t = t.Add(24*time.Hour - time.Second)
		}
		return t.Format(time.RFC3339), nil
	}
	lo, e := bound(from, false)
	if e != nil {
		return nil, e
	}
	hi, e := bound(to, true)
	if e != nil {
		return nil, e
	}
	window := bson.M{}
	if lo != "" {
		window["$gte"] = lo
	}
	if hi != "" {
		window["$lte"] = hi
	}
	find := func(pair string) ([]Quote, error) {
		f := bson.M{"user_id": p.UserID, "pair": pair, "archived": bson.M{"$ne": true}}
		if len(window) > 0 {
			f["at"] = window
		}
		cur, e := s.repo.db.Collection("prices").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "at", Value: 1}}).SetLimit(5001))
		if e != nil {
			return nil, e
		}
		var qs []Quote
		return qs, cur.All(ctx, &qs)
	}
	out := []Quote{}
	for _, pair := range pairs {
		a, b, e := Pair(pair)
		if e != nil {
			return nil, e
		}
		qs, e := find(a + "/" + b)
		if e != nil {
			return nil, e
		}
		if len(qs) == 0 {
			inv, e := find(b + "/" + a)
			if e != nil {
				return nil, e
			}
			for _, q := range inv {
				v, e := ledger.ParseDecimal(strings.TrimRight(strings.TrimRight(new(big.Rat).Inv(q.Value.Rat()).FloatString(10), "0"), "."))
				if e != nil {
					return nil, e
				}
				qs = append(qs, Quote{ID: q.ID, Pair: a + "/" + b, Base: a, Quote: b, At: q.At, Value: v, Source: "inverse of " + q.Pair + " (" + q.Source + ")"})
			}
		}
		out = append(out, qs...)
		if len(out) > 5000 {
			return nil, fmt.Errorf("rate_history exceeds 5000 rows; narrow from/to")
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out, nil
}
func (s *Service) quote(ctx context.Context, p principal.Principal, base, quote, at string) (Quote, error) {
	var q Quote
	e := s.repo.db.Collection("prices").FindOne(ctx, bson.M{"user_id": p.UserID, "pair": base + "/" + quote, "at": bson.M{"$lte": at}, "archived": bson.M{"$ne": true}}, options.FindOne().SetSort(bson.D{{Key: "at", Value: -1}})).Decode(&q)
	return q, e
}

// rate uses the exact inverse of an opposite quote, unlike the rounded inverse Get reports.
func (s *Service) rate(ctx context.Context, p principal.Principal, base, quote, at string) (*big.Rat, error) {
	if base == quote {
		return big.NewRat(1, 1), nil
	}
	q, e := s.quote(ctx, p, base, quote, at)
	if e == nil {
		return q.Value.Rat(), nil
	}
	if !errors.Is(e, mongo.ErrNoDocuments) {
		return nil, e
	}
	q, e = s.quote(ctx, p, quote, base, at)
	if e == nil {
		return new(big.Rat).Inv(q.Value.Rat()), nil
	}
	if !errors.Is(e, mongo.ErrNoDocuments) {
		return nil, e
	}
	return nil, fmt.Errorf("no quote for %s/%s at or before %s", base, quote, at)
}
func (s *Service) Convert(ctx context.Context, p principal.Principal, a ledger.Amount, quote, at string) (ledger.Amount, error) {
	r, e := s.Rate(ctx, p, a.Commodity, quote, at)
	if e != nil {
		return ledger.Amount{}, e
	}
	return Apply(a, r, quote)
}

// Rate is the from->quote rate at time at, triangulating through USD when no direct pair exists.
func (s *Service) Rate(ctx context.Context, p principal.Principal, from, quote, at string) (*big.Rat, error) {
	r, e := s.rate(ctx, p, from, quote, at)
	if e != nil && from != "USD" && quote != "USD" {
		var b, c *big.Rat
		b, e = s.rate(ctx, p, from, "USD", at)
		if e == nil {
			c, e = s.rate(ctx, p, "USD", quote, at)
			if e == nil {
				r = new(big.Rat).Mul(b, c)
			}
		}
	}
	return r, e
}

// Apply converts a by rate r into quote, rounded to cents.
func Apply(a ledger.Amount, r *big.Rat, quote string) (ledger.Amount, error) {
	v := new(big.Rat).Mul(a.Value.Rat(), r)
	d, e := ledger.ParseDecimal(v.FloatString(2))
	return ledger.Amount{Value: d, Commodity: quote}, e
}

type ecbQuote struct{ Pair, At, Value string }

// parseECB reads every day of an ECB eurofxref XML document, oldest data included.
func parseECB(b []byte) ([]ecbQuote, error) {
	var doc struct {
		Cube struct {
			Days []struct {
				Time  string `xml:"time,attr"`
				Rates []struct {
					Currency string `xml:"currency,attr"`
					Rate     string `xml:"rate,attr"`
				} `xml:"Cube"`
			} `xml:"Cube"`
		} `xml:"Cube"`
	}
	if e := xml.Unmarshal(b, &doc); e != nil {
		return nil, fmt.Errorf("parse ECB quotes: %w", e)
	}
	out := []ecbQuote{}
	for _, d := range doc.Cube.Days {
		for _, r := range d.Rates {
			out = append(out, ecbQuote{"EUR/" + r.Currency, d.Time + "T16:00:00Z", r.Rate})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ECB returned no quotes")
	}
	return out, nil
}

// RefreshECB backfills the last 90 days so budgets and valuations of recent months have quotes.
// shortcut: older entries still need rate_set quotes; load eurofxref-hist.xml once if long histories are imported.
func (s *Service) RefreshECB(ctx context.Context, p principal.Principal) ([]Quote, error) {
	if e := p.Require("ledger:write"); e != nil {
		return nil, e
	}
	b, _, e := safefetch.Read(ctx, safefetch.Client(), "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist-90d.xml", 1024*1024)
	if e != nil {
		return nil, e
	}
	quotes, e := parseECB(b)
	if e != nil {
		return nil, e
	}
	const source = "ECB daily reference"
	oldest := quotes[0].At
	for _, q := range quotes {
		oldest = min(oldest, q.At)
	}
	cur, e := s.repo.db.Collection("prices").Find(ctx, bson.M{"user_id": p.UserID, "source": source, "at": bson.M{"$gte": oldest}})
	if e != nil {
		return nil, e
	}
	var stored []Quote
	if e = cur.All(ctx, &stored); e != nil {
		return nil, e
	}
	have := map[string]string{}
	for _, q := range stored {
		have[q.Pair+"@"+q.At] = q.Value.String()
	}
	out := []Quote{}
	e = s.audit.Workflow(ctx, func(tx context.Context) error {
		for _, q := range quotes {
			v, e := ledger.ParseDecimal(q.Value)
			if e != nil {
				return e
			}
			if old, ok := have[q.Pair+"@"+q.At]; ok && old == v.String() {
				continue
			}
			r, e := s.Set(tx, p, SetInput{Pair: q.Pair, Value: v, At: q.At, Source: source})
			if e != nil {
				return e
			}
			out = append(out, r.Quote)
		}
		return nil
	})
	return out, e
}
