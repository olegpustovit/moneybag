// Package semantic holds the shared meaning agents define: metrics, labels, payee aliases, annotations and navigation.
package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Metric struct {
	Key         string   `json:"-" bson:"_id" db:"storage_key"`
	UserID      string   `json:"-" bson:"user_id" db:"user_id"`
	ID          string   `json:"id" bson:"id" db:"id" jsonschema:"lowercase letters, digits, _ or -"`
	Label       string   `json:"label" bson:"label" db:"label" jsonschema:"human name for sentences and titles"`
	Description string   `json:"description,omitempty" bson:"description,omitempty" db:"description"`
	Unit        string   `json:"unit" bson:"unit" db:"unit" jsonschema:"eur, usd (at each entry's date), int (count of postings), pct, mo or days (native sum)"`
	Kind        string   `json:"kind" bson:"kind" db:"kind" jsonschema:"flow (period sum), balance (holdings at period end) or rate (flow per month, or per)"`
	Additivity  string   `json:"additivity" bson:"additivity" db:"additivity" jsonschema:"sum, last, avg or none; how the total folds over time"`
	Per         string   `json:"per,omitempty" bson:"per,omitempty" db:"per" jsonschema:"day, week or month (default); unit a rate or avg averages over"`
	Estimates   string   `json:"estimates" bson:"estimates" db:"estimates" jsonschema:"include, exclude or split; how estimated accounts count"`
	Accounts    []string `json:"accounts,omitempty" bson:"accounts,omitempty" db:"accounts" jsonschema:"account id prefixes, such as expenses:"`
	Exclude     []string `json:"exclude,omitempty" bson:"exclude,omitempty" db:"exclude" jsonschema:"account id prefixes to leave out"`
	Sign        int      `json:"sign,omitempty" bson:"sign,omitempty" db:"sign" jsonschema:"1 or -1; -1 shows income, stored as negative postings, as positive"`
	Where       string   `json:"where,omitempty" bson:"where,omitempty" db:"where" jsonschema:"escape hatch: boolean CEL over row (date, payee, tags, account, class, groups, commodity, amount, value_eur, value_usd, estimated)"`
	Dimensions  []string `json:"dimensions,omitempty" bson:"dimensions,omitempty" db:"dimensions" jsonschema:"dimensions it splits by; the first is the default"`
}
type LabelInput struct {
	Kind  string `json:"kind" jsonschema:"account, category, pot, tag, dimension or series"`
	Key   string `json:"key"`
	Label string `json:"label"`
}
type Label struct {
	Doc   string `json:"-" bson:"_id" db:"storage_key"`
	User  string `json:"-" bson:"user_id" db:"user_id"`
	Kind  string `json:"kind" bson:"kind" db:"kind"`
	Key   string `json:"key" bson:"key" db:"key"`
	Label string `json:"label" bson:"label" db:"label"`
}
type PayeeAlias struct {
	Key     string `json:"-" bson:"_id" db:"storage_key"`
	UserID  string `json:"-" bson:"user_id" db:"user_id"`
	Pattern string `json:"pattern" bson:"pattern" db:"pattern" jsonschema:"case-insensitive RE2 regular expression over the bank's payee text"`
	Name    string `json:"name" bson:"name" db:"name"`
	Note    string `json:"note,omitempty" bson:"note,omitempty" db:"note"`
}
type Annotation struct {
	Key     string `json:"-" bson:"_id" db:"storage_key"`
	UserID  string `json:"-" bson:"user_id" db:"user_id"`
	ID      string `json:"id" bson:"id" db:"id"`
	Label   string `json:"label" bson:"label" db:"label"`
	From    string `json:"from,omitempty" bson:"from,omitempty" db:"from" jsonschema:"YYYY-MM-DD; may be omitted when tag_rule supplies the dates"`
	To      string `json:"to,omitempty" bson:"to,omitempty" db:"to"`
	TagRule string `json:"tag_rule,omitempty" bson:"tag_rule,omitempty" db:"tag_rule" jsonschema:"tag, or tag prefix ending in *, such as trip:*; its entries give the dates"`
	Kind    string `json:"kind,omitempty" bson:"kind,omitempty" db:"kind" jsonschema:"note, trip or event"`
}
type NavItem struct {
	ID        string `json:"id" bson:"id"`
	Label     string `json:"label" bson:"label"`
	Icon      string `json:"icon,omitempty" bson:"icon,omitempty"`
	Dashboard string `json:"dashboard,omitempty" bson:"dashboard,omitempty" jsonschema:"dashboard id this item opens; give this or route"`
	Route     string `json:"route,omitempty" bson:"route,omitempty" jsonschema:"app path this item opens, such as /ledger; give this or dashboard"`
}
type navDoc struct {
	Key    string    `json:"-" bson:"_id" db:"storage_key"`
	UserID string    `json:"-" bson:"user_id" db:"user_id"`
	Items  []NavItem `json:"items" bson:"items" db:"items"`
}
type PostgresRepository struct{ db *pgxpool.Pool }
type Service struct {
	repo   *PostgresRepository
	audit  *audit.Service
	ledger *ledger.Service
	rates  *rates.Service
	expr   *expressions.Engine
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository { return &PostgresRepository{db} }
func NewService(r *PostgresRepository, l *ledger.Service, q *rates.Service, a *audit.Service, e *expressions.Engine) *Service {
	return &Service{r, a, l, q, e}
}

var semanticID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var accountPrefix = regexp.MustCompile(`^(assets|liabilities|income|expenses|equity):[a-z0-9_:-]{0,150}$`)
var dateOnly = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func validDate(s string) bool {
	if !dateOnly.MatchString(s) {
		return false
	}
	_, e := time.Parse("2006-01-02", s)
	return e == nil
}

func oneOf(field, value string, allowed ...string) error {
	if !slices.Contains(allowed, value) {
		return fmt.Errorf("%s must be one of %s", field, strings.Join(allowed, ", "))
	}
	return nil
}

func validLabel(s string) bool { return strings.TrimSpace(s) != "" && len(s) <= 200 }

func (s *Service) validate(m *Metric) error {
	if !semanticID.MatchString(m.ID) {
		return fmt.Errorf("id must be 1..64 lowercase letters, digits, _ or -, starting with a letter")
	}
	if !validLabel(m.Label) || len(m.Description) > 2000 {
		return fmt.Errorf("label must be 1..200 and description at most 2000 characters")
	}
	if e := errors.Join(oneOf("unit", m.Unit, "eur", "usd", "int", "pct", "mo", "days"), oneOf("kind", m.Kind, "flow", "balance", "rate"), oneOf("additivity", m.Additivity, "sum", "last", "avg", "none"), oneOf("estimates", m.Estimates, "include", "exclude", "split")); e != nil {
		return e
	}
	if e := oneOf("per", m.Per, "", "day", "week", "month"); e != nil {
		return e
	}
	if m.Per != "" && m.Kind != "rate" && m.Additivity != "avg" {
		return fmt.Errorf("per needs a rate kind or avg additivity")
	}
	if m.Sign == 0 {
		m.Sign = 1
	}
	if m.Sign != 1 && m.Sign != -1 {
		return fmt.Errorf("sign must be 1 or -1")
	}
	if len(m.Accounts) == 0 && m.Where == "" {
		return fmt.Errorf("metric needs accounts or where")
	}
	if len(m.Accounts) > 100 || len(m.Exclude) > 100 || len(m.Dimensions) > 20 {
		return fmt.Errorf("at most 100 accounts, 100 exclude prefixes and 20 dimensions")
	}
	for _, prefix := range append(slices.Clone(m.Accounts), m.Exclude...) {
		if !accountPrefix.MatchString(prefix) {
			return fmt.Errorf("account prefix %q must start with assets:, liabilities:, income:, expenses: or equity:", prefix)
		}
	}
	if m.Where != "" {
		if _, e := s.expr.Compile(m.Where); e != nil {
			return fmt.Errorf("where: %w", e)
		}
	}
	return nil
}

func read[T any](ctx context.Context, db *pgxpool.Pool, query, key string) (*T, error) {
	rows, err := db.Query(ctx, query, pgx.NamedArgs{"key": key})
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[T])
	if postgres.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}
