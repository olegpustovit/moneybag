package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"math/big"
	"sort"
	"strings"
	"time"
)

type Assertion struct {
	ID      string         `json:"id" db:"id"`
	UserID  string         `json:"-" db:"user_id"`
	Account string         `json:"account" db:"account"`
	Date    string         `json:"date" db:"date"`
	Amount  ledger.Amount  `json:"amount" db:"amount"`
	Source  string         `json:"source" db:"source"`
	OK      bool           `json:"ok" db:"ok"`
	Diff    ledger.Decimal `json:"diff" db:"diff"`
}
type Discrepancy struct {
	ID           string        `json:"id" db:"id"`
	UserID       string        `json:"-" db:"user_id"`
	AssertionID  string        `json:"assertion_id" db:"assertion_id"`
	Account      string        `json:"account" db:"account"`
	Diff         ledger.Amount `json:"diff" db:"diff"`
	Status       string        `json:"status" db:"status"`
	Resolution   string        `json:"resolution,omitempty" db:"resolution"`
	Reason       string        `json:"reason,omitempty" db:"reason"`
	AdjustmentID string        `json:"adjustment_id,omitempty" db:"adjustment_id"`
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
type PostgresRepository struct{ db *pgxpool.Pool }
type Service struct {
	repo   *PostgresRepository
	ledger *ledger.Service
	audit  *audit.Service
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository { return &PostgresRepository{db} }
func NewService(r *PostgresRepository, l *ledger.Service, a *audit.Service) *Service {
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
	old, e = s.Assertion(ctx, p, key)
	if e != nil && !postgres.IsNotFound(e) {
		return AssertResult{}, e
	}
	_, e = s.audit.Write(ctx, p, "balance_assert", audit.Target{Collection: "balance_assertions", ID: key}, in.Source, old, assertion, func(tx context.Context) error {
		if _, e := s.repo.db.Exec(tx, `INSERT INTO balance_assertions(id,user_id,account,date,amount,source,ok,diff) VALUES (@id,@user,@account,@date,@amount,@source,@ok,@diff::numeric) ON CONFLICT(id) DO UPDATE SET account=EXCLUDED.account,date=EXCLUDED.date,amount=EXCLUDED.amount,source=EXCLUDED.source,ok=EXCLUDED.ok,diff=EXCLUDED.diff,archived=false WHERE balance_assertions.user_id=EXCLUDED.user_id`, pgx.NamedArgs{"id": key, "user": p.UserID, "account": assertion.Account, "date": assertion.Date, "amount": assertion.Amount, "source": assertion.Source, "ok": assertion.OK, "diff": assertion.Diff.String()}); e != nil {
			return e
		}
		if assertion.OK {
			_, e := s.repo.db.Exec(tx, `UPDATE discrepancies SET status='resolved',resolution='balance now matches' WHERE user_id=@user AND assertion_id=@id AND status='open'`, pgx.NamedArgs{"user": p.UserID, "id": key})
			return e
		}
		d := Discrepancy{ID: key, UserID: p.UserID, AssertionID: key, Account: in.Account, Diff: ledger.Amount{Value: diff, Commodity: in.Amount.Commodity}, Status: "open"}
		_, e := s.repo.db.Exec(tx, `INSERT INTO discrepancies(id,user_id,assertion_id,account,diff,status) VALUES (@id,@user,@assertion,@account,@diff,@status) ON CONFLICT(id) DO UPDATE SET assertion_id=EXCLUDED.assertion_id,account=EXCLUDED.account,diff=EXCLUDED.diff,status=EXCLUDED.status,resolution='',reason='',adjustment_id='',archived=false WHERE discrepancies.user_id=EXCLUDED.user_id`, pgx.NamedArgs{"id": d.ID, "user": p.UserID, "assertion": d.AssertionID, "account": d.Account, "diff": d.Diff, "status": d.Status})
		return e
	})
	return AssertResult{assertion, assertion.OK, diff}, e
}
func (s *Service) Status(ctx context.Context, p principal.Principal, account string) ([]AccountStatus, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	byAccount := map[string]*AccountStatus{}
	count := 0
	args := pgx.NamedArgs{"user": p.UserID, "account": account}
	// Read-only: backdated entries appear after Verify or an explicit reconcile_verify.
	cur, e := s.repo.db.Query(ctx, `WITH bounded AS (SELECT account,ok,date FROM balance_assertions WHERE user_id=@user AND NOT archived AND (@account='' OR account=@account) ORDER BY ok DESC,date DESC LIMIT 5001)
 SELECT account,COALESCE(MAX(date) FILTER (WHERE ok),'') AS last_ok,count(*) AS count FROM bounded GROUP BY account`, args)
	if e != nil {
		return nil, e
	}
	assertions, e := pgx.CollectRows(cur, pgx.RowToStructByName[struct {
		Account string
		LastOK  string `db:"last_ok"`
		Count   int
	}])
	if e != nil {
		return nil, e
	}
	for _, a := range assertions {
		count += a.Count
		byAccount[a.Account] = &AccountStatus{Account: a.Account, LastOK: a.LastOK, Discrepancies: []Discrepancy{}}
	}
	if count > 5000 {
		return nil, fmt.Errorf("too many assertions; select one account")
	}
	cur, e = s.repo.db.Query(ctx, `SELECT id,user_id,assertion_id,account,diff,status,resolution,reason,adjustment_id FROM discrepancies WHERE user_id=@user AND NOT archived AND status='open' AND (@account='' OR account=@account) LIMIT 5001`, args)
	if e != nil {
		return nil, e
	}
	ds, e := pgx.CollectRows(cur, pgx.RowToStructByName[Discrepancy])
	if e != nil {
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

// OpenCount reports recorded open discrepancies without running a balance recheck.
func (s *Service) OpenCount(ctx context.Context, p principal.Principal) (int64, error) {
	if err := p.Require("ledger:read"); err != nil {
		return 0, err
	}
	var n int64
	err := s.repo.db.QueryRow(ctx, `SELECT count(*) FROM discrepancies WHERE user_id=@user AND status='open' AND NOT archived`, pgx.NamedArgs{"user": p.UserID}).Scan(&n)
	return n, err
}

func (s *Service) discrepancy(ctx context.Context, p principal.Principal, id string) (Discrepancy, Assertion, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Discrepancy{}, Assertion{}, e
	}
	var d Discrepancy
	rows, e := s.repo.db.Query(ctx, `SELECT id,user_id,assertion_id,account,diff,status,resolution,reason,adjustment_id FROM discrepancies WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"id": id, "user": p.UserID})
	if e != nil {
		return d, Assertion{}, e
	}
	d, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[Discrepancy])
	if postgres.IsNotFound(e) {
		return d, Assertion{}, principal.ErrNotFound
	}
	if e != nil {
		return d, Assertion{}, e
	}
	var a Assertion
	a, e = s.Assertion(ctx, p, d.AssertionID)
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
					if _, e = s.repo.db.Exec(tx, `UPDATE discrepancies SET adjustment_id=@adjustment WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"adjustment": d.AdjustmentID, "id": d.ID, "user": p.UserID}); e != nil {
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
				if _, e = s.repo.db.Exec(ctx, `UPDATE balance_assertions SET ok=true,diff=@diff::numeric WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"diff": diff.String(), "id": a.ID, "user": p.UserID}); e != nil {
					return e
				}
			}
			r, e := s.repo.db.Exec(ctx, `UPDATE discrepancies SET assertion_id=@assertion,account=@account,diff=@diff,status=@status,resolution=@resolution,reason=@reason,adjustment_id=@adjustment,archived=false WHERE id=@id AND user_id=@user AND status='open'`, pgx.NamedArgs{"assertion": d.AssertionID, "account": d.Account, "diff": d.Diff, "status": d.Status, "resolution": d.Resolution, "reason": d.Reason, "adjustment": d.AdjustmentID, "id": d.ID, "user": p.UserID})
			if e == nil && r.RowsAffected() != 1 {
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

// Recheck recomputes one account's assertions, or every one when account is empty, and returns the status.
func (s *Service) Recheck(ctx context.Context, p principal.Principal, account string) ([]AccountStatus, error) {
	if e := s.verify(ctx, p, account); e != nil {
		return nil, e
	}
	return s.Status(ctx, p, account)
}
func (s *Service) verify(ctx context.Context, p principal.Principal, account string) error {
	if e := p.Require("ledger:write"); e != nil {
		return e
	}
	cur, e := s.repo.db.Query(ctx, `SELECT a.id,a.user_id,a.account,a.date,a.amount,a.source,a.ok,a.diff::text,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('commodity',b.commodity,'amount',b.amount)) FROM (
 SELECT p->'amount'->>'c' AS commodity,SUM((p->'amount'->>'v')::numeric)::text AS amount
 FROM journal j CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(j.postings,'null'::jsonb),'[]'::jsonb)) p
 WHERE j.user_id=@user AND j.status='posted' AND j.date<=CASE WHEN a.date='' THEN @today ELSE a.date END AND p->>'account'=a.account
 GROUP BY p->'amount'->>'c' LIMIT 5001) b),'[]'::jsonb)
 FROM balance_assertions a WHERE a.user_id=@user AND NOT a.archived AND (@account='' OR a.account=@account)`, pgx.NamedArgs{"user": p.UserID, "account": account, "today": time.Now().Format("2006-01-02")})
	if e != nil {
		return e
	}
	defer cur.Close()
	for cur.Next() {
		var row struct {
			Assertion
			Balances []struct {
				Commodity string         `json:"commodity"`
				Amount    ledger.Decimal `json:"amount"`
			}
		}
		var raw []byte
		stored := &row.Assertion
		if e = cur.Scan(&stored.ID, &stored.UserID, &stored.Account, &stored.Date, &stored.Amount, &stored.Source, &stored.OK, &stored.Diff, &raw); e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &row.Balances); e != nil {
			return e
		}
		a := row.Assertion
		if e = p.Require("ledger:read"); e != nil {
			return e
		}
		if a.Date != "" {
			if _, e = time.Parse("2006-01-02", a.Date); e != nil {
				return fmt.Errorf("at must be YYYY-MM-DD")
			}
		}
		if e = p.Valid(); e != nil {
			return e
		}
		if len(row.Balances) > 5000 {
			return fmt.Errorf("balance result exceeds 5000 rows")
		}
		actual := ledger.MustDecimal("0")
		for _, balance := range row.Balances {
			value, err := ledger.DecimalFromRat(balance.Amount.Rat())
			if err != nil {
				return err
			}
			if balance.Commodity == a.Amount.Commodity {
				actual = value
			}
		}
		diff, e := ledger.DecimalFromRat(new(big.Rat).Sub(a.Amount.Value.Rat(), actual.Rat()))
		if e != nil {
			return e
		}
		if diff.Rat().Cmp(a.Diff.Rat()) != 0 || a.OK != (diff.Rat().Sign() == 0) {
			result, err := s.Assert(ctx, p, AssertInput{Account: a.Account, Date: a.Date, Amount: a.Amount, Source: a.Source})
			if err != nil {
				return err
			}
			a = result.Assertion
		}
	}
	return cur.Err()
}
