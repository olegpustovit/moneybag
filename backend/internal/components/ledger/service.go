package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Service struct {
	repo  *MongoRepository
	audit *audit.Service
	// Expr evaluates tags_set where expressions; nil disables them.
	Expr *expressions.Engine
}

func NewService(r *MongoRepository, a *audit.Service) *Service { return &Service{repo: r, audit: a} }

var accountID = regexp.MustCompile(`^(assets|liabilities|income|expenses|equity):[a-z0-9_:-]{1,150}$`)
var commodityID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,19}$`)

func (s *Service) CreateAccount(ctx context.Context, p principal.Principal, a Account) (AccountResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return AccountResult{}, e
	}
	if !accountID.MatchString(a.ID) || strings.TrimSpace(a.Name) == "" || len(a.Name) > 200 || len(a.Commodities) == 0 || len(a.Commodities) > 20 || a.ClosedAt != nil {
		return AccountResult{}, fmt.Errorf("valid id, name, type and 1..20 commodities required")
	}
	if e := validateClassGroups(a.Class, a.Groups); e != nil {
		return AccountResult{}, e
	}
	types := map[string]string{"assets": "asset", "liabilities": "liability", "income": "income", "expenses": "expense", "equity": "equity"}
	if types[strings.Split(a.ID, ":")[0]] != a.Type {
		return AccountResult{}, fmt.Errorf("account type must match its id prefix")
	}
	for _, c := range a.Commodities {
		if !commodityID.MatchString(c) {
			return AccountResult{}, fmt.Errorf("invalid commodity")
		}
	}
	a.UserID = p.UserID
	a.Key = p.UserID + ":" + a.ID
	db, e := s.repo.db(p)
	if e != nil {
		return AccountResult{}, e
	}
	id, e := s.audit.Write(ctx, p, "account_create", audit.Target{Collection: "accounts", ID: a.ID}, "", nil, a, func(tx context.Context) error {
		_, err := db.Collection("accounts").InsertOne(tx, a)
		if mongo.IsDuplicateKeyError(err) {
			return principal.ErrConflict
		}
		return err
	})
	return AccountResult{a, id}, e
}

type AccountResult struct {
	Account Account `json:"account"`
	EventID string  `json:"event_id"`
}

// EnsureAccount creates a system account, or adds commodities it lacks, so built-in postings work for new users.
func (s *Service) EnsureAccount(ctx context.Context, p principal.Principal, a Account) error {
	old, e := s.Account(ctx, p, a.ID)
	if errors.Is(e, principal.ErrNotFound) {
		_, e = s.CreateAccount(ctx, p, a)
		return e
	}
	if e != nil {
		return e
	}
	commodities := old.Commodities
	for _, c := range a.Commodities {
		if !slices.Contains(commodities, c) {
			commodities = append(commodities, c)
		}
	}
	if len(commodities) == len(old.Commodities) {
		return nil
	}
	_, e = s.UpdateAccount(ctx, p, AccountUpdate{ID: a.ID, Commodities: &commodities})
	return e
}

type AccountUpdate struct {
	ID       string  `json:"id"`
	Name     *string `json:"name,omitempty"`
	Pot      *string `json:"pot,omitempty"`
	Location *string `json:"location,omitempty"`
	Close    *bool   `json:"close,omitempty"`
	// Class is one of bank, cash, metal_holding, receivable, liability, equity, income, expense; empty clears it.
	Class *string `json:"class,omitempty"`
	// Groups replaces all groups (dimension to value, e.g. type, category, source); an empty map clears them.
	Groups      *map[string]string `json:"groups,omitempty"`
	Commodities *[]string          `json:"commodities,omitempty"`
}

func (s *Service) UpdateAccount(ctx context.Context, p principal.Principal, in AccountUpdate) (AccountResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return AccountResult{}, e
	}
	old, e := s.repo.Account(ctx, p, in.ID)
	if e != nil {
		return AccountResult{}, e
	}
	a := old
	if in.Commodities != nil {
		if len(*in.Commodities) < 1 || len(*in.Commodities) > 20 {
			return AccountResult{}, fmt.Errorf("1..20 commodities required")
		}
		for _, c := range *in.Commodities {
			if !commodityID.MatchString(c) {
				return AccountResult{}, fmt.Errorf("invalid commodity")
			}
		}
		for _, c := range old.Commodities {
			if !slices.Contains(*in.Commodities, c) {
				return AccountResult{}, fmt.Errorf("existing commodities cannot be removed; close the account instead")
			}
		}
		a.Commodities = *in.Commodities
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" || len(*in.Name) > 200 {
			return AccountResult{}, fmt.Errorf("name must be 1..200 characters")
		}
		a.Name = *in.Name
	}
	if in.Pot != nil {
		a.Pot = *in.Pot
	}
	if in.Location != nil {
		a.Location = *in.Location
	}
	if in.Class != nil {
		a.Class = *in.Class
	}
	if in.Groups != nil {
		a.Groups = *in.Groups
	}
	if e := validateClassGroups(a.Class, a.Groups); e != nil {
		return AccountResult{}, e
	}
	if in.Close != nil {
		if *in.Close {
			now := time.Now().UTC()
			a.ClosedAt = &now
		} else {
			a.ClosedAt = nil
		}
	}
	db, _ := s.repo.db(p)
	id, e := s.audit.Write(ctx, p, "account_update", audit.Target{Collection: "accounts", ID: a.ID}, "", old, a, func(tx context.Context) error {
		// Compare in Go: a whole-document filter breaks on groups map key order, but a concurrent edit must still conflict.
		if current, err := s.repo.Account(tx, p, in.ID); err != nil {
			return err
		} else if !reflect.DeepEqual(current, old) {
			return principal.ErrConflict
		}
		result, err := db.Collection("accounts").ReplaceOne(tx, bson.M{"_id": old.Key}, a)
		if err == nil && result.MatchedCount != 1 {
			return principal.ErrConflict
		}
		return err
	})
	return AccountResult{a, id}, e
}
func (s *Service) Accounts(ctx context.Context, p principal.Principal, in AccountsInput) (AccountsPage, error) {
	if e := p.Require("ledger:read"); e != nil {
		return AccountsPage{}, e
	}
	if in.Limit < 0 || in.Limit > 500 {
		return AccountsPage{}, fmt.Errorf("limit must be 1..500")
	}
	return s.repo.Accounts(ctx, p, in)
}
func (s *Service) Entries(ctx context.Context, p principal.Principal, in EntriesInput) (EntriesPage, error) {
	if e := p.Require("ledger:read"); e != nil {
		return EntriesPage{}, e
	}
	if in.Limit < 0 || in.Limit > 500 || len(in.Text) > 200 {
		return EntriesPage{}, fmt.Errorf("limit at most 500 and text at most 200 characters")
	}
	for _, d := range []string{in.From, in.To} {
		if d != "" {
			if _, e := time.Parse("2006-01-02", d); e != nil {
				return EntriesPage{}, fmt.Errorf("dates must be YYYY-MM-DD")
			}
		}
	}
	return s.repo.Entries(ctx, p, in)
}
func (s *Service) Get(ctx context.Context, p principal.Principal, id string) (Entry, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Entry{}, e
	}
	return s.repo.Entry(ctx, p, id)
}
func (s *Service) Account(ctx context.Context, p principal.Principal, id string) (Account, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Account{}, e
	}
	return s.repo.Account(ctx, p, id)
}
func (s *Service) checkAccounts(ctx context.Context, p principal.Principal, in EntryInput, allowClosed bool) error {
	for _, post := range in.Postings {
		a, err := s.repo.Account(ctx, p, post.Account)
		if err != nil {
			return fmt.Errorf("posting account %s: %w", post.Account, err)
		}
		if a.ClosedAt != nil && !allowClosed || !slices.Contains(a.Commodities, post.Amount.Commodity) {
			return fmt.Errorf("account %s is closed or does not accept %s", a.ID, post.Amount.Commodity)
		}
		for _, v := range []*Amount{post.Cost, post.Price} {
			if v != nil {
				if _, err = ParseDecimal(v.Value.String()); err != nil || !commodityID.MatchString(v.Commodity) || v.Value.Rat().Sign() < 0 {
					return fmt.Errorf("cost and price must be non-negative amounts")
				}
			}
		}
	}
	return nil
}

// ValidateEntry checks a proposal without writing a draft or journal entry.
func (s *Service) ValidateEntry(ctx context.Context, p principal.Principal, in EntryInput) error {
	if err := p.Require("ledger:read"); err != nil {
		return err
	}
	if err := Validate(in); err != nil {
		return err
	}
	return s.checkAccounts(ctx, p, in, false)
}

func inferKind(posts []Posting) string {
	for _, p := range posts {
		if !strings.HasPrefix(p.Account, "assets:") && !strings.HasPrefix(p.Account, "liabilities:") && !strings.HasPrefix(p.Account, "equity:conversion") {
			return "normal"
		}
	}
	return "transfer"
}
func (s *Service) Post(ctx context.Context, p principal.Principal, in EntryInput) (EntryResult, error) {
	if in.ID != "" {
		if in.Date != "" || in.Payee != "" || len(in.Postings) > 0 || in.Tags != nil {
			if in.Commit {
				return EntryResult{}, fmt.Errorf("edit the draft with commit=false, then post with only id and commit=true")
			}
			return s.UpdateDraft(ctx, p, in)
		}
		if !in.Commit {
			return EntryResult{}, fmt.Errorf("provide the full entry body to edit a draft, or only id and commit=true to post it")
		}
		return s.CommitDraft(ctx, p, in.ID)
	}
	return s.PostLinked(ctx, p, in, "", nil, "entry_post", "")
}
func (s *Service) PostLinked(ctx context.Context, p principal.Principal, in EntryInput, kind string, links []Link, tool, reason string) (EntryResult, error) {
	return s.PostLinkedID(ctx, p, in, ulid.Make().String(), kind, links, tool, reason)
}
func (s *Service) PostLinkedID(ctx context.Context, p principal.Principal, in EntryInput, id, kind string, links []Link, tool, reason string) (EntryResult, error) {
	if err := p.Require("ledger:write"); err != nil {
		return EntryResult{}, err
	}
	if err := Validate(in); err != nil {
		return EntryResult{}, err
	}
	if kind == "" {
		kind = inferKind(in.Postings)
	}
	if _, e := ulid.ParseStrict(id); e != nil {
		return EntryResult{}, fmt.Errorf("invalid entry id")
	}
	if existing, e := s.repo.Entry(ctx, p, id); e == nil {
		a, _ := json.Marshal(existing.Postings)
		b, _ := json.Marshal(in.Postings)
		if existing.Date != in.Date || existing.Payee != in.Payee || string(a) != string(b) {
			return EntryResult{}, principal.ErrConflict
		}
		return EntryResult{Entry: existing, Balanced: true}, nil
	} else if !errors.Is(e, principal.ErrNotFound) {
		return EntryResult{}, e
	}
	entry := Entry{UserID: p.UserID, ID: id, Date: in.Date, Payee: in.Payee, Postings: in.Postings, Tags: in.Tags, Links: links, Kind: kind, Status: "draft", Actor: p.Actor, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if in.Commit {
		entry.Status = "posted"
	}
	if entry.Tags == nil {
		entry.Tags = []string{}
	}
	if entry.Links == nil {
		entry.Links = []Link{}
	}
	id, err := s.audit.Write(ctx, p, tool, audit.Target{Collection: "journal", ID: entry.ID}, reason, nil, &entry, func(tx context.Context) error {
		if err := s.checkAccounts(tx, p, in, kind == "reversal"); err != nil {
			return err
		}
		return s.repo.insert(tx, p, &entry)
	})
	return EntryResult{entry, true, id}, err
}

type ReverseInput struct {
	ID          string      `json:"id"`
	Reason      string      `json:"reason"`
	Replacement *EntryInput `json:"replacement,omitempty"`
}
type ReverseResult struct {
	Reversal    Entry    `json:"reversal"`
	Replacement *Entry   `json:"replacement,omitempty"`
	EventIDs    []string `json:"event_ids"`
}

func (s *Service) Reverse(ctx context.Context, p principal.Principal, in ReverseInput) (ReverseResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return ReverseResult{}, e
	}
	if strings.TrimSpace(in.Reason) == "" {
		return ReverseResult{}, fmt.Errorf("reason is required")
	}
	var result ReverseResult
	err := s.workflow(ctx, func(tx context.Context) error {
		if in.Replacement != nil {
			if e := Validate(*in.Replacement); e != nil {
				return e
			}
			if e := s.checkAccounts(tx, p, *in.Replacement, false); e != nil {
				return e
			}
		}
		original, e := s.repo.Entry(tx, p, in.ID)
		if e != nil {
			return e
		}
		if original.Status != "posted" {
			return fmt.Errorf("only posted entries can be reversed")
		}
		db, _ := s.repo.db(p)
		n, e := db.Collection("journal").CountDocuments(tx, bson.M{"user_id": p.UserID, "links": bson.M{"$elemMatch": bson.M{"rel": "reverses", "id": in.ID}}})
		if e != nil {
			return e
		}
		if n > 0 {
			return fmt.Errorf("entry already reversed")
		}
		postings := slices.Clone(original.Postings)
		for i := range postings {
			postings[i].Amount.Value = postings[i].Amount.Value.Negate()
		}
		rev, e := s.PostLinked(tx, p, EntryInput{Date: original.Date, Payee: original.Payee, Postings: postings, Tags: original.Tags, Commit: true}, "reversal", []Link{{Rel: "reverses", ID: in.ID}}, "entry_reverse", in.Reason)
		if e != nil {
			return e
		}
		result = ReverseResult{Reversal: rev.Entry, EventIDs: []string{rev.EventID}}
		if in.Replacement != nil {
			rep, e := s.PostLinked(tx, p, *in.Replacement, "", []Link{{Rel: "replaces", ID: in.ID}}, "entry_reverse", in.Reason)
			if e != nil {
				return e
			}
			result.Replacement = &rep.Entry
			result.EventIDs = append(result.EventIDs, rep.EventID)
		}
		return nil
	})
	return result, err
}
func (s *Service) workflow(ctx context.Context, fn func(context.Context) error) error {
	return s.audit.Workflow(ctx, fn)
}

type TransferInput struct {
	Date        string  `json:"date"`
	FromAccount string  `json:"from_account"`
	ToAccount   string  `json:"to_account"`
	Amount      Amount  `json:"amount"`
	Received    *Amount `json:"received,omitempty"`
	Fee         *Amount `json:"fee,omitempty"`
}

func (s *Service) Transfer(ctx context.Context, p principal.Principal, in TransferInput) (EntryResult, error) {
	if in.Amount.Value.Rat().Sign() == 0 {
		return EntryResult{}, fmt.Errorf("transfer amount cannot be zero")
	}
	a := in.Amount
	a.Value = MustDecimal(strings.TrimPrefix(a.Value.String(), "-"))
	recv := a
	if in.Received != nil {
		recv = *in.Received
	}
	if recv.Value.Rat().Sign() <= 0 {
		return EntryResult{}, fmt.Errorf("received amount must be positive")
	}
	posts := []Posting{{Account: in.FromAccount, Amount: Amount{a.Value.Negate(), a.Commodity}}, {Account: in.ToAccount, Amount: recv}}
	if a.Commodity != recv.Commodity {
		posts = append(posts, Posting{Account: "equity:conversion", Amount: a}, Posting{Account: "equity:conversion", Amount: Amount{recv.Value.Negate(), recv.Commodity}})
	} else if a.Value.Rat().Cmp(recv.Value.Rat()) != 0 {
		return EntryResult{}, fmt.Errorf("same-currency amounts must match; use fee for bank charges")
	}
	if in.Fee != nil {
		if in.Fee.Value.Rat().Sign() < 0 {
			return EntryResult{}, fmt.Errorf("fee cannot be negative")
		}
		posts = append(posts, Posting{Account: in.FromAccount, Amount: Amount{in.Fee.Value.Negate(), in.Fee.Commodity}}, Posting{Account: "expenses:fees", Amount: *in.Fee})
	}
	if a.Commodity != recv.Commodity {
		if e := s.EnsureAccount(ctx, p, Account{ID: "equity:conversion", Name: "Currency conversion", Type: "equity", Commodities: []string{a.Commodity, recv.Commodity}}); e != nil {
			return EntryResult{}, e
		}
	}
	if in.Fee != nil {
		if e := s.EnsureAccount(ctx, p, Account{ID: "expenses:fees", Name: "Fees", Type: "expense", Commodities: []string{in.Fee.Commodity}}); e != nil {
			return EntryResult{}, e
		}
	}
	return s.PostLinked(ctx, p, EntryInput{Date: in.Date, Payee: "Transfer", Postings: posts, Commit: true}, "transfer", nil, "transfer_record", "")
}
func (s *Service) CommitDraft(ctx context.Context, p principal.Principal, id string) (EntryResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return EntryResult{}, e
	}
	old, e := s.repo.Entry(ctx, p, id)
	if e != nil {
		return EntryResult{}, e
	}
	if old.Status != "draft" {
		return EntryResult{}, fmt.Errorf("entry is not a draft")
	}
	entry := old
	entry.Status = "posted"
	db, _ := s.repo.db(p)
	ev, e := s.audit.Write(ctx, p, "entry_commit", audit.Target{Collection: "journal", ID: id}, "", old, entry, func(tx context.Context) error {
		if e := s.checkAccounts(tx, p, EntryInput{Postings: old.Postings}, false); e != nil {
			return e
		}
		r, e := db.Collection("journal").ReplaceOne(tx, bson.M{"user_id": p.UserID, "_id": id, "status": "draft"}, entry)
		if e == nil && r.MatchedCount != 1 {
			return principal.ErrConflict
		}
		if e != nil {
			return e
		}
		_, e = db.Collection("balances").UpdateMany(tx, bson.M{"user_id": p.UserID}, bson.M{"$set": bson.M{"dirty": true}})
		return e
	})
	return EntryResult{entry, true, ev}, e
}
func (s *Service) Verify(ctx context.Context, p principal.Principal) error {
	page := EntriesInput{Limit: 500, Status: "posted"}
	for {
		out, err := s.Entries(ctx, p, page)
		if err != nil {
			return err
		}
		for _, e := range out.Items {
			if err = Validate(EntryInput{Date: e.Date, Payee: e.Payee, Postings: e.Postings, Tags: e.Tags}); err != nil {
				return fmt.Errorf("journal invariant at %s: %w", e.ID, err)
			}
		}
		if out.NextCursor == 0 {
			break
		}
		page.Cursor = out.NextCursor
	}
	return s.audit.Verify(ctx, p)
}
