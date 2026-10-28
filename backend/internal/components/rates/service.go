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
		var q Quote
		e = s.repo.db.Collection("prices").FindOne(ctx, bson.M{"user_id": p.UserID, "pair": a + "/" + b, "at": bson.M{"$lte": at}, "archived": bson.M{"$ne": true}}, options.FindOne().SetSort(bson.D{{Key: "at", Value: -1}})).Decode(&q)
		if errors.Is(e, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("no quote for %s at or before %s", pair, at)
		}
		if e != nil {
			return nil, e
		}
		out = append(out, q)
	}
	return out, nil
}
func (s *Service) rate(ctx context.Context, p principal.Principal, base, quote, at string) (*big.Rat, error) {
	if base == quote {
		return big.NewRat(1, 1), nil
	}
	q, e := s.Get(ctx, p, []string{base + "/" + quote}, at)
	if e == nil {
		return q[0].Value.Rat(), nil
	}
	q, inv := s.Get(ctx, p, []string{quote + "/" + base}, at)
	if inv == nil {
		return new(big.Rat).Inv(q[0].Value.Rat()), nil
	}
	return nil, e
}
func (s *Service) Convert(ctx context.Context, p principal.Principal, a ledger.Amount, quote, at string) (ledger.Amount, error) {
	r, e := s.rate(ctx, p, a.Commodity, quote, at)
	if e != nil && a.Commodity != "USD" && quote != "USD" {
		var b, c *big.Rat
		b, e = s.rate(ctx, p, a.Commodity, "USD", at)
		if e == nil {
			c, e = s.rate(ctx, p, "USD", quote, at)
			if e == nil {
				r = new(big.Rat).Mul(b, c)
			}
		}
	}
	if e != nil {
		return ledger.Amount{}, e
	}
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
