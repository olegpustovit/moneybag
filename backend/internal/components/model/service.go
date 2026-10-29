package model

import (
	"context"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
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
	// Ref resolves a model input at forecast time: budget_line:<id>, budget_lines.total or income_stream:<id>. The assumption name must end in _usd or _eur; the value is converted to that currency with the live EUR/USD rate.
	Ref    string   `json:"ref,omitempty" bson:"ref,omitempty" jsonschema:"model input: budget_line:<id> (limit), budget_lines.total or income_stream:<id> (monthly amount); assumption must end in _usd or _eur, converted with the live EUR/USD rate"`
	Factor *float64 `json:"factor,omitempty" bson:"factor,omitempty" jsonschema:"multiplier applied to the resolved ref value"`
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
	Archived         bool          `json:"archived,omitempty" bson:"archived,omitempty"`
	Key              string        `json:"-" bson:"_id"`
	UserID           string        `json:"-" bson:"user_id"`
	ID               string        `json:"id" bson:"id"`
	Name             string        `json:"name" bson:"name"`
	Revision         int           `json:"revision,omitempty" bson:"revision"`
	ExpectedRevision *int          `json:"expected_revision,omitempty" bson:"-" jsonschema:"optional revision from model_get; -1 requires a new scenario"`
	Overrides        []Override    `json:"overrides" bson:"overrides"`
	Events           []Event       `json:"events,omitempty" bson:"events,omitempty"`
	Legacy           *LegacyInputs `json:"legacy,omitempty" bson:"legacy,omitempty"`
	// DropLegacy removes a stored legacy block; omitting legacy otherwise keeps it.
	DropLegacy bool `json:"drop_legacy,omitempty" bson:"-" jsonschema:"true removes the stored legacy block; the scenario then uses assumptions and income streams"`
}
type Income struct {
	ID        string  `json:"id" bson:"id"`
	Amount    float64 `json:"amount" bson:"amount"`
	Currency  string  `json:"currency" bson:"currency"`
	Frequency string  `json:"frequency" bson:"frequency"`
	Start     string  `json:"start,omitempty" bson:"start,omitempty"`
	End       string  `json:"end,omitempty" bson:"end,omitempty"`
	GrowthPct float64 `json:"growth_pct,omitempty" bson:"growth_pct,omitempty"`
	// Deprecated: use Kind "reimbursement". Still read so stored streams keep working.
	Reimbursement bool `json:"reimbursement,omitempty" bson:"reimbursement,omitempty" jsonschema:"deprecated; use kind reimbursement"`
	// Kind is salary, side or reimbursement. Reimbursement streams repay spend already paid from Kyiv cash, so they net against Kyiv instead of adding wealth.
	Kind string `json:"kind,omitempty" bson:"kind,omitempty" jsonschema:"salary, side or reimbursement; reimbursement is never counted as income"`
	// PayerMatch is a CEL expression over row.payee and row.tags selecting the entries that pay this stream.
	PayerMatch string `json:"payer_match,omitempty" bson:"payer_match,omitempty" jsonschema:"CEL over row.payee and row.tags selecting entries that pay this stream, e.g. row.payee == \"Acme\""`
	// LandsIn is the account id receiving the money; without it actuals are read from income-type accounts.
	LandsIn    string `json:"lands_in,omitempty" bson:"lands_in,omitempty" jsonschema:"account id receiving the money; empty reads income-type accounts"`
	GrossOrNet string `json:"gross_or_net,omitempty" bson:"gross_or_net,omitempty" jsonschema:"gross or net"`
}

func (v Income) IsReimbursement() bool { return v.Kind == "reimbursement" || v.Reimbursement }

