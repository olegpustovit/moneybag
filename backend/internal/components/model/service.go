package model

import (
	"context"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"math"
	"strings"
	"time"
)

type Override struct {
	Assumption string   `json:"assumption" bson:"assumption"`
	Value      *float64 `json:"value,omitempty" bson:"value,omitempty"`
	Expr       string   `json:"expr,omitempty" bson:"expr,omitempty"`
}
type Event struct {
	Month   int     `json:"month" bson:"month"`
	Amount  float64 `json:"amount" bson:"amount"`
	Account string  `json:"account,omitempty" bson:"account,omitempty"`
}
type SpendLine struct {
	Name     string  `json:"name" bson:"name"`
	Amount   float64 `json:"amount" bson:"amount"`
	Currency string  `json:"currency" bson:"currency"`
	USD      float64 `json:"usd" bson:"usd"`
}
type LegacyInputs struct {
	Spend                    []SpendLine `json:"spend" bson:"spend"`
	SalaryBiweeklyUSD        float64     `json:"salaryBiweeklyUsd" bson:"salary_biweekly_usd"`
	ReimbursementBiweeklyUSD float64     `json:"reimbursementBiweeklyUsd" bson:"reimbursement_biweekly_usd"`
	SideIncomeMonthlyUSD     float64     `json:"sideIncomeMonthlyUsd" bson:"side_income_monthly_usd"`
	KyivExtraDrainMonthlyUSD float64     `json:"kyivExtraDrainMonthlyUsd" bson:"kyiv_extra_drain_monthly_usd"`
	BufferUSD                float64     `json:"bufferUsd" bson:"buffer_usd"`
	YieldPct                 float64     `json:"yieldPct" bson:"yield_pct"`
}
type Scenario struct {
	Archived  bool          `json:"archived,omitempty" bson:"archived,omitempty"`
	Key       string        `json:"-" bson:"_id"`
	UserID    string        `json:"-" bson:"user_id"`
	ID        string        `json:"id" bson:"id"`
	Name      string        `json:"name" bson:"name"`
	Overrides []Override    `json:"overrides" bson:"overrides"`
	Events    []Event       `json:"events,omitempty" bson:"events,omitempty"`
	Legacy    *LegacyInputs `json:"legacy,omitempty" bson:"legacy,omitempty"`
}
type Income struct {
	ID        string  `json:"id" bson:"id"`
	Amount    float64 `json:"amount" bson:"amount"`
	Currency  string  `json:"currency" bson:"currency"`
	Frequency string  `json:"frequency" bson:"frequency"`
	Start     string  `json:"start,omitempty" bson:"start,omitempty"`
	End       string  `json:"end,omitempty" bson:"end,omitempty"`
	GrowthPct float64 `json:"growth_pct,omitempty" bson:"growth_pct,omitempty"`
}
type State struct {
	Assumptions []tables.Row `json:"assumptions"`
	Scenarios   []Scenario   `json:"scenarios"`
	Streams     []Income     `json:"streams"`
}
type ForecastInput struct {
	ScenarioIDs   []string `json:"scenario_ids"`
	HorizonMonths int      `json:"horizon_months"`
	ValueIn       string   `json:"value_in,omitempty"`
}
type Point struct {
	M      int      `json:"m"`
	Net    float64  `json:"net"`
	Cash   float64  `json:"cash"`
	Gold   float64  `json:"gold"`
	Runway *float64 `json:"runway"`
	Wise   float64  `json:"wise"`
	Kyiv   float64  `json:"kyiv"`
}
type Projection struct {
	Currency       string   `json:"currency"`
	Spend          float64  `json:"spend"`
	EndNet         float64  `json:"end_net"`
	Scenario       string   `json:"scenario"`
	Name           string   `json:"name"`
	Months         []Point  `json:"months"`
	SpendUSD       float64  `json:"spendUsd"`
	EndNetUSD      float64  `json:"endNetUsd"`
	Runway         *float64 `json:"runway"`
	KyivEmptyMonth int      `json:"kyivEmptyMonth"`
}
type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo   *MongoRepository
	tables *tables.Service
	audit  *audit.Service
	expr   *expressions.Engine
}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func NewService(r *MongoRepository, t *tables.Service, a *audit.Service, e *expressions.Engine) *Service {
	return &Service{r, t, a, e}
}
func (s *Service) Get(ctx context.Context, p principal.Principal) (State, error) {
	if e := p.Require("ledger:read"); e != nil {
		return State{}, e
	}
	out := State{Assumptions: []tables.Row{}, Scenarios: []Scenario{}, Streams: []Income{}}
	q, e := s.tables.Query(ctx, p, tables.QueryInput{DB: "assumptions"})
	if e == nil {
		out.Assumptions = q.Rows
	} else if !errors.Is(e, principal.ErrNotFound) {
		return out, e
	}
	cur, e := s.repo.db.Collection("scenarios").Find(ctx, bson.M{"user_id": p.UserID, "archived": bson.M{"$ne": true}}, options.Find().SetSort(bson.D{{Key: "id", Value: 1}}).SetLimit(101))
	if e != nil {
		return out, e
	}
	defer cur.Close(ctx)
	if e = cur.All(ctx, &out.Scenarios); e != nil {
		return out, e
	}
	if len(out.Scenarios) > 100 {
		return out, fmt.Errorf("at most 100 scenarios")
	}
	var streams struct {
		Streams []Income `bson:"streams"`
	}
	e = s.repo.db.Collection("income_models").FindOne(ctx, bson.M{"_id": p.UserID}).Decode(&streams)
	if e == nil {
		out.Streams = streams.Streams
	} else if !errors.Is(e, mongo.ErrNoDocuments) {
		return out, e
	}
	return out, nil
}
func (s *Service) Upsert(ctx context.Context, p principal.Principal, in Scenario) (Scenario, error) {
	if e := p.Require("model:write"); e != nil {
		return Scenario{}, e
	}
	if in.ID == "" || len(in.ID) > 100 || strings.TrimSpace(in.Name) == "" || len(in.Name) > 150 || len(in.Overrides) > 100 || len(in.Events) > 600 {
		return Scenario{}, fmt.Errorf("id, name, at most 100 overrides and 600 events required")
	}
	for _, o := range in.Overrides {
		if o.Assumption == "" || (o.Value == nil) == (o.Expr == "") {
			return Scenario{}, fmt.Errorf("each override needs assumption and exactly one value or expr")
		}
		if o.Value != nil && (math.IsInf(*o.Value, 0) || math.IsNaN(*o.Value)) {
			return Scenario{}, fmt.Errorf("finite override required")
		}
		if o.Expr != "" {
			if _, e := s.expr.Compile(o.Expr); e != nil {
				return Scenario{}, e
			}
		}
	}
	for _, e := range in.Events {
		if e.Account != "" && e.Account != "wise" && e.Account != "kyiv" {
			return Scenario{}, fmt.Errorf("event account must be wise or kyiv; omitted uses wise (USD cash)")
		}
		if e.Month < 1 || e.Month > 600 || math.IsInf(e.Amount, 0) || math.IsNaN(e.Amount) {
			return Scenario{}, fmt.Errorf("event month must be 1..600 and amount finite")
		}
	}
	in.Archived = false
	in.UserID = p.UserID
	in.Key = p.UserID + ":" + in.ID
	var before Scenario
	e := s.repo.db.Collection("scenarios").FindOne(ctx, bson.M{"user_id": p.UserID, "id": in.ID}).Decode(&before)
	if e != nil && !errors.Is(e, mongo.ErrNoDocuments) {
		return Scenario{}, e
	}
	if in.Legacy == nil {
		in.Legacy = before.Legacy
	}
	_, e = s.audit.Write(ctx, p, "scenario_upsert", audit.Target{Collection: "scenarios", ID: in.ID}, "", before, in, func(tx context.Context) error {
		_, e := s.repo.db.Collection("scenarios").ReplaceOne(tx, bson.M{"user_id": p.UserID, "id": in.ID}, in, options.Replace().SetUpsert(true))
		return e
	})
	return in, e
}
func (s *Service) SetIncome(ctx context.Context, p principal.Principal, streams []Income) ([]Income, error) {
	if e := p.Require("model:write"); e != nil {
		return nil, e
	}
	if len(streams) > 100 {
		return nil, fmt.Errorf("at most 100 income streams")
	}
	ids := map[string]bool{}
	for _, v := range streams {
		if v.ID == "" || ids[v.ID] || v.Amount < 0 || math.IsInf(v.Amount, 0) || math.IsNaN(v.Amount) || v.GrowthPct < -100 || math.IsInf(v.GrowthPct, 0) || math.IsNaN(v.GrowthPct) || (v.Currency != "USD" && v.Currency != "EUR") || (v.Frequency != "monthly" && v.Frequency != "biweekly" && v.Frequency != "yearly") {
			return nil, fmt.Errorf("income needs unique id, positive amount, USD/EUR and monthly/biweekly/yearly frequency")
		}
		ids[v.ID] = true
		for _, d := range []string{v.Start, v.End} {
			if d != "" {
				if _, e := time.Parse("2006-01-02", d); e != nil {
					return nil, fmt.Errorf("income dates must be YYYY-MM-DD")
				}
			}
		}
		if v.Start != "" && v.End != "" && v.Start > v.End {
			return nil, fmt.Errorf("income end precedes start")
		}
	}
	var before struct {
		Streams []Income `bson:"streams"`
	}
	e := s.repo.db.Collection("income_models").FindOne(ctx, bson.M{"_id": p.UserID}).Decode(&before)
	if e != nil && !errors.Is(e, mongo.ErrNoDocuments) {
		return nil, e
	}
	_, e = s.audit.Write(ctx, p, "income_model_set", audit.Target{Collection: "income_models", ID: p.UserID}, "", before.Streams, streams, func(tx context.Context) error {
		_, e := s.repo.db.Collection("income_models").ReplaceOne(tx, bson.M{"_id": p.UserID}, bson.M{"_id": p.UserID, "user_id": p.UserID, "streams": streams}, options.Replace().SetUpsert(true))
		return e
	})
	return streams, e
}
func (s *Service) Forecast(ctx context.Context, p principal.Principal, in ForecastInput) ([]Projection, error) {
	if in.HorizonMonths < 1 || in.HorizonMonths > 600 || len(in.ScenarioIDs) < 1 || len(in.ScenarioIDs) > 20 || (in.ValueIn != "" && in.ValueIn != "USD" && in.ValueIn != "EUR") {
		return nil, fmt.Errorf("1..20 scenarios, horizon 1..600 and USD/EUR value_in required")
	}
	state, e := s.Get(ctx, p)
	if e != nil {
		return nil, e
	}
	a := map[string]float64{}
	for _, r := range state.Assumptions {
		key, ok := r.Cells["key"].(string)
		if !ok {
			key = r.ID
		}
		switch v := r.Cells["value"].(type) {
		case float64:
			a[key] = v
		case int32:
			a[key] = float64(v)
		case int64:
			a[key] = float64(v)
		}
	}
	out := []Projection{}
	for _, id := range in.ScenarioIDs {
		var sc *Scenario
		for i := range state.Scenarios {
			if state.Scenarios[i].ID == id {
				sc = &state.Scenarios[i]
				break
			}
		}
		if sc == nil {
			return nil, principal.ErrNotFound
		}
		vars := map[string]float64{}
		for k, v := range a {
			vars[k] = v
		}
		for _, o := range sc.Overrides {
			if o.Value != nil {
				vars[o.Assumption] = *o.Value
			} else {
				r, e := s.expr.Eval(ctx, o.Expr, map[string]any{"params": vars})
				if e != nil {
					return nil, e
				}
				v, ok := r.Value.(float64)
				if !ok {
					return nil, fmt.Errorf("override must produce a number")
				}
				vars[o.Assumption] = v
			}
		}
		for _, key := range []string{"start_net_usd", "income_usd", "spend_eur", "eurusd"} {
			if _, ok := vars[key]; !ok {
				return nil, fmt.Errorf("missing assumption %s", key)
			}
		}
		if vars["eurusd"] <= 0 || vars["spend_eur"] < 0 {
			return nil, fmt.Errorf("positive EUR/USD and nonnegative spending required")
		}
		proj, e := Project(*sc, vars, state.Streams, in.HorizonMonths, time.Now())
		if e != nil {
			return nil, e
		}
		if in.ValueIn == "EUR" {
			rate := vars["eurusd"]
			for i := range proj.Months {
				proj.Months[i].Net = round(proj.Months[i].Net / rate)
				proj.Months[i].Cash = round(proj.Months[i].Cash / rate)
				proj.Months[i].Gold = round(proj.Months[i].Gold / rate)
				proj.Months[i].Wise = round(proj.Months[i].Wise / rate)
				proj.Months[i].Kyiv = round(proj.Months[i].Kyiv / rate)
			}
			proj.Currency = "EUR"
			proj.Spend = round(proj.SpendUSD / rate)
			proj.EndNet = round(proj.EndNetUSD / rate)
		}
		out = append(out, proj)
	}
	return out, nil
}
func round(v float64) float64 { return math.Round(v*100) / 100 }
func ptr(v float64) *float64  { return &v }
func Project(sc Scenario, a map[string]float64, streams []Income, horizon int, start time.Time) (Projection, error) {
	spend := a["spend_eur"] * a["eurusd"]
	income := a["income_usd"]
	reimb, extra, yield, buffer := 0.0, 0.0, 0.0, 0.0
	if sc.Legacy != nil {
		l := sc.Legacy
		spend = 0
		for _, v := range l.Spend {
			spend += v.USD
		}
		income = l.SalaryBiweeklyUSD*26/12 + l.SideIncomeMonthlyUSD
		reimb = l.ReimbursementBiweeklyUSD * 26 / 12
		extra = l.KyivExtraDrainMonthlyUSD
		yield = l.YieldPct
		buffer = l.BufferUSD
	}
	incomeOverride := false
	for _, o := range sc.Overrides {
		switch o.Assumption {
		case "spend_eur":
			spend = a["spend_eur"] * a["eurusd"]
		case "income_usd":
			income = a["income_usd"]
			incomeOverride = true
		}
	}
	gold := a["gold_g"] * a["gold_usd_g"]
	shock := gold * a["gold_shock_pct"] / 100
	gold += shock
	net := a["start_net_usd"] + shock
	wise := a["wise_usd"]
	kyiv := a["kyiv_usd"]
	stat := net - wise - kyiv
	out := Projection{Currency: "USD", Scenario: sc.ID, Name: sc.Name, SpendUSD: round(spend), Months: []Point{}}
	if spend > 0 {
		out.Runway = ptr(a["start_net_usd"] / spend)
	}
	for m := 0; m <= horizon; m++ {
		if m > 0 {
			monthIncome := income
			if len(streams) > 0 && !incomeOverride {
				monthIncome = 0
				d := start.AddDate(0, m, 0).Format("2006-01-02")
				for _, v := range streams {
					if v.Start != "" && d < v.Start || v.End != "" && d > v.End {
						continue
					}
					n := v.Amount
					switch v.Frequency {
					case "biweekly":
						n *= 26.0 / 12
					case "yearly":
						n /= 12
					}
					if v.Currency == "EUR" {
						n *= a["eurusd"]
					}
					monthIncome += n * math.Pow(1+v.GrowthPct/100, float64(m-1)/12)
				}
			}
			wise += monthIncome + reimb - spend
			fromKyiv := math.Min(reimb, kyiv)
			kyiv -= fromKyiv
			wise -= reimb - fromKyiv
			drain := math.Min(extra, kyiv)
			kyiv -= drain
			wise += drain
			if yield > 0 && wise > buffer {
				wise += (wise - buffer) * yield / 100 / 12
			}
			for _, e := range sc.Events {
				if e.Month == m {
					if e.Account == "kyiv" {
						kyiv += e.Amount
					} else {
						wise += e.Amount
					}
				}
			}
			if kyiv <= 0.005 && out.KyivEmptyMonth == 0 && a["kyiv_usd"] > 0 {
				out.KyivEmptyMonth = m
			}
		}
		net = wise + kyiv + stat
		if math.IsInf(net, 0) || math.IsNaN(net) {
			return Projection{}, fmt.Errorf("forecast produced a non-finite number")
		}
		point := Point{M: m, Net: round(net), Cash: round(net - gold), Gold: round(gold), Wise: round(wise), Kyiv: round(kyiv)}
		if spend > 0 {
			point.Runway = ptr(round(net / spend))
		}
		out.Months = append(out.Months, point)
	}
	out.EndNetUSD = round(net)
	out.Spend = out.SpendUSD
	out.EndNet = out.EndNetUSD
	return out, nil
}
