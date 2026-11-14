package budget

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"math/big"
	"slices"
	"strings"
	"time"
)

type Line struct {
	LiteralAccounts []string       `json:"-" bson:"-" db:"-"`
	Synthetic       bool           `json:"-" bson:"-" db:"-"`
	Archived        bool           `json:"archived,omitempty" bson:"archived,omitempty" db:"archived"`
	Key             string         `json:"-" bson:"_id" db:"storage_key"`
	UserID          string         `json:"-" bson:"user_id" db:"user_id"`
	ID              string         `json:"id" bson:"id" db:"id"`
	Name            string         `json:"name" bson:"name" db:"name"`
	Revision        int            `json:"revision,omitempty" bson:"revision" db:"revision"`
	Limit           ledger.Decimal `json:"limit" bson:"limit" db:"limit"`
	Accounts        []string       `json:"accounts" bson:"accounts" db:"accounts"`
	// GroupRef maps every expense account whose groups[dimension] equals value, resolved on read.
	GroupRef *GroupRef `json:"group_ref,omitempty" bson:"group_ref,omitempty" db:"group_ref"`
	Order    int       `json:"order" bson:"order" db:"order"`
}
type GroupRef struct {
	Dimension string `json:"dimension" bson:"dimension"`
	Value     string `json:"value" bson:"value"`
}
type SetInput struct {
	ID               string          `json:"id"`
	ExpectedRevision *int            `json:"expected_revision,omitempty" jsonschema:"optional revision from budget_get; -1 requires a new line"`
	Limit            *ledger.Decimal `json:"limit,omitempty"`
	Name             *string         `json:"name,omitempty"`
	Accounts         *[]string       `json:"accounts,omitempty"`
	// GroupRef replaces the literal accounts list by an account group; an empty dimension clears it.
	GroupRef *GroupRef `json:"group_ref,omitempty"`
	Order    *int      `json:"order,omitempty"`
}
type SetResult struct {
	Line    Line   `json:"line"`
	EventID string `json:"event_id"`
}
type StatusLine struct {
	LimitExact      string    `json:"limit_exact,omitempty"`
	LiteralAccounts []string  `json:"literal_accounts"`
	GroupRef        *GroupRef `json:"group_ref,omitempty"`
	Synthetic       bool      `json:"synthetic,omitempty"`
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Revision        int       `json:"revision,omitempty"`
	Limit           float64   `json:"limit"`
	Actual          float64   `json:"actual"`
	Avg6            float64   `json:"avg6"`
	Status          string    `json:"status"`
	Accounts        []string  `json:"accounts"`
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
type PostgresRepository struct{ db *pgxpool.Pool }
type Service struct {
	repo   *PostgresRepository
	audit  *audit.Service
	valuer ledger.Valuer
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository { return &PostgresRepository{db} }
func NewService(r *PostgresRepository, a *audit.Service, v ledger.Valuer) *Service {
	return &Service{r, a, v}
}
func (s *Service) Lines(ctx context.Context, p principal.Principal) ([]Line, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	lines, err := reqcache.Do(ctx, fmt.Sprintf("budget-lines|%p|%d:%s", s.repo.db, len(p.UserID), p.UserID), func() ([]Line, error) {
		return s.lines(ctx, p)
	})
	if err != nil {
		return nil, err
	}
	out := slices.Clone(lines)
	for i := range out {
		out[i].Accounts = slices.Clone(out[i].Accounts)
		out[i].LiteralAccounts = slices.Clone(out[i].LiteralAccounts)
		if out[i].GroupRef != nil {
			ref := *out[i].GroupRef
			out[i].GroupRef = &ref
		}
	}
	return out, nil
}

func (s *Service) lines(ctx context.Context, p principal.Principal) ([]Line, error) {
	cur, e := s.repo.db.Query(ctx, `SELECT `+lineColumns+` FROM budgets WHERE user_id=@user AND NOT archived ORDER BY "order",id LIMIT 501`, pgx.NamedArgs{"user": p.UserID})
	if e != nil {
		return nil, e
	}
	out, e := pgx.CollectRows(cur, pgx.RowToStructByNameLax[Line])
	if e != nil {
		return nil, e
	}
	if out == nil {
		out = []Line{}
	}
	if len(out) > 500 {
		return nil, fmt.Errorf("too many budget lines")
	}
	for i, l := range out {
		out[i].LiteralAccounts = slices.Clone(l.Accounts)
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

const lineColumns = `storage_key,user_id,id,name,revision,COALESCE(limit_lexeme,"limit"::text) AS "limit",accounts,group_ref,"order",archived`

type accountID struct {
	ID string `bson:"id" db:"id"`
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
	c, err := s.repo.db.Query(ctx, `SELECT id FROM accounts WHERE user_id=@user AND type='expense' AND closed_at IS NULL AND groups->>@dimension=@value ORDER BY id`, pgx.NamedArgs{"user": p.UserID, "dimension": ref.Dimension, "value": ref.Value})
	if err == nil {
		found, err = pgx.CollectRows(c, pgx.RowToStructByName[accountID])
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
	var result SetResult
	err := s.audit.Workflow(ctx, func(tx context.Context) error {
		var err error
		result, err = s.set(tx, p, in)
		return err
	})
	return result, err
}
func (s *Service) set(ctx context.Context, p principal.Principal, in SetInput) (SetResult, error) {
	var old Line
	rows, e := s.repo.db.Query(ctx, `SELECT `+lineColumns+` FROM budgets WHERE user_id=@user AND id=@id`, pgx.NamedArgs{"user": p.UserID, "id": in.ID})
	if e != nil {
		return SetResult{}, e
	}
	old, e = pgx.CollectOneRow(rows, pgx.RowToStructByNameLax[Line])
	if e != nil && !postgres.IsNotFound(e) {
		return SetResult{}, e
	}
	exists := e == nil
	if in.ExpectedRevision != nil && ((!exists && *in.ExpectedRevision != -1) || (exists && *in.ExpectedRevision != old.Revision)) {
		return SetResult{}, principal.ErrConflict
	}
	line := old
	line.Archived = false
	if postgres.IsNotFound(e) {
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
	line.Revision = old.Revision + 1
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
			var n int
			e := s.repo.db.QueryRow(tx, `SELECT count(*) FROM accounts WHERE user_id=@user AND id=@id AND type='expense'`, pgx.NamedArgs{"user": p.UserID, "id": a}).Scan(&n)
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
		var lexeme *string
		// numeric normalizes negative zero; retain its original decimal spelling.
		if line.Limit.Rat().Sign() == 0 && strings.HasPrefix(line.Limit.String(), "-") {
			v := line.Limit.String()
			lexeme = &v
		}
		args := pgx.NamedArgs{"key": line.Key, "user": p.UserID, "id": line.ID, "name": line.Name, "revision": line.Revision, "limit": line.Limit.String(), "lexeme": lexeme, "accounts": line.Accounts, "group": line.GroupRef, "order": line.Order, "archived": line.Archived, "old": old.Revision}
		if !exists {
			delete(args, "old")
			_, e = s.repo.db.Exec(tx, `INSERT INTO budgets (storage_key,user_id,id,name,revision,"limit",limit_lexeme,accounts,group_ref,"order",archived) VALUES (@key,@user,@id,@name,@revision,@limit::numeric,@lexeme,@accounts,@group,@order,@archived)`, args)
			if postgres.IsUniqueViolation(e) {
				return principal.ErrConflict
			}
			return e
		}
		result, e := s.repo.db.Exec(tx, `UPDATE budgets SET storage_key=@key,name=@name,revision=@revision,"limit"=@limit::numeric,limit_lexeme=@lexeme,accounts=@accounts,group_ref=@group,"order"=@order,archived=@archived WHERE user_id=@user AND id=@id AND revision=@old`, args)
		if e == nil && result.RowsAffected() == 0 {
			return principal.ErrConflict
		}
		return e
	})
	return SetResult{line, id}, e
}

type monthReadWindow struct{ from, to string }
type monthReadWindowKey struct{}

// WithMonthReadWindow shares grouped journal rows only within the supplied read range.
func WithMonthReadWindow(ctx context.Context, from, to string) context.Context {
	return context.WithValue(ctx, monthReadWindowKey{}, monthReadWindow{from, to})
}

// MonthReadWindow returns the shared budget aggregation window, if present.
func MonthReadWindow(ctx context.Context) (string, string, bool) {
	w, ok := ctx.Value(monthReadWindowKey{}).(monthReadWindow)
	return w.from, w.to, ok
}

type monthRow = ledger.JournalMonthRow

func (s *Service) monthRows(ctx context.Context, p principal.Principal, from, to string) ([]monthRow, error) {
	if rows, ok, err := ledger.NewPostgresRepository(s.repo.db).CombinedMonthRows(ctx, p, from, to); ok || err != nil {
		return rows, err
	}
	cur, err := s.repo.db.Query(ctx, `SELECT j.date,p->>'account',p->'amount'->>'c',
 CASE WHEN p->'price' IS NULL OR p->'price'='null'::jsonb THEN NULL ELSE jsonb_build_object('v',((p->'price'->>'v')::numeric)::text,'c',p->'price'->>'c') END,
 SUM((p->'amount'->>'v')::numeric)::text
 FROM journal j CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(j.postings,'null'::jsonb),'[]'::jsonb)) p
 WHERE j.user_id=@user AND j.status='posted' AND j.date>=@from AND j.date<=@to
 AND p->>'account' LIKE 'expenses:%' AND p->>'account' NOT LIKE 'expenses:estimated%'
 GROUP BY j.date,p->>'account',p->'amount'->>'c',(p->'price'->>'v')::numeric,p->'price'->>'c',p->'price' IS NULL OR p->'price'='null'::jsonb LIMIT 5001`, pgx.NamedArgs{"user": p.UserID, "from": from, "to": to})
	if err != nil {
		return nil, err
	}
	defer cur.Close()
	var rows []monthRow
	for cur.Next() {
		var row monthRow
		var price json.RawMessage
		if err = cur.Scan(&row.ID.Date, &row.ID.Account, &row.ID.Commodity, &price, &row.Amount); err != nil {
			return nil, err
		}
		if len(price) > 0 {
			if err = json.Unmarshal(price, &row.ID.Price); err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, cur.Err()
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
	var rows []monthRow
	window, shared := ctx.Value(monthReadWindowKey{}).(monthReadWindow)
	shared = shared && from >= window.from && to <= window.to && from <= to
	if shared {
		key := fmt.Sprintf("budget-months|%p|%d:%s|%s|%s", s.repo.db, len(p.UserID), p.UserID, window.from, window.to)
		rows, e = reqcache.Do(ctx, key, func() ([]monthRow, error) { return s.monthRows(ctx, p, window.from, window.to) })
		if e != nil && ctx.Err() != nil {
			return nil, e
		}
		if e == nil && len(rows) <= 5000 {
			filtered := make([]monthRow, 0, len(rows))
			for _, row := range rows {
				if row.ID.Date >= from && row.ID.Date <= to {
					filtered = append(filtered, row)
				}
			}
			rows = filtered
		} else {
			// shortcut: retry narrow ranges when the shared window fails or exceeds the cap; use streaming grouping if large windows are common.
			shared = false
		}
	}
	if !shared {
		rows, e = s.monthRows(ctx, p, from, to)
		if e != nil {
			return nil, e
		}
	}
	if len(rows) > 5000 {
		return nil, fmt.Errorf("monthly query exceeds 5000 rows")
	}
	type monthTotals struct {
		lines map[string]*big.Rat
		total *big.Rat
	}
	months := map[string]*monthTotals{}
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
			m = &monthTotals{lines: map[string]*big.Rat{}, total: new(big.Rat)}
			months[month] = m
		}
		line := mapping[r.ID.Account]
		if line == "" {
			line = "unmapped"
		}
		if m.lines[line] == nil {
			m.lines[line] = new(big.Rat)
		}
		value := amount.Rat()
		m.lines[line].Add(m.lines[line], value)
		m.total.Add(m.total, value)
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
			result := Month{Month: key, ByLine: make(map[string]float64, len(m.lines))}
			for line, amount := range m.lines {
				result.ByLine[line], _ = amount.Float64()
			}
			result.Total, _ = m.total.Float64()
			out = append(out, result)
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
			lines = append(lines, Line{ID: "unmapped", Name: "Unmapped expenses", Limit: ledger.MustDecimal("0"), Accounts: []string{}, Synthetic: true})
			break
		}
	}
	for _, l := range lines {
		r := StatusLine{ID: l.ID, Name: l.Name, Revision: l.Revision, Limit: l.Limit.Float(), LimitExact: l.Limit.String(), Accounts: l.Accounts, LiteralAccounts: l.LiteralAccounts, GroupRef: l.GroupRef, Synthetic: l.Synthetic, Status: "within"}
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
		cur, e := s.repo.db.Query(ctx, `SELECT id FROM accounts WHERE user_id=@user AND type='expense' AND closed_at IS NULL ORDER BY id`, pgx.NamedArgs{"user": p.UserID})
		if e != nil {
			return Status{}, e
		}
		expenses, e = pgx.CollectRows(cur, pgx.RowToStructByName[accountID])
		if e != nil {
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
