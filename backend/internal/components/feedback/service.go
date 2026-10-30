// Package feedback is what people send back: flags on numbers, page usage and their own saved views.
package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"slices"
	"strings"
	"time"
)

type Target struct {
	Dashboard string         `json:"dashboard,omitempty" bson:"dashboard,omitempty"`
	Component string         `json:"component,omitempty" bson:"component,omitempty"`
	Metric    string         `json:"metric,omitempty" bson:"metric,omitempty"`
	Value     string         `json:"value,omitempty" bson:"value,omitempty"`
	Params    map[string]any `json:"params,omitempty" bson:"params,omitempty"`
}
type Flag struct {
	ID         string          `json:"id" bson:"_id"`
	UserID     string          `json:"-" bson:"user_id"`
	Target     Target          `json:"target" bson:"target"`
	Note       string          `json:"note" bson:"note"`
	Status     string          `json:"status" bson:"status"`
	Resolution string          `json:"resolution,omitempty" bson:"resolution,omitempty"`
	Answer     string          `json:"answer,omitempty" bson:"answer,omitempty"`
	CreatedBy  principal.Actor `json:"created_by" bson:"created_by"`
	CreatedAt  time.Time       `json:"created_at" bson:"created_at"`
	ResolvedBy principal.Actor `json:"resolved_by,omitzero" bson:"resolved_by,omitempty"`
	ResolvedAt *time.Time      `json:"resolved_at,omitempty" bson:"resolved_at,omitempty"`
}
type FlagInput struct {
	Target Target `json:"target"`
	Note   string `json:"note"`
}
type ResolveInput struct {
	ID         string `json:"id"`
	Resolution string `json:"resolution" jsonschema:"fixed, explained, wont_fix or duplicate"`
	Note       string `json:"note,omitempty" jsonschema:"what the person who flagged it should read"`
}
type ListInput struct {
	Status string `json:"status,omitempty" jsonschema:"open, resolved or all; default open"`
	Limit  int    `json:"limit,omitempty" jsonschema:"1..200, default 50"`
}
type UsageBeacon struct {
	Path      string `json:"path"`
	Dashboard string `json:"dashboard,omitempty"`
}
type UsageRow struct {
	Path      string `json:"path"`
	Dashboard string `json:"dashboard,omitempty"`
	Views     int64  `json:"views"`
	LastAt    string `json:"last_at"`
}
type UsageInput struct {
	From string `json:"from,omitempty" jsonschema:"YYYY-MM-DD, default 30 days ago"`
	To   string `json:"to,omitempty" jsonschema:"YYYY-MM-DD, default today"`
}
type UsageReport struct {
	From  string     `json:"from"`
	To    string     `json:"to"`
	Pages []UsageRow `json:"pages"`
}

// SavedView is one person's remembered page state; never shared.
type SavedView struct {
	Key    string          `json:"-" bson:"_id"`
	UserID string          `json:"-" bson:"user_id"`
	ID     string          `json:"id" bson:"id"`
	Name   string          `json:"name" bson:"name"`
	Page   string          `json:"page" bson:"page"`
	State  json.RawMessage `json:"state,omitempty" bson:"state,omitempty"`
}
type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo  *MongoRepository
	audit *audit.Service
}

func NewMongoRepository(db *mongo.Database) *MongoRepository   { return &MongoRepository{db} }
func NewService(r *MongoRepository, a *audit.Service) *Service { return &Service{r, a} }

func (s *Service) Flag(ctx context.Context, p principal.Principal, in FlagInput) (Flag, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Flag{}, e
	}
	if strings.TrimSpace(in.Note) == "" && in.Target.Value == "" {
		return Flag{}, fmt.Errorf("say what looks wrong")
	}
	params, e := json.Marshal(in.Target.Params)
	if e != nil || len(params) > 4096 || len(in.Note) > 2000 || len(in.Target.Dashboard) > 100 || len(in.Target.Component) > 100 || len(in.Target.Metric) > 100 || len(in.Target.Value) > 200 {
		return Flag{}, fmt.Errorf("note at most 2000 characters, target fields at most 200 and params at most 4 KB")
	}
	flag := Flag{ID: ulid.Make().String(), UserID: p.UserID, Target: in.Target, Note: in.Note, Status: "open", CreatedBy: p.Actor, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	_, e = s.audit.Write(audit.WithSummary(ctx, "Flagged a number for an agent to check"), p, "feedback_flag", audit.Target{Collection: "feedback", ID: flag.ID}, "", nil, flag, func(tx context.Context) error {
		_, e := s.repo.db.Collection("feedback").InsertOne(tx, flag)
		return e
	})
	return flag, e
}