type State struct {
	Assumptions []tables.Row `json:"assumptions"`
	Scenarios   []Scenario   `json:"scenarios"`
	Streams     []Income     `json:"streams"`
}
type ForecastInput struct {
	ScenarioIDs   []string `json:"scenario_ids" jsonschema:"1..20 scenario ids"`
	HorizonMonths int      `json:"horizon_months" jsonschema:"1..600"`
	ValueIn       string   `json:"value_in,omitempty" jsonschema:"USD (default) or EUR"`
	AsOf          string   `json:"as_of,omitempty" jsonschema:"start date YYYY-MM-DD for the live ledger start; default today in Europe/Zagreb"`
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
	SavingsUSDMo   float64  `json:"savings_usd_mo"`
	RunwayExGold   *float64 `json:"runway_ex_gold"`
	Provenance
}
type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo   *MongoRepository
	tables *tables.Service
	audit  *audit.Service
	expr   *expressions.Engine
	// Optional live-start dependencies; without Ledger and Rates forecasts use the stored assumptions.
	Ledger *ledger.Service
	Rates  *rates.Service
	Budget *budget.Service
	// Views reads a saved view's rows; it is a func because dashboard (which owns views) imports model.
	Views func(ctx context.Context, p principal.Principal, id string) ([]map[string]any, error)
}