func list[T any](ctx context.Context, db *pgxpool.Pool, query, coll, user string) ([]T, error) {
	rows, err := db.Query(ctx, query, pgx.NamedArgs{"user": user})
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if err != nil {
		return nil, err
	}
	if len(out) > 1000 {
		return nil, fmt.Errorf("%s exceeds 1000 records", coll)
	}
	if out == nil {
		out = []T{}
	}
	return out, nil
}

// put replaces one owned document; its _id already embeds the owner, so a key can never reach another user's record.
func (s *Service) put(ctx context.Context, p principal.Principal, tool, coll, target, key, summary string, before, next any) error {
	_, e := s.audit.Write(audit.WithSummary(ctx, summary), p, tool, audit.Target{Collection: coll, ID: target}, "", before, next, func(tx context.Context) error {
		var e error
		switch coll {
		case "metrics":
			v := next.(Metric)
			_, e = s.repo.db.Exec(tx, `INSERT INTO metrics(storage_key,user_id,id,label,description,unit,kind,additivity,per,estimates,accounts,exclude,sign,"where",dimensions) VALUES (@key,@user,@id,@label,@description,@unit,@kind,@additivity,@per,@estimates,@accounts,@exclude,@sign,@where,@dimensions) ON CONFLICT(storage_key) DO UPDATE SET id=EXCLUDED.id,label=EXCLUDED.label,description=EXCLUDED.description,unit=EXCLUDED.unit,kind=EXCLUDED.kind,additivity=EXCLUDED.additivity,per=EXCLUDED.per,estimates=EXCLUDED.estimates,accounts=EXCLUDED.accounts,exclude=EXCLUDED.exclude,sign=EXCLUDED.sign,"where"=EXCLUDED."where",dimensions=EXCLUDED.dimensions WHERE metrics.user_id=EXCLUDED.user_id`, pgx.NamedArgs{"key": key, "user": p.UserID, "id": v.ID, "label": v.Label, "description": v.Description, "unit": v.Unit, "kind": v.Kind, "additivity": v.Additivity, "per": v.Per, "estimates": v.Estimates, "accounts": v.Accounts, "exclude": v.Exclude, "sign": v.Sign, "where": v.Where, "dimensions": v.Dimensions})
		case "labels":
			v := next.(Label)
			_, e = s.repo.db.Exec(tx, `INSERT INTO labels(storage_key,user_id,kind,key,label) VALUES (@key,@user,@kind,@logical,@label) ON CONFLICT(storage_key) DO UPDATE SET kind=EXCLUDED.kind,key=EXCLUDED.key,label=EXCLUDED.label WHERE labels.user_id=EXCLUDED.user_id`, pgx.NamedArgs{"key": key, "user": p.UserID, "kind": v.Kind, "logical": v.Key, "label": v.Label})
		case "payee_aliases":
			v := next.(PayeeAlias)
			_, e = s.repo.db.Exec(tx, `INSERT INTO payee_aliases(storage_key,user_id,pattern,name,note) VALUES (@key,@user,@pattern,@name,@note) ON CONFLICT(storage_key) DO UPDATE SET pattern=EXCLUDED.pattern,name=EXCLUDED.name,note=EXCLUDED.note WHERE payee_aliases.user_id=EXCLUDED.user_id`, pgx.NamedArgs{"key": key, "user": p.UserID, "pattern": v.Pattern, "name": v.Name, "note": v.Note})
		case "annotations":
			v := next.(Annotation)
			_, e = s.repo.db.Exec(tx, `INSERT INTO annotations(storage_key,user_id,id,label,"from","to",tag_rule,kind) VALUES (@key,@user,@id,@label,@from,@to,@rule,@kind) ON CONFLICT(storage_key) DO UPDATE SET id=EXCLUDED.id,label=EXCLUDED.label,"from"=EXCLUDED."from","to"=EXCLUDED."to",tag_rule=EXCLUDED.tag_rule,kind=EXCLUDED.kind WHERE annotations.user_id=EXCLUDED.user_id`, pgx.NamedArgs{"key": key, "user": p.UserID, "id": v.ID, "label": v.Label, "from": v.From, "to": v.To, "rule": v.TagRule, "kind": v.Kind})
		case "nav":
			v := next.(navDoc)
			_, e = s.repo.db.Exec(tx, `INSERT INTO nav(storage_key,user_id,items) VALUES (@key,@user,COALESCE(@items::jsonb,'null'::jsonb)) ON CONFLICT(storage_key) DO UPDATE SET items=EXCLUDED.items WHERE nav.user_id=EXCLUDED.user_id`, pgx.NamedArgs{"key": key, "user": p.UserID, "items": v.Items})
		default:
			return fmt.Errorf("unknown semantic collection %q", coll)
		}
		return e
	})
	return e
}