func (s *Service) List(ctx context.Context, p principal.Principal, in ListInput) ([]Flag, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Limit < 1 || in.Limit > 200 {
		return nil, fmt.Errorf("limit must be 1..200")
	}
	f := bson.M{"user_id": p.UserID}
	switch in.Status {
	case "", "open":
		f["status"] = "open"
	case "resolved":
		f["status"] = "resolved"
	case "all":
	default:
		return nil, fmt.Errorf("status must be open, resolved or all")
	}
	cur, e := s.repo.db.Collection("feedback").Find(ctx, f, options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).SetLimit(int64(in.Limit)))
	if e != nil {
		return nil, e
	}
	out := []Flag{}
	return out, cur.All(ctx, &out)
}

func (s *Service) OpenCount(ctx context.Context, p principal.Principal) (int64, error) {
	if e := p.Require("ledger:read"); e != nil {
		return 0, e
	}
	return s.repo.db.Collection("feedback").CountDocuments(ctx, bson.M{"user_id": p.UserID, "status": "open"})
}

func (s *Service) load(ctx context.Context, p principal.Principal, id string) (Flag, error) {
	var flag Flag
	e := s.repo.db.Collection("feedback").FindOne(ctx, bson.M{"_id": id, "user_id": p.UserID}).Decode(&flag)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return Flag{}, principal.ErrNotFound
	}
	return flag, e
}

func (s *Service) Resolve(ctx context.Context, p principal.Principal, in ResolveInput) (Flag, error) {
	if e := p.Require("dash:write"); e != nil {
		return Flag{}, e
	}
	if !slices.Contains([]string{"fixed", "explained", "wont_fix", "duplicate"}, in.Resolution) || len(in.Note) > 2000 {
		return Flag{}, fmt.Errorf("resolution must be fixed, explained, wont_fix or duplicate, and note at most 2000 characters")
	}
	var out Flag
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		old, e := s.load(tx, p, in.ID)
		if e != nil {
			return e
		}
		if old.Status != "open" {
			return fmt.Errorf("this flag is already resolved: %w", principal.ErrConflict)
		}
		out = old
		now := time.Now().UTC().Truncate(time.Millisecond)
		out.Status, out.Resolution, out.Answer, out.ResolvedBy, out.ResolvedAt = "resolved", in.Resolution, in.Note, p.Actor, &now
		summary := map[string]string{"fixed": "Fixed a flagged number", "explained": "Explained a flagged number", "wont_fix": "Closed a flagged number without a change", "duplicate": "Closed a duplicate flag"}[in.Resolution]
		_, e = s.audit.Write(audit.WithSummary(tx, summary), p, "feedback_resolve", audit.Target{Collection: "feedback", ID: in.ID}, in.Note, old, out, func(tx context.Context) error {
			_, e := s.repo.db.Collection("feedback").ReplaceOne(tx, bson.M{"_id": in.ID, "user_id": p.UserID}, out)
			return e
		})
		return e
	})
	return out, e
}

// Reopen is the inverse of Resolve.
func (s *Service) Reopen(ctx context.Context, p principal.Principal, id, reason string) error {
	if e := p.Require("dash:write"); e != nil {
		return e
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		old, e := s.load(tx, p, id)
		if e != nil {
			return e
		}
		next := old
		next.Status, next.Resolution, next.Answer, next.ResolvedBy, next.ResolvedAt = "open", "", "", principal.Actor{}, nil
		_, e = s.audit.Write(audit.WithSummary(tx, "Reopened a flagged number"), p, "feedback_reopen", audit.Target{Collection: "feedback", ID: id}, reason, old, next, func(tx context.Context) error {
			_, e := s.repo.db.Collection("feedback").ReplaceOne(tx, bson.M{"_id": id, "user_id": p.UserID}, next)
			return e
		})
		return e
	})
}

// Record counts a page view by day. Usage is deliberately not audited: a beacon must not invalidate every cached view.
func (s *Service) Record(ctx context.Context, p principal.Principal, in UsageBeacon) error {
	if e := p.Require("ledger:read"); e != nil {
		return e
	}
	path, _, _ := strings.Cut(in.Path, "?")
	if !strings.HasPrefix(path, "/") || len(path) > 200 || len(in.Dashboard) > 100 {
		return fmt.Errorf("path must be an app path of at most 200 characters")
	}
	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	key := p.UserID + "|" + date + "|" + path + "|" + in.Dashboard
	_, e := s.repo.db.Collection("usage").UpdateOne(ctx, bson.M{"_id": key}, bson.M{"$inc": bson.M{"views": int64(1)}, "$max": bson.M{"last_at": now.Format(time.RFC3339)}, "$setOnInsert": bson.M{"user_id": p.UserID, "date": date, "path": path, "dashboard": in.Dashboard}}, options.UpdateOne().SetUpsert(true))
	return e
}

