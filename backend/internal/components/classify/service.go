package classify

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"math/big"
	"slices"
	"sort"
	"strings"
)

type RuleSet struct {
	Account string   `json:"account,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Kind    string   `json:"kind,omitempty"`
}
type Rule struct {
	Key      string  `json:"-" db:"storage_key"`
	UserID   string  `json:"-" db:"user_id"`
	ID       string  `json:"id,omitempty" db:"id"`
	Priority int     `json:"priority" db:"priority"`
	Match    string  `json:"match" db:"match"`
	Set      RuleSet `json:"set" db:"set"`
	Note     string  `json:"note,omitempty"`
	Enabled  bool    `json:"enabled" db:"enabled"`
	Version  int     `json:"version" db:"version"`
}
type RuleInput struct {
	ID       string  `json:"id,omitempty"`
	Priority int     `json:"priority"`
	Match    string  `json:"match"`
	Set      RuleSet `json:"set"`
	Note     string  `json:"note,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
}
type ReclassInput struct {
	EntryIDs    []string `json:"entry_ids"`
	FromAccount string   `json:"from_account"`
	ToAccount   string   `json:"to_account"`
	Reason      string   `json:"reason"`
	Commit      bool     `json:"commit,omitempty"`
}
type ReclassResult struct {
	Entries  []ledger.Entry `json:"reclass_entries"`
	EventIDs []string       `json:"event_ids"`
}
type PreviewInput struct {
	Rule   *RuleInput `json:"rule,omitempty"`
	RuleID string     `json:"rule_id,omitempty"`
	From   string     `json:"from,omitempty"`
	To     string     `json:"to,omitempty"`
}
type Change struct {
	EntryID string `json:"entry_id"`
	From    string `json:"from"`
	To      string `json:"to"`
}
type PreviewResult struct {
	WouldChange []Change `json:"would_change"`
	Count       int      `json:"count"`
}
type PostgresRepository struct{ db *pgxpool.Pool }
type Service struct {
	repo   *PostgresRepository
	ledger *ledger.Service
	audit  *audit.Service
	expr   *expressions.Engine
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository { return &PostgresRepository{db} }
func NewService(r *PostgresRepository, l *ledger.Service, a *audit.Service, e *expressions.Engine) *Service {
	return &Service{r, l, a, e}
}
func (s *Service) Rules(ctx context.Context, p principal.Principal) ([]Rule, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	cur, e := s.repo.db.Query(ctx, `SELECT storage_key,user_id,id,priority,match,"set",note,enabled,version FROM rules WHERE user_id=@user ORDER BY priority,id LIMIT 501`, pgx.NamedArgs{"user": p.UserID})
	if e != nil {
		return nil, e
	}
	out, e := pgx.CollectRows(cur, pgx.RowToStructByName[Rule])
	if out == nil {
		out = []Rule{}
	}
	if len(out) > 500 {
		return nil, fmt.Errorf("at most 500 rules")
	}
	return out, e
}
func (s *Service) Upsert(ctx context.Context, p principal.Principal, input RuleInput) (Rule, error) {
	in := Rule{ID: input.ID, Priority: input.Priority, Match: input.Match, Set: input.Set, Note: input.Note}
	if e := p.Require("ledger:write"); e != nil {
		return Rule{}, e
	}
	if in.ID == "" {
		in.ID = ulid.Make().String()
	}
	if len(in.ID) > 100 || len(in.Note) > 1000 || in.Match == "" || len(in.Set.Tags) > 50 {
		return Rule{}, fmt.Errorf("rule id, match and at most 50 tags required")
	}
	for _, tag := range in.Set.Tags {
		if strings.TrimSpace(tag) == "" || len(tag) > 100 {
			return Rule{}, fmt.Errorf("rule tags must contain 1..100 characters")
		}
	}
	if _, e := s.expr.Compile(in.Match); e != nil {
		return Rule{}, e
	}
	if in.Set.Kind != "" && in.Set.Kind != "normal" && in.Set.Kind != "transfer" {
		return Rule{}, fmt.Errorf("rule kind must be normal or transfer")
	}
	if in.Set.Account != "" {
		var n int
		e := s.repo.db.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE user_id=@user AND id=@id AND type='expense'`, pgx.NamedArgs{"user": p.UserID, "id": in.Set.Account}).Scan(&n)
		if e != nil {
			return Rule{}, e
		}
		if n != 1 {
			return Rule{}, fmt.Errorf("rule account must be an owned expense category")
		}
	}
	var old Rule
	rows, e := s.repo.db.Query(ctx, `SELECT storage_key,user_id,id,priority,match,"set",note,enabled,version FROM rules WHERE user_id=@user AND id=@id`, pgx.NamedArgs{"user": p.UserID, "id": in.ID})
	if e != nil {
		return Rule{}, e
	}
	old, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[Rule])
	if e != nil && !postgres.IsNotFound(e) {
		return Rule{}, e
	}
	in.Key = p.UserID + ":" + in.ID
	in.UserID = p.UserID
	in.Version = old.Version + 1
	in.Enabled = true
	if input.Enabled != nil {
		in.Enabled = *input.Enabled
	}
	_, e = s.audit.Write(ctx, p, "rule_upsert", audit.Target{Collection: "rules", ID: in.ID}, in.Note, old, in, func(tx context.Context) error {
		_, e := s.repo.db.Exec(tx, `INSERT INTO rules (storage_key,user_id,id,priority,match,"set",note,enabled,version) VALUES (@key,@user,@id,@priority,@match,@set,@note,@enabled,@version) ON CONFLICT (user_id,id) DO UPDATE SET storage_key=EXCLUDED.storage_key,priority=EXCLUDED.priority,match=EXCLUDED.match,"set"=EXCLUDED."set",note=EXCLUDED.note,enabled=EXCLUDED.enabled,version=EXCLUDED.version`, pgx.NamedArgs{"key": in.Key, "user": p.UserID, "id": in.ID, "priority": in.Priority, "match": in.Match, "set": in.Set, "note": in.Note, "enabled": in.Enabled, "version": in.Version})
		return e
	})
	return in, e
}
func (s *Service) resolveRule(ctx context.Context, p principal.Principal, in PreviewInput) (Rule, error) {
	if in.Rule != nil && in.RuleID != "" {
		return Rule{}, fmt.Errorf("choose rule or rule_id")
	}
	if in.Rule != nil {
		r := in.Rule
		return Rule{ID: r.ID, Priority: r.Priority, Match: r.Match, Set: r.Set, Note: r.Note, Enabled: true}, nil
	}
	var rule Rule
	rows, e := s.repo.db.Query(ctx, `SELECT storage_key,user_id,id,priority,match,"set",note,enabled,version FROM rules WHERE user_id=@user AND id=@id`, pgx.NamedArgs{"user": p.UserID, "id": in.RuleID})
	if e != nil {
		return Rule{}, e
	}
	rule, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[Rule])
	if postgres.IsNotFound(e) {
		e = principal.ErrNotFound
	}
	return rule, e
}
func (s *Service) Effective(ctx context.Context, p principal.Principal, entry ledger.Entry) (map[string]map[string]*big.Rat, error) {
	entries, e := s.ledger.LinkedEntries(ctx, p, entry.ID)
	if e != nil {
		return nil, e
	}
	amounts := map[string]map[string]*big.Rat{}
	for _, entry := range entries {
		for _, post := range entry.Postings {
			if !strings.HasPrefix(post.Account, "expenses:") {
				continue
			}
			if amounts[post.Account] == nil {
				amounts[post.Account] = map[string]*big.Rat{}
			}
			v := amounts[post.Account][post.Amount.Commodity]
			if v == nil {
				v = new(big.Rat)
				amounts[post.Account][post.Amount.Commodity] = v
			}
			v.Add(v, post.Amount.Value.Rat())
		}
	}
	return amounts, nil
}
func (s *Service) Reclassify(ctx context.Context, p principal.Principal, in ReclassInput) (ReclassResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return ReclassResult{}, e
	}
	if len(in.EntryIDs) < 1 || len(in.EntryIDs) > 100 || strings.TrimSpace(in.Reason) == "" || in.FromAccount == in.ToAccount || !strings.HasPrefix(in.FromAccount, "expenses:") || !strings.HasPrefix(in.ToAccount, "expenses:") {
		return ReclassResult{}, fmt.Errorf("1..100 entries, different expense accounts and a reason required")
	}
	out := ReclassResult{Entries: []ledger.Entry{}, EventIDs: []string{}}
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		for _, id := range in.EntryIDs {
			entry, e := s.ledger.Get(tx, p, id)
			if e != nil {
				return e
			}
			if entry.Status != "posted" {
				return fmt.Errorf("reclassification needs a posted entry")
			}
			effective, e := s.Effective(tx, p, entry)
			if e != nil {
				return e
			}
			posts := []ledger.Posting{}
			commodities := []string{}
			for c := range effective[in.FromAccount] {
				commodities = append(commodities, c)
			}
			sort.Strings(commodities)
			for _, c := range commodities {
				v := effective[in.FromAccount][c]
				if v.Sign() == 0 {
					continue
				}
				amount, e := ledger.DecimalFromRat(v)
				if e != nil {
					return e
				}
				posts = append(posts, ledger.Posting{Account: in.FromAccount, Amount: ledger.Amount{Value: amount.Negate(), Commodity: c}}, ledger.Posting{Account: in.ToAccount, Amount: ledger.Amount{Value: amount, Commodity: c}})
			}
			if len(posts) == 0 {
				continue
			}
			result, e := s.ledger.PostLinked(tx, p, ledger.EntryInput{Date: entry.Date, Payee: entry.Payee, Postings: posts, Tags: entry.Tags, Commit: in.Commit}, "reclass", []ledger.Link{{Rel: "reclassifies", ID: entry.ID}}, "reclassify", in.Reason)
			if e != nil {
				return e
			}
			out.Entries = append(out.Entries, result.Entry)
			out.EventIDs = append(out.EventIDs, result.EventID)
		}
		return nil
	})
	return out, e
}
func (s *Service) Preview(ctx context.Context, p principal.Principal, in PreviewInput) (PreviewResult, error) {
	if e := p.Require("ledger:read"); e != nil {
		return PreviewResult{}, e
	}
	rule, e := s.resolveRule(ctx, p, in)
	if e != nil {
		return PreviewResult{}, e
	}
	out := PreviewResult{WouldChange: []Change{}}
	q := ledger.EntriesInput{From: in.From, To: in.To, Status: "posted", Limit: 500}
	count := 0
	for {
		page, e := s.ledger.Entries(ctx, p, q)
		if e != nil {
			return out, e
		}
		for _, entry := range page.Items {
			if entry.Kind != "normal" && entry.Kind != "transfer" {
				continue
			}
			count++
			if count > 5000 {
				return out, fmt.Errorf("preview exceeds 5000 entries; narrow the date range")
			}
			b, _ := json.Marshal(entry)
			var row map[string]any
			_ = json.Unmarshal(b, &row)
			r, e := s.expr.Eval(ctx, rule.Match, map[string]any{"row": row})
			if e != nil {
				return out, e
			}
			match, ok := r.Value.(bool)
			if !ok {
				return out, fmt.Errorf("rule match must return boolean")
			}
			if !match {
				continue
			}
			if rule.Set.Kind != "" && rule.Set.Kind != entry.Kind {
				return out, fmt.Errorf("kind changes apply during imports; correct a posted kind with a reversal and replacement")
			}
			start := len(out.WouldChange)
			effective, e := s.Effective(ctx, p, entry)
			if e != nil {
				return out, e
			}
			for from, amounts := range effective {
				if rule.Set.Account == "" || from == rule.Set.Account {
					continue
				}
				nonzero := false
				for _, v := range amounts {
					if v.Sign() != 0 {
						nonzero = true
					}
				}
				if nonzero {
					out.WouldChange = append(out.WouldChange, Change{entry.ID, from, rule.Set.Account})
				}
			}
			if len(out.WouldChange) == start {
				for _, tag := range rule.Set.Tags {
					if !slices.Contains(entry.Tags, tag) {
						out.WouldChange = append(out.WouldChange, Change{EntryID: entry.ID})
						break
					}
				}
			}
		}
		if page.NextCursor == 0 {
			break
		}
		q.Cursor = page.NextCursor
	}
	out.Count = len(out.WouldChange)
	return out, nil
}
func (s *Service) Apply(ctx context.Context, p principal.Principal, in PreviewInput, commit bool) (ReclassResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return ReclassResult{}, e
	}
	rule, e := s.resolveRule(ctx, p, in)
	if e != nil {
		return ReclassResult{}, e
	}
	preview, e := s.Preview(ctx, p, in)
	if e != nil {
		return ReclassResult{}, e
	}
	ctx = audit.WithBatch(ctx, ulid.Make().String())
	out := ReclassResult{Entries: []ledger.Entry{}, EventIDs: []string{}}
	for _, c := range preview.WouldChange {
		if c.From != "" && c.To != "" {
			r, e := s.Reclassify(ctx, p, ReclassInput{EntryIDs: []string{c.EntryID}, FromAccount: c.From, ToAccount: c.To, Reason: "Rule " + rule.ID + ": " + rule.Note, Commit: commit})
			if e != nil {
				return out, e
			}
			out.Entries = append(out.Entries, r.Entries...)
			out.EventIDs = append(out.EventIDs, r.EventIDs...)
		}
		if len(rule.Set.Tags) > 0 {
			tagged, err := s.ledger.SetTags(ctx, p, ledger.TagInput{EntryIDs: []string{c.EntryID}, Add: rule.Set.Tags})
			if err != nil {
				return out, err
			}
			if tagged.EventID != "" {
				out.EventIDs = append(out.EventIDs, tagged.EventID)
			}
		}
	}
	return out, nil
}
func (s *Service) Classify(ctx context.Context, p principal.Principal, entry ledger.Entry) (RuleSet, error) {
	rules, e := s.Rules(ctx, p)
	if e != nil {
		return RuleSet{}, e
	}
	b, _ := json.Marshal(entry)
	var row map[string]any
	_ = json.Unmarshal(b, &row)
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		v, e := s.expr.Eval(ctx, rule.Match, map[string]any{"row": row})
		if e != nil {
			return RuleSet{}, e
		}
		match, ok := v.Value.(bool)
		if !ok {
			return RuleSet{}, fmt.Errorf("rule must return boolean")
		}
		if match {
			return rule.Set, nil
		}
	}
	return RuleSet{}, nil
}