func (s *Service) drop(ctx context.Context, p principal.Principal, tool, coll, target, key, summary string, before any) error {
	_, e := s.audit.Write(audit.WithSummary(ctx, summary), p, tool, audit.Target{Collection: coll, ID: target}, "", before, nil, func(tx context.Context) error {
		var query string
		switch coll {
		case "metrics":
			query = `DELETE FROM metrics WHERE storage_key=@key AND user_id=@user`
		case "labels":
			query = `DELETE FROM labels WHERE storage_key=@key AND user_id=@user`
		case "payee_aliases":
			query = `DELETE FROM payee_aliases WHERE storage_key=@key AND user_id=@user`
		case "annotations":
			query = `DELETE FROM annotations WHERE storage_key=@key AND user_id=@user`
		case "nav":
			query = `DELETE FROM nav WHERE storage_key=@key AND user_id=@user`
		default:
			return fmt.Errorf("unknown semantic collection %q", coll)
		}
		_, e := s.repo.db.Exec(tx, query, pgx.NamedArgs{"key": key, "user": p.UserID})
		return e
	})
	return e
}

func (s *Service) UpsertMetric(ctx context.Context, p principal.Principal, m Metric) (Metric, error) {
	if e := p.Require("dash:write"); e != nil {
		return Metric{}, e
	}
	if e := s.validate(&m); e != nil {
		return Metric{}, e
	}
	m.UserID, m.Key = p.UserID, p.UserID+":"+m.ID
	summary := "Defined the " + m.Label + " metric"
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		old, e := read[Metric](tx, s.repo.db, `SELECT * FROM metrics WHERE storage_key=@key`, m.Key)
		if e != nil {
			return e
		}
		if old != nil {
			summary = "Changed the " + m.Label + " metric"
		}
		return s.put(tx, p, "metric_upsert", "metrics", m.ID, m.Key, summary, old, m)
	})
	return m, e
}

