package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"math/big"
	"sort"
	"strings"
	"time"
)

type Assertion struct {
	ID      string         `json:"id" bson:"_id"`
	UserID  string         `json:"-" bson:"user_id"`
	Account string         `json:"account" bson:"account"`
	Date    string         `json:"date" bson:"date"`
	Amount  ledger.Amount  `json:"amount" bson:"amount"`
	Source  string         `json:"source" bson:"source"`
	OK      bool           `json:"ok" bson:"ok"`
	Diff    ledger.Decimal `json:"diff" bson:"diff"`
}
type Discrepancy struct {
	ID           string        `json:"id" bson:"_id"`
	UserID       string        `json:"-" bson:"user_id"`
	AssertionID  string        `json:"assertion_id" bson:"assertion_id"`
	Account      string        `json:"account" bson:"account"`
	Diff         ledger.Amount `json:"diff" bson:"diff"`
	Status       string        `json:"status" bson:"status"`
	Resolution   string        `json:"resolution,omitempty" bson:"resolution,omitempty"`
	Reason       string        `json:"reason,omitempty" bson:"reason,omitempty"`
	AdjustmentID string        `json:"adjustment_id,omitempty" bson:"adjustment_id,omitempty"`
}
type AssertInput struct {
	Account string        `json:"account"`
	Date    string        `json:"date"`
	Amount  ledger.Amount `json:"amount"`
	Source  string        `json:"source"`
}
type AssertResult struct {
	Assertion Assertion      `json:"assertion"`
	OK        bool           `json:"ok"`
	Diff      ledger.Decimal `json:"diff"`
}
type AccountStatus struct {
	Account       string        `json:"account"`
	LastOK        string        `json:"last_ok"`
	Discrepancies []Discrepancy `json:"open_discrepancies"`
}
type ResolveInput struct {
	DiscrepancyID string   `json:"discrepancy_id"`
	Action        string   `json:"action"`
	EntryIDs      []string `json:"entry_ids,omitempty"`
	Reason        string   `json:"reason"`
}
type ResolveResult struct {
	Resolved   bool          `json:"resolved"`
	Adjustment *ledger.Entry `json:"adjustment_entry,omitempty"`
}
type Candidate struct {
	Kind     string         `json:"kind"`
	Entries  []ledger.Entry `json:"entries"`
	Explains string         `json:"explains"`
}
type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo   *MongoRepository
	ledger *ledger.Service
	audit  *audit.Service
}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func NewService(r *MongoRepository, l *ledger.Service, a *audit.Service) *Service {
	return &Service{r, l, a}
}
func (s *Service) balance(ctx context.Context, p principal.Principal, account, date, commodity string) (ledger.Decimal, error) {
	b, e := s.ledger.Balances(ctx, p, ledger.BalancesInput{At: date, Accounts: []string{account}}, nil)
	if e != nil {
		return ledger.Decimal{}, e
	}
	for _, row := range b.Rows {
		for _, a := range row.Amounts {
			if a.Commodity == commodity {
				return a.Value, nil
			}
		}
	}
	return ledger.MustDecimal("0"), nil
}
func (s *Service) Assert(ctx context.Context, p principal.Principal, in AssertInput) (AssertResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return AssertResult{}, e
	}
	a, e := s.ledger.Account(ctx, p, in.Account)
	if e != nil {
		return AssertResult{}, e
	}
	allowed := false
	for _, c := range a.Commodities {
		if c == in.Amount.Commodity {
			allowed = true
		}
	}
	if !allowed || strings.TrimSpace(in.Source) == "" {
		return AssertResult{}, fmt.Errorf("declared account commodity and source required")
	}
	actual, e := s.balance(ctx, p, in.Account, in.Date, in.Amount.Commodity)
	if e != nil {
		return AssertResult{}, e
	}
	diff, e := ledger.DecimalFromRat(new(big.Rat).Sub(in.Amount.Value.Rat(), actual.Rat()))
	if e != nil {
		return AssertResult{}, e
	}
	key := p.UserID + ":" + in.Account + ":" + in.Date + ":" + in.Amount.Commodity
	assertion := Assertion{ID: key, UserID: p.UserID, Account: in.Account, Date: in.Date, Amount: in.Amount, Source: in.Source, OK: diff.Rat().Sign() == 0, Diff: diff}
	var old Assertion
	e = s.repo.db.Collection("balance_assertions").FindOne(ctx, bson.M{"_id": key, "user_id": p.UserID}).Decode(&old)
	if e != nil && !errors.Is(e, mongo.ErrNoDocuments) {
		return AssertResult{}, e
	}
	_, e = s.audit.Write(ctx, p, "balance_assert", audit.Target{Collection: "balance_assertions", ID: key}, in.Source, old, assertion, func(tx context.Context) error {
		if _, e := s.repo.db.Collection("balance_assertions").ReplaceOne(tx, bson.M{"_id": key, "user_id": p.UserID}, assertion, options.Replace().SetUpsert(true)); e != nil {
			return e
		}
		if assertion.OK {
			_, e := s.repo.db.Collection("discrepancies").UpdateMany(tx, bson.M{"user_id": p.UserID, "assertion_id": key, "status": "open"}, bson.M{"$set": bson.M{"status": "resolved", "resolution": "balance now matches"}})
			return e
		}
		d := Discrepancy{ID: key, UserID: p.UserID, AssertionID: key, Account: in.Account, Diff: ledger.Amount{Value: diff, Commodity: in.Amount.Commodity}, Status: "open"}
		_, e := s.repo.db.Collection("discrepancies").ReplaceOne(tx, bson.M{"_id": key, "user_id": p.UserID}, d, options.Replace().SetUpsert(true))
		return e
	})
	return AssertResult{assertion, assertion.OK, diff}, e
}
func (s *Service) Status(ctx context.Context, p principal.Principal, account string) ([]AccountStatus, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	// Backdated entries, reversals and import undos change past balances; recheck so status is current.
	// shortcut: read-only callers see the last nightly verification, upgrade if that matters to them.
	if p.Require("ledger:write") == nil {
		if e := s.verify(ctx, p, account); e != nil {
			return nil, e
		}
	}
	f := bson.M{"user_id": p.UserID, "archived": bson.M{"$ne": true}}
	if account != "" {
		f["account"] = account
	}
	cur, e := s.repo.db.Collection("balance_assertions").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "date", Value: -1}}).SetLimit(5001))
	if e != nil {
		return nil, e
	}
	defer cur.Close(ctx)
	var assertions []Assertion
	if e = cur.All(ctx, &assertions); e != nil {
		return nil, e
	}
	if len(assertions) > 5000 {
		return nil, fmt.Errorf("too many assertions; select one account")
	}
	byAccount := map[string]*AccountStatus{}
	for _, a := range assertions {
		r := byAccount[a.Account]
		if r == nil {
			r = &AccountStatus{Account: a.Account, Discrepancies: []Discrepancy{}}
			byAccount[a.Account] = r
		}
		if a.OK && r.LastOK == "" {
			r.LastOK = a.Date
		}
	}
	f["status"] = "open"
	cur, e = s.repo.db.Collection("discrepancies").Find(ctx, f, options.Find().SetLimit(5001))
	if e != nil {
		return nil, e
	}
	defer cur.Close(ctx)
	var ds []Discrepancy
	if e = cur.All(ctx, &ds); e != nil {
		return nil, e
	}
	if len(ds) > 5000 {
		return nil, fmt.Errorf("too many discrepancies")
	}
	for _, d := range ds {
		r := byAccount[d.Account]
		if r == nil {
			r = &AccountStatus{Account: d.Account, Discrepancies: []Discrepancy{}}
			byAccount[d.Account] = r
		}
		r.Discrepancies = append(r.Discrepancies, d)
	}
	out := []AccountStatus{}
	for _, r := range byAccount {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	return out, nil
}
func (s *Service) discrepancy(ctx context.Context, p principal.Principal, id string) (Discrepancy, Assertion, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Discrepancy{}, Assertion{}, e
	}
	var d Discrepancy
	e := s.repo.db.Collection("discrepancies").FindOne(ctx, bson.M{"_id": id, "user_id": p.UserID}).Decode(&d)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return d, Assertion{}, principal.ErrNotFound
	}
	if e != nil {
		return d, Assertion{}, e
	}
	var a Assertion
	e = s.repo.db.Collection("balance_assertions").FindOne(ctx, bson.M{"_id": d.AssertionID, "user_id": p.UserID}).Decode(&a)
	return d, a, e
}
func (s *Service) Resolve(ctx context.Context, p principal.Principal, in ResolveInput) (ResolveResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return ResolveResult{}, e
	}
	if strings.TrimSpace(in.Reason) == "" || (in.Action != "match" && in.Action != "adjust" && in.Action != "ignore") {
		return ResolveResult{}, fmt.Errorf("match, adjust or ignore and a reason required")
	}
	var out ResolveResult
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		d, a, e := s.discrepancy(tx, p, in.DiscrepancyID)
		if e != nil {
			return e
		}
		if d.Status != "open" {
			if d.Resolution != in.Action {
				return fmt.Errorf("discrepancy was already resolved with %s; undo that resolution before changing it", d.Resolution)
			}
			if d.AdjustmentID != "" {
				entry, err := s.ledger.Get(tx, p, d.AdjustmentID)
				if err != nil {
					return err
				}
				out.Adjustment = &entry
			}
			out.Resolved = true
			return nil
		}
		old := d
		if in.Action == "match" {
			if len(in.EntryIDs) == 0 {
				return fmt.Errorf("matching entries are required")
			}
			for _, id := range in.EntryIDs {
				entry, e := s.ledger.Get(tx, p, id)
				if e != nil {
					return e
				}
				if entry.Status != "posted" || entry.Date > a.Date {
					return fmt.Errorf("matching evidence must be a posted entry on or before the assertion")
				}
				matches := false
				for _, post := range entry.Postings {
					if post.Account == a.Account && post.Amount.Commodity == a.Amount.Commodity {
						matches = true
					}
				}
				if !matches {
					return fmt.Errorf("matching evidence must include the asserted account and commodity")
				}
			}
			actual, e := s.balance(tx, p, a.Account, a.Date, a.Amount.Commodity)
			if e != nil {
				return e
			}
			if actual.Rat().Cmp(a.Amount.Value.Rat()) != 0 {
				return fmt.Errorf("balance still differs; record the missing entry or use adjust")
			}
		}
		if in.Action == "adjust" {
			actual, e := s.balance(tx, p, a.Account, a.Date, a.Amount.Commodity)
			if e != nil {
				return e
			}
			delta, e := ledger.DecimalFromRat(new(big.Rat).Sub(a.Amount.Value.Rat(), actual.Rat()))
			if e != nil {
				return e
			}
			if delta.Rat().Sign() != 0 {
				if e = s.ledger.EnsureAccount(tx, p, ledger.Account{ID: "equity:adjustments", Name: "Reconciliation adjustments", Type: "equity", Commodities: []string{a.Amount.Commodity}}); e != nil {
					return e
				}
				if d.AdjustmentID == "" {
					d.AdjustmentID = ulid.Make().String()
					if _, e = s.repo.db.Collection("discrepancies").UpdateOne(tx, bson.M{"_id": d.ID, "user_id": p.UserID}, bson.M{"$set": bson.M{"adjustment_id": d.AdjustmentID}}); e != nil {
						return e
					}
				}
				entry, e := s.ledger.PostLinkedID(tx, p, ledger.EntryInput{Date: a.Date, Payee: "Reconciliation adjustment", Commit: true, Postings: []ledger.Posting{{Account: a.Account, Amount: ledger.Amount{Value: delta, Commodity: a.Amount.Commodity}}, {Account: "equity:adjustments", Amount: ledger.Amount{Value: delta.Negate(), Commodity: a.Amount.Commodity}}}}, d.AdjustmentID, "adjustment", []ledger.Link{{Rel: "settles", ID: d.ID}}, "reconcile_resolve", in.Reason)
				if e != nil {
					return e
				}
				out.Adjustment = &entry.Entry
			}
		}
		d.Status = "resolved"
		if in.Action == "ignore" {
			d.Status = "ignored"
		}
		d.Reason = in.Reason
		d.Resolution = in.Action
		_, e = s.audit.Write(tx, p, "reconcile_resolve", audit.Target{Collection: "discrepancies", ID: d.ID}, in.Reason, old, d, func(ctx context.Context) error {
			if in.Action != "ignore" {
				actual, e := s.balance(ctx, p, a.Account, a.Date, a.Amount.Commodity)
				if e != nil {
					return e
				}
				diff, e := ledger.DecimalFromRat(new(big.Rat).Sub(a.Amount.Value.Rat(), actual.Rat()))
				if e != nil {
					return e
				}
				if diff.Rat().Sign() != 0 {
					return fmt.Errorf("balance still differs after adjustment")
				}
				if _, e = s.repo.db.Collection("balance_assertions").UpdateOne(ctx, bson.M{"_id": a.ID, "user_id": p.UserID}, bson.M{"$set": bson.M{"ok": true, "diff": diff}}); e != nil {
					return e
				}
			}
			r, e := s.repo.db.Collection("discrepancies").ReplaceOne(ctx, bson.M{"_id": d.ID, "user_id": p.UserID, "status": "open"}, d)
			if e == nil && r.MatchedCount != 1 {
				return principal.ErrConflict
			}
			return e
		})
		if e == nil {
			out.Resolved = true
		}
		return e
	})
	return out, e
}
func (s *Service) Suggest(ctx context.Context, p principal.Principal, id string) ([]Candidate, error) {
	d, a, e := s.discrepancy(ctx, p, id)
	if e != nil {
		return nil, e
	}
	date, _ := time.Parse("2006-01-02", a.Date)
	entries, e := s.ledger.Entries(ctx, p, ledger.EntriesInput{Account: a.Account, From: date.AddDate(0, 0, -3).Format("2006-01-02"), To: date.AddDate(0, 0, 3).Format("2006-01-02"), Status: "posted", Limit: 500})
	if e != nil {
		return nil, e
	}
	out := []Candidate{}
	for _, entry := range entries.Items {
		for _, post := range entry.Postings {
			if post.Account == a.Account && post.Amount.Commodity == d.Diff.Commodity && (post.Amount.Value.Rat().Cmp(d.Diff.Value.Rat()) == 0 || new(big.Rat).Neg(post.Amount.Value.Rat()).Cmp(d.Diff.Value.Rat()) == 0) {
				kind := "duplicate"
				if entry.Date > a.Date {
					kind = "timing"
				}
				out = append(out, Candidate{Kind: kind, Entries: []ledger.Entry{entry}, Explains: "This posting equals the discrepancy magnitude; check its date and provenance before resolving."})
				break
			}
		}
	}
	return out, nil
}
func (s *Service) Duplicates(ctx context.Context, p principal.Principal, in ledger.EntriesInput) ([][]ledger.Entry, error) {
	in.Status = "posted"
	in.Limit = 500
	groups := map[string][]ledger.Entry{}
	total := 0
	for {
		page, e := s.ledger.Entries(ctx, p, in)
		if e != nil {
			return nil, e
		}
		for _, entry := range page.Items {
			total++
			if total > 5000 {
				return nil, fmt.Errorf("duplicate search exceeds 5000 entries; narrow dates")
			}
			posts := append([]ledger.Posting{}, entry.Postings...)
			sort.Slice(posts, func(i, j int) bool {
				return posts[i].Account+posts[i].Amount.Commodity < posts[j].Account+posts[j].Amount.Commodity
			})
			b, _ := json.Marshal(struct {
				Date, Payee string
				Postings    []ledger.Posting
			}{entry.Date, strings.ToLower(entry.Payee), posts})
			hash := sha256.Sum256(b)
			key := hex.EncodeToString(hash[:])
			groups[key] = append(groups[key], entry)
		}
		if page.NextCursor == 0 {
			break
		}
		in.Cursor = page.NextCursor
	}
	out := [][]ledger.Entry{}
	for _, group := range groups {
		if len(group) > 1 {
			out = append(out, group)
		}
	}
	return out, nil
}

