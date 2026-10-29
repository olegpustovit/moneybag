package budget

import (
	"context"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"math/big"
	"slices"
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
	// GroupRef maps every expense account whose groups[dimension] equals value, resolved on read.
	GroupRef *GroupRef `json:"group_ref,omitempty" bson:"group_ref,omitempty"`
	Order    int       `json:"order" bson:"order"`
}
type GroupRef struct {
	Dimension string `json:"dimension" bson:"dimension"`
	Value     string `json:"value" bson:"value"`
}
type SetInput struct {
	ID       string          `json:"id"`
	Limit    *ledger.Decimal `json:"limit,omitempty"`
	Name     *string         `json:"name,omitempty"`
	Accounts *[]string       `json:"accounts,omitempty"`
	// GroupRef replaces the literal accounts list by an account group; an empty dimension clears it.
	GroupRef *GroupRef `json:"group_ref,omitempty"`
	Order    *int      `json:"order,omitempty"`
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
	Month  string       `json:"month"`
	Window Window       `json:"window"`
	Lines  []StatusLine `json:"lines"`
	Total  StatusLine   `json:"total"`
	// Unmapped lists open expense accounts that no budget line covers.
	Unmapped []string `json:"unmapped"`
}

// Window selects the months averaged by budget_get. Zero Months means 6; without IncludeCurrent the window ends before the selected month.
type Window struct {
	Months         int    `json:"months,omitempty"`
	IncludeCurrent bool   `json:"include_current,omitempty"`
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	Label          string `json:"label,omitempty"`
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
	for i, l := range out {
		if l.GroupRef == nil || e != nil {
			continue
		}
		found, err := s.groupAccounts(ctx, p, *l.GroupRef)
		if err != nil {
			return nil, err
		}
		out[i].Accounts = mergeAccounts(l.Accounts, found)
	}
	return out, e
}

type accountID struct {
	ID string `bson:"id"`
}