func (s *Service) Metrics(ctx context.Context, p principal.Principal) ([]Metric, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	return list[Metric](ctx, s.repo.db, `SELECT * FROM metrics WHERE user_id=@user ORDER BY "id" LIMIT 1001`, "metrics", p.UserID)
}

func (s *Service) Metric(ctx context.Context, p principal.Principal, id string) (Metric, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Metric{}, e
	}
	m, e := read[Metric](ctx, s.repo.db, `SELECT * FROM metrics WHERE storage_key=@key`, p.UserID+":"+id)
	if e != nil {
		return Metric{}, e
	}
	if m == nil {
		return Metric{}, fmt.Errorf("metric %q: %w; read the catalog with metric_list", id, principal.ErrNotFound)
	}
	return *m, nil
}

func (s *Service) SetLabel(ctx context.Context, p principal.Principal, in LabelInput) (Label, error) {
	if e := p.Require("dash:write"); e != nil {
		return Label{}, e
	}
	if e := oneOf("kind", in.Kind, "account", "category", "pot", "tag", "dimension", "series"); e != nil {
		return Label{}, e
	}
	if in.Key == "" || len(in.Key) > 200 || !validLabel(in.Label) {
		return Label{}, fmt.Errorf("key must be 1..200 and label 1..200 characters")
	}
	l := Label{Doc: p.UserID + ":" + in.Kind + ":" + in.Key, User: p.UserID, Kind: in.Kind, Key: in.Key, Label: in.Label}
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		old, e := read[Label](tx, s.repo.db, `SELECT * FROM labels WHERE storage_key=@key`, l.Doc)
		if e != nil {
			return e
		}
		return s.put(tx, p, "label_set", "labels", in.Kind+":"+in.Key, l.Doc, "Named a "+in.Kind+" "+in.Label, old, l)
	})
	return l, e
}