func (s *Service) Usage(ctx context.Context, p principal.Principal, in UsageInput) (UsageReport, error) {
	if e := p.Require("ledger:read"); e != nil {
		return UsageReport{}, e
	}
	now := time.Now().UTC()
	if in.To == "" {
		in.To = now.Format("2006-01-02")
	}
	if in.From == "" {
		in.From = now.AddDate(0, 0, -30).Format("2006-01-02")
	}
	for _, date := range []string{in.From, in.To} {
		if _, e := time.Parse("2006-01-02", date); e != nil {
			return UsageReport{}, fmt.Errorf("from and to must be YYYY-MM-DD")
		}
	}
	pipeline := bson.A{
		bson.M{"$match": bson.M{"user_id": p.UserID, "date": bson.M{"$gte": in.From, "$lte": in.To}}},
		bson.M{"$group": bson.M{"_id": bson.M{"path": "$path", "dashboard": "$dashboard"}, "views": bson.M{"$sum": "$views"}, "last_at": bson.M{"$max": "$last_at"}}},
		bson.M{"$sort": bson.D{{Key: "views", Value: -1}, {Key: "_id.path", Value: 1}}},
		bson.M{"$limit": 200},
	}
	cur, e := s.repo.db.Collection("usage").Aggregate(ctx, pipeline)
	if e != nil {
		return UsageReport{}, e
	}
	var rows []struct {
		ID struct {
			Path      string `bson:"path"`
			Dashboard string `bson:"dashboard"`
		} `bson:"_id"`
		Views  int64  `bson:"views"`
		LastAt string `bson:"last_at"`
	}
	if e = cur.All(ctx, &rows); e != nil {
		return UsageReport{}, e
	}
	out := UsageReport{From: in.From, To: in.To, Pages: []UsageRow{}}
	for _, row := range rows {
		out.Pages = append(out.Pages, UsageRow{row.ID.Path, row.ID.Dashboard, row.Views, row.LastAt})
	}
	return out, nil
}

func (s *Service) SavedViews(ctx context.Context, p principal.Principal) ([]SavedView, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	cur, e := s.repo.db.Collection("saved_views").Find(ctx, bson.M{"user_id": p.UserID}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}).SetLimit(200))
	if e != nil {
		return nil, e
	}
	out := []SavedView{}
	return out, cur.All(ctx, &out)
}

// SaveView keeps personal page state; like other browser preferences it is not an audited finance write.
func (s *Service) SaveView(ctx context.Context, p principal.Principal, v SavedView) (SavedView, error) {
	if e := p.Require("ledger:read"); e != nil {
		return SavedView{}, e
	}
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 100 || !strings.HasPrefix(v.Page, "/") || len(v.Page) > 200 || len(v.State) > 16*1024 {
		return SavedView{}, fmt.Errorf("a view needs a name of 1..100 characters, an app page and state of at most 16 KB")
	}
	if v.ID == "" {
		v.ID = ulid.Make().String()
		n, e := s.repo.db.Collection("saved_views").CountDocuments(ctx, bson.M{"user_id": p.UserID})
		if e != nil {
			return SavedView{}, e
		}
		if n >= 200 {
			return SavedView{}, fmt.Errorf("at most 200 saved views; delete one first")
		}
	}
	if len(v.ID) > 100 {
		return SavedView{}, fmt.Errorf("invalid view id")
	}
	v.UserID, v.Key = p.UserID, p.UserID+":"+v.ID
	_, e := s.repo.db.Collection("saved_views").ReplaceOne(ctx, bson.M{"_id": v.Key, "user_id": p.UserID}, v, options.Replace().SetUpsert(true))
	return v, e
}

func (s *Service) DeleteView(ctx context.Context, p principal.Principal, id string) error {
	if e := p.Require("ledger:read"); e != nil {
		return e
	}
	result, e := s.repo.db.Collection("saved_views").DeleteOne(ctx, bson.M{"_id": p.UserID + ":" + id, "user_id": p.UserID})
	if e == nil && result.DeletedCount != 1 {
		return principal.ErrNotFound
	}
	return e
}