func (s *Service) groupAccounts(ctx context.Context, p principal.Principal, ref GroupRef) ([]accountID, error) {
	var found []accountID
	if all, ok, err := ledger.CachedAccounts(ctx, s.repo.db, p); ok {
		for _, a := range all {
			if a.Type == "expense" && a.ClosedAt == nil && a.Groups[ref.Dimension] == ref.Value {
				found = append(found, accountID{a.ID})
			}
		}
		return found, err
	}
	c, err := s.repo.db.Collection("accounts").Find(ctx, bson.M{"user_id": p.UserID, "type": "expense", "closed_at": bson.M{"$exists": false}, "groups." + ref.Dimension: ref.Value}, options.Find().SetSort(bson.D{{Key: "id", Value: 1}}))
	if err == nil {
		err = c.All(ctx, &found)
	}
	return found, err
}
func mergeAccounts(base []string, found []accountID) []string {
	out := slices.Clone(base)
	for _, a := range found {
		if !slices.Contains(out, a.ID) {
			out = append(out, a.ID)
		}
	}
	return out
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
	if in.GroupRef != nil {
		line.GroupRef = in.GroupRef
		if in.GroupRef.Dimension == "" {
			line.GroupRef = nil
		} else if !ledger.ValidateGroupKey(in.GroupRef.Dimension) || strings.TrimSpace(in.GroupRef.Value) == "" {
			return SetResult{}, fmt.Errorf("group_ref needs a valid dimension and a value")
		}
	}
	if in.Order != nil {
		line.Order = *in.Order
	}
	id, e := s.audit.Write(ctx, p, "budget_set_line", audit.Target{Collection: "budgets", ID: in.ID}, "", old, line, func(tx context.Context) error {
		mine := line.Accounts
		if line.GroupRef != nil {
			found, e := s.groupAccounts(tx, p, *line.GroupRef)
			if e != nil {
				return e
			}
			mine = mergeAccounts(mine, found)
		}
		others, e := s.Lines(tx, p) // resolved, so group_ref lines claim their accounts too
		if e != nil {
			return e
		}
		for _, a := range mine {
			n, e := s.repo.db.Collection("accounts").CountDocuments(tx, bson.M{"user_id": p.UserID, "id": a, "type": "expense"})
			if e != nil {
				return e
			}
			if n != 1 {
				return fmt.Errorf("budget mapping must reference an owned expense account")
			}
			for _, o := range others {
				if o.ID != line.ID && slices.Contains(o.Accounts, a) {
					return fmt.Errorf("expense account is already mapped to another budget line")
				}
			}
		}
		_, e = s.repo.db.Collection("budgets").ReplaceOne(tx, bson.M{"user_id": p.UserID, "id": in.ID}, line, options.Replace().SetUpsert(true))
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
	return s.GetWindow(ctx, p, month, Window{})
}
func (s *Service) GetWindow(ctx context.Context, p principal.Principal, month string, w Window) (Status, error) {
	ctx = ledger.WithAccountCache(rates.WithQuoteCache(reqcache.With(ctx))) // quotes and accounts load once per request
	if w.Months == 0 {
		w.Months = 6
	}
	if w.Months < 1 || w.Months > 36 {
		return Status{}, fmt.Errorf("window months must be 1..36")
	}
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
	first := date.AddDate(0, -w.Months, 0)
	if w.IncludeCurrent {
		first = first.AddDate(0, 1, 0)
	}
	last := date.AddDate(0, 1, -1)
	if !w.IncludeCurrent {
		last = date.AddDate(0, 0, -1)
	}
	w.From, w.To = first.Format("2006-01-02"), last.Format("2006-01-02")
	w.Label = first.Format("Jan") + "–" + last.Format("Jan")
	if w.Months == 1 {
		w.Label = first.Format("Jan")
	}
	// Fetch through the selected month so its actual is available even when the window excludes it.
	months, e := s.Months(ctx, p, w.From, date.AddDate(0, 1, -1).Format("2006-01-02"))
	if e != nil {
		return Status{}, e
	}
	out := Status{Month: month, Window: w, Unmapped: []string{}, Lines: []StatusLine{}, Total: StatusLine{ID: "total", Name: "Total", Status: "within"}}
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
			}
			if m.Month >= w.From[:7] && m.Month <= w.To[:7] {
				v := new(big.Rat)
				v.SetFloat64(m.ByLine[l.ID])
				avg.Add(avg, v)
			}
		}
		r.Avg6, _ = avg.Quo(avg, big.NewRat(int64(w.Months), 1)).Float64()
		if r.Actual > r.Limit {
			r.Status = "over"
		}
		out.Lines = append(out.Lines, r)
		out.Total.Limit += r.Limit
		out.Total.Actual += r.Actual
		out.Total.Avg6 += r.Avg6
	}
	mapped := map[string]bool{}
	for _, l := range lines {
		for _, a := range l.Accounts {
			mapped[a] = true
		}
	}
	var expenses []accountID
	if all, ok, e := ledger.CachedAccounts(ctx, s.repo.db, p); ok {
		if e != nil {
			return Status{}, e
		}
		for _, a := range all {
			if a.Type == "expense" && a.ClosedAt == nil {
				expenses = append(expenses, accountID{a.ID})
			}
		}
	} else {
		cur, e := s.repo.db.Collection("accounts").Find(ctx, bson.M{"user_id": p.UserID, "type": "expense", "closed_at": bson.M{"$exists": false}}, options.Find().SetSort(bson.D{{Key: "id", Value: 1}}))
		if e != nil {
			return Status{}, e
		}
		if e = cur.All(ctx, &expenses); e != nil {
			return Status{}, e
		}
	}
	for _, a := range expenses {
		if !mapped[a.ID] {
			out.Unmapped = append(out.Unmapped, a.ID)
		}
	}
	if out.Total.Actual > out.Total.Limit {
		out.Total.Status = "over"
	}
	return out, nil
}