// EngineAssumptions are the names Project reads; scenarios may override them without a stored row.
var EngineAssumptions = []string{"start_net_usd", "wise_usd", "kyiv_usd", "gold_g", "gold_usd_g", "eurusd", "income_usd", "spend_eur", "kyiv_extra_drain_usd", "yield_pct", "buffer_usd", "gold_shock_pct", "horizon"}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func NewService(r *MongoRepository, t *tables.Service, a *audit.Service, e *expressions.Engine) *Service {
	return &Service{repo: r, tables: t, audit: a, expr: e}
}
func (s *Service) Get(ctx context.Context, p principal.Principal) (State, error) {
	if e := p.Require("ledger:read"); e != nil {
		return State{}, e
	}
	return reqcache.Do(ctx, "model:"+p.UserID, func() (State, error) { return s.load(ctx, p) })
}
func (s *Service) load(ctx context.Context, p principal.Principal) (State, error) {
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
		n := 0
		for _, set := range []bool{o.Value != nil, o.Expr != "", o.Ref != ""} {
			if set {
				n++
			}
		}
		if o.Assumption == "" || n != 1 {
			return Scenario{}, fmt.Errorf("each override needs assumption and exactly one value, expr or ref")
		}
		if o.Ref != "" {
			if e := validRef(o); e != nil {
				return Scenario{}, e
			}
		} else if o.Factor != nil {
			return Scenario{}, fmt.Errorf("factor applies only to ref overrides")
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
	err := s.audit.Workflow(ctx, func(tx context.Context) error {
		collection := s.repo.db.Collection("scenarios")
		filter := bson.M{"user_id": p.UserID, "id": in.ID}
		var before Scenario
		err := collection.FindOne(tx, filter).Decode(&before)
		exists := err == nil
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		if in.ExpectedRevision != nil && ((!exists && *in.ExpectedRevision != -1) || (exists && *in.ExpectedRevision != before.Revision)) {
			return principal.ErrConflict
		}
		if in.Legacy == nil && !in.DropLegacy {
			in.Legacy = before.Legacy
		}
		in.DropLegacy = false
		in.ExpectedRevision = nil
		in.Revision = before.Revision + 1
		_, err = s.audit.Write(tx, p, "scenario_upsert", audit.Target{Collection: "scenarios", ID: in.ID}, "", before, in, func(tx context.Context) error {
			if !exists {
				_, err := collection.InsertOne(tx, in)
				if mongo.IsDuplicateKeyError(err) {
					return principal.ErrConflict
				}
				return err
			}
			if before.Revision == 0 {
				filter["$or"] = []bson.M{{"revision": 0}, {"revision": bson.M{"$exists": false}}}
			} else {
				filter["revision"] = before.Revision
			}
			result, err := collection.ReplaceOne(tx, filter, in)
			if err == nil && result.MatchedCount == 0 {
				return principal.ErrConflict
			}
			return err
		})
		return err
	})
	return in, err
}
func (s *Service) SetIncome(ctx context.Context, p principal.Principal, streams []Income) ([]Income, error) {
	if e := p.Require("model:write"); e != nil {
		return nil, e
	}
	if len(streams) > 100 {
		return nil, fmt.Errorf("at most 100 income streams")
	}
	ids := map[string]bool{}
	for i, v := range streams {
		if v.ID == "" || ids[v.ID] || v.Amount < 0 || math.IsInf(v.Amount, 0) || math.IsNaN(v.Amount) || v.GrowthPct < -100 || math.IsInf(v.GrowthPct, 0) || math.IsNaN(v.GrowthPct) || (v.Currency != "USD" && v.Currency != "EUR") || (v.Frequency != "monthly" && v.Frequency != "biweekly" && v.Frequency != "yearly") {
			return nil, fmt.Errorf("income needs unique id, positive amount, USD/EUR and monthly/biweekly/yearly frequency")
		}
		ids[v.ID] = true
		if v.Kind != "" && v.Kind != "salary" && v.Kind != "side" && v.Kind != "reimbursement" || v.GrossOrNet != "" && v.GrossOrNet != "gross" && v.GrossOrNet != "net" {
			return nil, fmt.Errorf("income kind must be salary, side or reimbursement and gross_or_net gross or net")
		}
		if v.PayerMatch != "" {
			if _, e := s.expr.Compile(v.PayerMatch); e != nil {
				return nil, fmt.Errorf("payer_match: %w", e)
			}
		}
		if v.Kind == "" && v.Reimbursement {
			streams[i].Kind = "reimbursement"
		}
		streams[i].Reimbursement = streams[i].Kind == "reimbursement"
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
	ctx = ledger.WithAccountCache(rates.WithQuoteCache(reqcache.With(ctx))) // quotes and accounts load once per request
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
	live, prov, e := s.liveStart(ctx, p, in.AsOf)
	if e != nil {
		return nil, e
	}
	for k, v := range live {
		a[k] = v
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
		pr := prov
		for _, o := range sc.Overrides {
			if o.Value != nil {
				vars[o.Assumption] = *o.Value
			} else if o.Ref != "" {
				v, e := s.resolveRef(ctx, p, o, state.Streams, vars["eurusd"])
				if e != nil {
					return nil, e
				}
				vars[o.Assumption] = v
				pr.Inputs = append(append([]ResolvedInput{}, pr.Inputs...), ResolvedInput{o.Assumption, o.Ref, v})
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
		required := []string{"start_net_usd", "eurusd"}
		if sc.Legacy == nil {
			required = append(required, "spend_eur")
			if len(state.Streams) == 0 {
				required = append(required, "income_usd")
			}
		}
		for _, key := range required {
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
		pr.StartNetUSD = round(vars["start_net_usd"])
		proj.Provenance = pr
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
	reimb, extra, yield, buffer := 0.0, a["kyiv_extra_drain_usd"], a["yield_pct"], a["buffer_usd"]
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
		case "kyiv_extra_drain_usd":
			extra = a["kyiv_extra_drain_usd"]
		case "yield_pct":
			yield = a["yield_pct"]
		case "buffer_usd":
			buffer = a["buffer_usd"]
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
		out.RunwayExGold = ptr(round((a["start_net_usd"] - a["gold_g"]*a["gold_usd_g"]) / spend))
	}
	for m := 0; m <= horizon; m++ {
		if m > 0 {
			monthIncome, monthReimb := income, reimb
			if len(streams) > 0 && !incomeOverride {
				monthIncome, monthReimb = 0, 0
				d := start.AddDate(0, m, 0).Format("2006-01-02")
				for _, v := range streams {
					if v.Start != "" && d < v.Start || v.End != "" && d > v.End {
						continue
					}
					n := v.Amount
					if o, ok := a[v.ID]; ok { // a scenario override named after the stream wins, even when zero
						n = o
					}
					switch v.Frequency {
					case "biweekly":
						n *= 26.0 / 12
					case "yearly":
						n /= 12
					}
					if v.Currency == "EUR" {
						n *= a["eurusd"]
					}
					n *= math.Pow(1+v.GrowthPct/100, float64(m-1)/12)
					if v.IsReimbursement() {
						monthReimb += n
					} else {
						monthIncome += n
					}
				}
			}
			if m == 1 {
				out.SavingsUSDMo = round(monthIncome - spend)
			}
			wise += monthIncome + monthReimb - spend
			fromKyiv := math.Min(monthReimb, kyiv)
			kyiv -= fromKyiv
			wise -= monthReimb - fromKyiv
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