// Labels is keyed kind:key, the same keys the cube and bootstrap hand to the browser.
func (s *Service) Labels(ctx context.Context, p principal.Principal) (map[string]string, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	rows, e := list[Label](ctx, s.repo.db, `SELECT * FROM labels WHERE user_id=@user ORDER BY "key" LIMIT 1001`, "labels", p.UserID)
	if e != nil {
		return nil, e
	}
	out := make(map[string]string, len(rows))
	for _, l := range rows {
		out[l.Kind+":"+l.Key] = l.Label
	}
	return out, nil
}

func (s *Service) UpsertAlias(ctx context.Context, p principal.Principal, a PayeeAlias) (PayeeAlias, error) {
	if e := p.Require("dash:write"); e != nil {
		return PayeeAlias{}, e
	}
	if a.Pattern == "" || len(a.Pattern) > 300 || !validLabel(a.Name) || len(a.Note) > 1000 {
		return PayeeAlias{}, fmt.Errorf("pattern must be 1..300, name 1..200 and note at most 1000 characters")
	}
	if _, e := regexp.Compile("(?i)" + a.Pattern); e != nil {
		return PayeeAlias{}, fmt.Errorf("pattern: %w", e)
	}
	a.UserID, a.Key = p.UserID, p.UserID+":"+a.Pattern
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		old, e := read[PayeeAlias](tx, s.repo.db, `SELECT * FROM payee_aliases WHERE storage_key=@key`, a.Key)
		if e != nil {
			return e
		}
		return s.put(tx, p, "payee_alias_upsert", "payee_aliases", a.Pattern, a.Key, "Named a payee "+a.Name, old, a)
	})
	return a, e
}

func (s *Service) UpsertAnnotation(ctx context.Context, p principal.Principal, a Annotation) (Annotation, error) {
	if e := p.Require("dash:write"); e != nil {
		return Annotation{}, e
	}
	if !semanticID.MatchString(a.ID) || !validLabel(a.Label) || len(a.TagRule) > 100 {
		return Annotation{}, fmt.Errorf("id must be a lowercase identifier, label 1..200 and tag_rule at most 100 characters")
	}
	if a.Kind == "" {
		a.Kind = "note"
	}
	if e := oneOf("kind", a.Kind, "note", "trip", "event"); e != nil {
		return Annotation{}, e
	}
	if a.From == "" && a.TagRule == "" {
		return Annotation{}, fmt.Errorf("annotation needs from or tag_rule")
	}
	if a.From != "" && !validDate(a.From) || a.To != "" && !validDate(a.To) || a.From != "" && a.To != "" && a.To < a.From {
		return Annotation{}, fmt.Errorf("from and to must be YYYY-MM-DD with from at or before to")
	}
	a.UserID, a.Key = p.UserID, p.UserID+":"+a.ID
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		old, e := read[Annotation](tx, s.repo.db, `SELECT * FROM annotations WHERE storage_key=@key`, a.Key)
		if e != nil {
			return e
		}
		return s.put(tx, p, "annotation_upsert", "annotations", a.ID, a.Key, "Marked "+a.Label+" on the charts", old, a)
	})
	return a, e
}