func (s *Service) Verify(ctx context.Context, p principal.Principal) error {
	return s.verify(ctx, p, "")
}
func (s *Service) verify(ctx context.Context, p principal.Principal, account string) error {
	if e := p.Require("ledger:write"); e != nil {
		return e
	}
	f := bson.M{"user_id": p.UserID, "archived": bson.M{"$ne": true}}
	if account != "" {
		f["account"] = account
	}
	cur, e := s.repo.db.Collection("balance_assertions").Find(ctx, f, options.Find().SetBatchSize(100))
	if e != nil {
		return e
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var a Assertion
		if e = cur.Decode(&a); e != nil {
			return e
		}
		actual, e := s.balance(ctx, p, a.Account, a.Date, a.Amount.Commodity)
		if e != nil {
			return e
		}
		diff, e := ledger.DecimalFromRat(new(big.Rat).Sub(a.Amount.Value.Rat(), actual.Rat()))
		if e != nil {
			return e
		}
		if diff.Rat().Cmp(a.Diff.Rat()) != 0 || a.OK != (diff.Rat().Sign() == 0) {
			if _, e = s.Assert(ctx, p, AssertInput{Account: a.Account, Date: a.Date, Amount: a.Amount, Source: a.Source}); e != nil {
				return e
			}
		}
	}
	return cur.Err()
}
