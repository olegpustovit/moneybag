package budget

import (
	"context"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"math/big"
	"strings"
	"time"
)

type Line struct {
	Archived bool           `json:"archived,omitempty" bson:"archived,omitempty"`
	Key      string         `json:"-" bson:"_id"`
	UserID   string         `json:"-" bson:"user_id"`
	ID       string         `json:"id" bson:"id"`
	Name     string         `json:"name" bson:"name"`
	Limit    ledger.Decimal `json:"limit" bson:"limit"`
	Accounts []string       `json:"accounts" bson:"accounts"`
	Order    int            `json:"order" bson:"order"`
}
type SetInput struct {
	ID       string          `json:"id"`
	Limit    *ledger.Decimal `json:"limit,omitempty"`
	Name     *string         `json:"name,omitempty"`
	Accounts *[]string       `json:"accounts,omitempty"`
	Order    *int            `json:"order,omitempty"`
}
type SetResult struct {
	Line    Line   `json:"line"`
	EventID string `json:"event_id"`
}
type StatusLine struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Limit    float64  `json:"limit"`
	Actual   float64  `json:"actual"`
	Avg6     float64  `json:"avg6"`
	Status   string   `json:"status"`
	Accounts []string `json:"accounts"`
}
type Status struct {
	Month string       `json:"month"`
	Lines []StatusLine `json:"lines"`
	Total StatusLine   `json:"total"`
}
type Month struct {
	Month  string             `json:"month"`
	ByLine map[string]float64 `json:"byLine"`
	Total  float64            `json:"total"`
}
type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo   *MongoRepository
	audit  *audit.Service
	valuer ledger.Valuer
}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func NewService(r *MongoRepository, a *audit.Service, v ledger.Valuer) *Service {
	return &Service{r, a, v}
}
func (s *Service) Lines(ctx context.Context, p principal.Principal) ([]Line, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	cur, e := s.repo.db.Collection("budgets").Find(ctx, bson.M{"user_id": p.UserID, "archived": bson.M{"$ne": true}}, options.Find().SetSort(bson.D{{Key: "order", Value: 1}, {Key: "id", Value: 1}}).SetLimit(501))
	if e != nil {
		return nil, e
	}
	defer cur.Close(ctx)
	out := []Line{}
	e = cur.All(ctx, &out)
	if len(out) > 500 {
		return nil, fmt.Errorf("too many budget lines")
	}
	return out, e
}
func (s *Service) Set(ctx context.Context, p principal.Principal, in SetInput) (SetResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return SetResult{}, e
	}
	if in.ID == "" || len(in.ID) > 100 {
		return SetResult{}, fmt.Errorf("line id required")
	}
	var old Line
	e := s.repo.db.Collection("budgets").FindOne(ctx, bson.M{"user_id": p.UserID, "id": in.ID}).Decode(&old)
	if e != nil && !errors.Is(e, mongo.ErrNoDocuments) {
		return SetResult{}, e
	}
	line := old
	line.Archived = false
	if errors.Is(e, mongo.ErrNoDocuments) {
		line = Line{Key: p.UserID + ":" + in.ID, UserID: p.UserID, ID: in.ID, Name: in.ID, Limit: ledger.MustDecimal("0"), Accounts: []string{}}
	}
	if in.Limit != nil {
		if in.Limit.Rat().Sign() < 0 {
			return SetResult{}, fmt.Errorf("budget limit cannot be negative")
		}
		line.Limit = *in.Limit
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 200 {
			return SetResult{}, fmt.Errorf("name must be 1..200 characters")
		}
		line.Name = *in.Name
	}
	if in.Accounts != nil {
		if len(*in.Accounts) > 100 {
			return SetResult{}, fmt.Errorf("at most 100 accounts")
		}
		line.Accounts = *in.Accounts
	}
	if in.Order != nil {
		line.Order = *in.Order
	}
	id, e := s.audit.Write(ctx, p, "budget_set_line", audit.Target{Collection: "budgets", ID: in.ID}, "", old, line, func(tx context.Context) error {
		for _, a := range line.Accounts {
			n, e := s.repo.db.Collection("accounts").CountDocuments(tx, bson.M{"user_id": p.UserID, "id": a, "type": "expense"})
			if e != nil {
				return e
			}
			if n != 1 {
				return fmt.Errorf("budget mapping must reference an owned expense account")
			}
			n, e = s.repo.db.Collection("budgets").CountDocuments(tx, bson.M{"user_id": p.UserID, "id": bson.M{"$ne": line.ID}, "accounts": a, "archived": bson.M{"$ne": true}})
			if e != nil {
				return e
			}
			if n > 0 {
				return fmt.Errorf("expense account is already mapped to another budget line")
			}
		}
		_, e := s.repo.db.Collection("budgets").ReplaceOne(tx, bson.M{"user_id": p.UserID, "id": in.ID}, line, options.Replace().SetUpsert(true))
		return e
	})
	return SetResult{line, id}, e
}
func (s *Service) Months(ctx context.Context, p principal.Principal, from, to string) ([]Month, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	lines, e := s.Lines(ctx, p)
	if e != nil {
		return nil, e
	}
	mapping := map[string]string{}
	for _, l := range lines {
		for _, a := range l.Accounts {
			mapping[a] = l.ID
		}
	}
	match := bson.M{"user_id": p.UserID, "status": "posted", "date": bson.M{"$gte": from, "$lte": to}}
	cur, e := s.repo.db.Collection("journal").Aggregate(ctx, bson.A{bson.M{"$match": match}, bson.M{"$unwind": "$postings"}, bson.M{"$match": bson.M{"postings.account": bson.M{"$regex": "^expenses:"}}}, bson.M{"$group": bson.M{"_id": bson.M{"date": "$date", "account": "$postings.account", "commodity": "$postings.amount.c", "price": "$postings.price"}, "amount": bson.M{"$sum": "$postings.amount.v"}}}, bson.M{"$limit": 5001}})
	if e != nil {
		return nil, e
	}
	defer cur.Close(ctx)
	var rows []struct {
		ID struct {
			Date      string         `bson:"date"`
			Account   string         `bson:"account"`
			Commodity string         `bson:"commodity"`
			Price     *ledger.Amount `bson:"price"`
		} `bson:"_id"`
		Amount ledger.Decimal `bson:"amount"`
	}
	if e = cur.All(ctx, &rows); e != nil {
		return nil, e
	}
	if len(rows) > 5000 {
		return nil, fmt.Errorf("monthly query exceeds 5000 rows")
	}
	months := map[string]*Month{}
	for _, r := range rows {
		month := r.ID.Date[:7]
		amount := r.Amount
		if r.ID.Commodity != "EUR" {
			if r.ID.Price != nil && r.ID.Price.Commodity == "EUR" {
				v, e := ledger.DecimalFromRat(new(big.Rat).Mul(amount.Rat(), r.ID.Price.Value.Rat()))
				if e != nil {
					return nil, e
				}
				amount = v
			} else {
				if s.valuer == nil {
					return nil, fmt.Errorf("historical valuation is required for %s expenses", r.ID.Commodity)
				}
				v, e := s.valuer.Convert(ctx, p, ledger.Amount{Value: amount, Commodity: r.ID.Commodity}, "EUR", r.ID.Date+"T23:59:59Z")
				if e != nil {
					return nil, e
				}
				amount = v.Value
			}
		}
		m := months[month]
		if m == nil {
			m = &Month{Month: month, ByLine: map[string]float64{}}
			months[month] = m
		}
		line := mapping[r.ID.Account]
		if line == "" {
			line = "unmapped"
		}
		m.ByLine[line] += amount.Float()
		m.Total += amount.Float()
	}
	start, e := time.Parse("2006-01-02", from)
	if e != nil {
		return nil, e
	}
	end, e := time.Parse("2006-01-02", to)
	if e != nil {
		return nil, e
	}
	out := []Month{}
	for d := start; !d.After(end); d = d.AddDate(0, 1, 0) {
		key := d.Format("2006-01")
		if m := months[key]; m != nil {
			out = append(out, *m)
		} else {
			out = append(out, Month{Month: key, ByLine: map[string]float64{}})
		}
		if len(out) > 600 {
			return nil, fmt.Errorf("at most 600 months")
		}
	}
	return out, nil
}
func (s *Service) Get(ctx context.Context, p principal.Principal, month string) (Status, error) {
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	date, e := time.Parse("2006-01", month)
	if e != nil {
		return Status{}, fmt.Errorf("month must be YYYY-MM")
	}
	lines, e := s.Lines(ctx, p)
	if e != nil {
		return Status{}, e
	}
	months, e := s.Months(ctx, p, date.AddDate(0, -6, 0).Format("2006-01-02"), date.AddDate(0, 1, -1).Format("2006-01-02"))
	if e != nil {
		return Status{}, e
	}
	out := Status{Month: month, Lines: []StatusLine{}, Total: StatusLine{ID: "total", Name: "Total", Status: "within"}}
	for _, m := range months {
		if m.ByLine["unmapped"] != 0 {
			lines = append(lines, Line{ID: "unmapped", Name: "Unmapped expenses", Limit: ledger.MustDecimal("0"), Accounts: []string{}})
			break
		}
	}
	for _, l := range lines {
		r := StatusLine{ID: l.ID, Name: l.Name, Limit: l.Limit.Float(), Accounts: l.Accounts, Status: "within"}
		avg := new(big.Rat)
		for _, m := range months {
			if m.Month == month {
				r.Actual = m.ByLine[l.ID]
			} else {
				v := new(big.Rat)
				v.SetFloat64(m.ByLine[l.ID])
				avg.Add(avg, v)
			}
		}
		r.Avg6, _ = avg.Quo(avg, big.NewRat(6, 1)).Float64()
		if r.Actual > r.Limit {
			r.Status = "over"
		}
		out.Lines = append(out.Lines, r)
		out.Total.Limit += r.Limit
		out.Total.Actual += r.Actual
		out.Total.Avg6 += r.Avg6
	}
	if out.Total.Actual > out.Total.Limit {
		out.Total.Status = "over"
	}
	return out, nil
}