func (s *Service) SetNav(ctx context.Context, p principal.Principal, items []NavItem) ([]NavItem, error) {
	if e := p.Require("dash:write"); e != nil {
		return nil, e
	}
	if len(items) > 20 {
		return nil, fmt.Errorf("at most 20 navigation items")
	}
	seen := map[string]bool{}
	for _, item := range items {
		if !semanticID.MatchString(item.ID) || seen[item.ID] || !validLabel(item.Label) || len(item.Label) > 60 || len(item.Icon) > 40 {
			return nil, fmt.Errorf("each item needs a unique lowercase id and a label of 1..60 characters")
		}
		seen[item.ID] = true
		if (item.Dashboard == "") == (item.Route == "") {
			return nil, fmt.Errorf("item %s needs exactly one of dashboard or route", item.ID)
		}
		if item.Route != "" && (!strings.HasPrefix(item.Route, "/") || strings.HasPrefix(item.Route, "//") || len(item.Route) > 200 || strings.ContainsAny(item.Route, "\\\r\n")) {
			return nil, fmt.Errorf("item %s route must be an app path such as /ledger", item.ID)
		}
	}
	doc := navDoc{Key: p.UserID, UserID: p.UserID, Items: items}
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		old, e := read[navDoc](tx, s.repo.db, `SELECT * FROM nav WHERE storage_key=@key`, doc.Key)
		if e != nil {
			return e
		}
		return s.put(tx, p, "nav_set", "nav", "nav", doc.Key, "Changed the navigation", old, doc)
	})
	return items, e
}

// Nav returns the saved navigation, or nil so the browser falls back to its default.
func (s *Service) Nav(ctx context.Context, p principal.Principal) ([]NavItem, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	doc, e := read[navDoc](ctx, s.repo.db, `SELECT * FROM nav WHERE storage_key=@key`, p.UserID)
	if e != nil || doc == nil {
		return nil, e
	}
	return doc.Items, nil
}

// Restore is the inverse of one semantic write: it puts back the record from before the event, or removes one the event created.
func (s *Service) Restore(ctx context.Context, p principal.Principal, event audit.Event) error {
	if e := p.Require("dash:write"); e != nil {
		return e
	}
	before := event.Before
	if event.Outcome != nil {
		before = event.Outcome.Before
	}
	id := event.Target.ID
	created := len(before) == 0 || string(before) == "null"
	switch event.Tool {
	case "metric_upsert":
		key := p.UserID + ":" + id
		if created {
			return s.drop(ctx, p, "metric_upsert", "metrics", id, key, "Removed a metric", nil)
		}
		var old Metric
		if e := json.Unmarshal(before, &old); e != nil {
			return e
		}
		old.Key, old.UserID = key, p.UserID
		return s.put(ctx, p, "metric_upsert", "metrics", id, key, "Restored the "+old.Label+" metric", nil, old)
	case "label_set":
		key := p.UserID + ":" + id
		if created {
			return s.drop(ctx, p, "label_set", "labels", id, key, "Removed a name", nil)
		}
		var old Label
		if e := json.Unmarshal(before, &old); e != nil {
			return e
		}
		old.Doc, old.User = key, p.UserID
		return s.put(ctx, p, "label_set", "labels", id, key, "Restored the name "+old.Label, nil, old)
	case "payee_alias_upsert":
		key := p.UserID + ":" + id
		if created {
			return s.drop(ctx, p, "payee_alias_upsert", "payee_aliases", id, key, "Removed a payee name", nil)
		}
		var old PayeeAlias
		if e := json.Unmarshal(before, &old); e != nil {
			return e
		}
		old.Key, old.UserID = key, p.UserID
		return s.put(ctx, p, "payee_alias_upsert", "payee_aliases", id, key, "Restored the payee name "+old.Name, nil, old)
	case "annotation_upsert":
		key := p.UserID + ":" + id
		if created {
			return s.drop(ctx, p, "annotation_upsert", "annotations", id, key, "Removed a chart marker", nil)
		}
		var old Annotation
		if e := json.Unmarshal(before, &old); e != nil {
			return e
		}
		old.Key, old.UserID = key, p.UserID
		return s.put(ctx, p, "annotation_upsert", "annotations", id, key, "Restored the chart marker "+old.Label, nil, old)
	case "nav_set":
		if created {
			return s.drop(ctx, p, "nav_set", "nav", "nav", p.UserID, "Restored the default navigation", nil)
		}
		var old navDoc
		if e := json.Unmarshal(before, &old); e != nil {
			return e
		}
		old.Key, old.UserID = p.UserID, p.UserID
		return s.put(ctx, p, "nav_set", "nav", "nav", p.UserID, "Restored the navigation", nil, old)
	}
	return fmt.Errorf("%s has no semantic inverse", event.Tool)
}
