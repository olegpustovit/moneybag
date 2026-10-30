// Package semantic holds the shared meaning agents define: metrics, labels, payee aliases, annotations and navigation.
package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Metric struct {
	Key         string   `json:"-" bson:"_id"`
	UserID      string   `json:"-" bson:"user_id"`
	ID          string   `json:"id" bson:"id" jsonschema:"stable id, lowercase letters, digits, _ or -"`
	Label       string   `json:"label" bson:"label" jsonschema:"human name, used in sentences and titles"`
	Description string   `json:"description,omitempty" bson:"description,omitempty"`
	Unit        string   `json:"unit" bson:"unit" jsonschema:"eur, usd (values at each entry's date), int (count of postings), pct, mo or days (sum of native amounts)"`
	Kind        string   `json:"kind" bson:"kind" jsonschema:"flow (sum over the period), balance (holdings at the period end) or rate (flow per month)"`
	Additivity  string   `json:"additivity" bson:"additivity" jsonschema:"sum, last, avg or none; how the total folds over time and whether a table may total it"`
	Estimates   string   `json:"estimates" bson:"estimates" jsonschema:"include, exclude or split; how estimated accounts count"`
	Accounts    []string `json:"accounts,omitempty" bson:"accounts,omitempty" jsonschema:"account id prefixes, such as expenses:"`
	Exclude     []string `json:"exclude,omitempty" bson:"exclude,omitempty" jsonschema:"account id prefixes to leave out"`
	Sign        int      `json:"sign,omitempty" bson:"sign,omitempty" jsonschema:"1 or -1; -1 shows income, stored as negative postings, as positive"`
	Where       string   `json:"where,omitempty" bson:"where,omitempty" jsonschema:"escape hatch: boolean CEL over row (date, payee, tags, account, class, groups, commodity, amount, value_eur, value_usd, estimated)"`
	Dimensions  []string `json:"dimensions,omitempty" bson:"dimensions,omitempty" jsonschema:"dimensions this metric may split by; the first is the default for contributors"`
}
type LabelInput struct {
	Kind  string `json:"kind" jsonschema:"account, category, pot, tag, dimension or series"`
	Key   string `json:"key"`
	Label string `json:"label"`
}
type Label struct {
	Doc   string `json:"-" bson:"_id"`
	User  string `json:"-" bson:"user_id"`
	Kind  string `json:"kind" bson:"kind"`
	Key   string `json:"key" bson:"key"`
	Label string `json:"label" bson:"label"`
}
type PayeeAlias struct {
	Key     string `json:"-" bson:"_id"`
	UserID  string `json:"-" bson:"user_id"`
	Pattern string `json:"pattern" bson:"pattern" jsonschema:"case-insensitive RE2 regular expression over the bank's payee text"`
	Name    string `json:"name" bson:"name"`
	Note    string `json:"note,omitempty" bson:"note,omitempty"`
}
type Annotation struct {
	Key     string `json:"-" bson:"_id"`
	UserID  string `json:"-" bson:"user_id"`
	ID      string `json:"id" bson:"id"`
	Label   string `json:"label" bson:"label"`
	From    string `json:"from,omitempty" bson:"from,omitempty" jsonschema:"YYYY-MM-DD; may be omitted when tag_rule supplies the dates"`
	To      string `json:"to,omitempty" bson:"to,omitempty"`
	TagRule string `json:"tag_rule,omitempty" bson:"tag_rule,omitempty" jsonschema:"tag, or tag prefix ending in *, such as trip:*; its entries give the dates"`
	Kind    string `json:"kind,omitempty" bson:"kind,omitempty" jsonschema:"note, trip or event"`
}
type NavItem struct {
	ID        string `json:"id" bson:"id"`
	Label     string `json:"label" bson:"label"`
	Icon      string `json:"icon,omitempty" bson:"icon,omitempty"`
	Dashboard string `json:"dashboard,omitempty" bson:"dashboard,omitempty" jsonschema:"dashboard id this item opens; give this or route"`
	Route     string `json:"route,omitempty" bson:"route,omitempty" jsonschema:"app path this item opens, such as /ledger; give this or dashboard"`
}
type navDoc struct {
	Key    string    `json:"-" bson:"_id"`
	UserID string    `json:"-" bson:"user_id"`
	Items  []NavItem `json:"items" bson:"items"`
}
type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo   *MongoRepository
	audit  *audit.Service
	ledger *ledger.Service
	rates  *rates.Service
	expr   *expressions.Engine
}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func NewService(r *MongoRepository, l *ledger.Service, q *rates.Service, a *audit.Service, e *expressions.Engine) *Service {
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

func read[T any](ctx context.Context, c *mongo.Collection, key string) (*T, error) {
	var out T
	e := c.FindOne(ctx, bson.M{"_id": key}).Decode(&out)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &out, nil
}

func list[T any](ctx context.Context, c *mongo.Collection, user, sort string) ([]T, error) {
	cur, e := c.Find(ctx, bson.M{"user_id": user}, options.Find().SetSort(bson.D{{Key: sort, Value: 1}}).SetLimit(1001))
	if e != nil {
		return nil, e
	}
	out := []T{}
	if e = cur.All(ctx, &out); e != nil {
		return nil, e
	}
	if len(out) > 1000 {
		return nil, fmt.Errorf("%s exceeds 1000 records", c.Name())
	}
	return out, nil
}

// put replaces one owned document; its _id already embeds the owner, so a key can never reach another user's record.
func (s *Service) put(ctx context.Context, p principal.Principal, tool, coll, target, key, summary string, before, next any) error {
	_, e := s.audit.Write(audit.WithSummary(ctx, summary), p, tool, audit.Target{Collection: coll, ID: target}, "", before, next, func(tx context.Context) error {
		_, e := s.repo.db.Collection(coll).ReplaceOne(tx, bson.M{"_id": key, "user_id": p.UserID}, next, options.Replace().SetUpsert(true))
		return e
	})
	return e
}

func (s *Service) drop(ctx context.Context, p principal.Principal, tool, coll, target, key, summary string, before any) error {
	_, e := s.audit.Write(audit.WithSummary(ctx, summary), p, tool, audit.Target{Collection: coll, ID: target}, "", before, nil, func(tx context.Context) error {
		_, e := s.repo.db.Collection(coll).DeleteOne(tx, bson.M{"_id": key, "user_id": p.UserID})
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
		old, e := read[Metric](tx, s.repo.db.Collection("metrics"), m.Key)
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
	return list[Metric](ctx, s.repo.db.Collection("metrics"), p.UserID, "id")
}

func (s *Service) Metric(ctx context.Context, p principal.Principal, id string) (Metric, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Metric{}, e
	}
	m, e := read[Metric](ctx, s.repo.db.Collection("metrics"), p.UserID+":"+id)
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
		old, e := read[Label](tx, s.repo.db.Collection("labels"), l.Doc)
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
	rows, e := list[Label](ctx, s.repo.db.Collection("labels"), p.UserID, "key")
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
		old, e := read[PayeeAlias](tx, s.repo.db.Collection("payee_aliases"), a.Key)
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
		old, e := read[Annotation](tx, s.repo.db.Collection("annotations"), a.Key)
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
		old, e := read[navDoc](tx, s.repo.db.Collection("nav"), doc.Key)
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
	doc, e := read[navDoc](ctx, s.repo.db.Collection("nav"), p.UserID)
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
