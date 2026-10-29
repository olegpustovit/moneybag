package dashboard

import (
	"context"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"strings"
)

// View is a named, per-user saved Query usable as the data source "view:<id>".
type View struct {
	Key    string `json:"-" bson:"_id"`
	UserID string `json:"-" bson:"user_id"`
	ID     string `json:"id" bson:"id"`
	Query  Query  `json:"query" bson:"query"`
}
type ViewInput struct {
	ID    string `json:"id"`
	Query Query  `json:"query"`
}

const maxViewDepth = 8

type viewStackKey struct{}

func (s *Service) View(ctx context.Context, p principal.Principal, id string) (View, error) {
	if e := p.Require("ledger:read"); e != nil {
		return View{}, e
	}
	var v View
	e := s.repo.db.Collection("views").FindOne(ctx, bson.M{"user_id": p.UserID, "id": id}).Decode(&v)
	if errors.Is(e, mongo.ErrNoDocuments) {
		e = fmt.Errorf("view %q: %w", id, principal.ErrNotFound)
	}
	return v, e
}
func (s *Service) Views(ctx context.Context, p principal.Principal) ([]View, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	cur, e := s.repo.db.Collection("views").Find(ctx, bson.M{"user_id": p.UserID}, options.Find().SetSort(bson.D{{Key: "id", Value: 1}}).SetLimit(501))
	if e != nil {
		return nil, e
	}
	defer cur.Close(ctx)
	out := []View{}
	if e = cur.All(ctx, &out); len(out) > 500 {
		return nil, fmt.Errorf("view catalog exceeds 500")
	}
	return out, e
}

// UpsertView validates the query's expressions and rejects a view that reaches itself through view: sources.
func (s *Service) UpsertView(ctx context.Context, p principal.Principal, in ViewInput) (View, error) {
	if e := p.Require("dash:write"); e != nil {
		return View{}, e
	}
	if !dashboardID.MatchString(in.ID) || in.Query.From == "" {
		return View{}, fmt.Errorf("id must use 1..100 letters, digits, hyphens or underscores, and query.from is required")
	}
	var bad error
	walkExpressions(Spec{Data: map[string]Query{in.ID: in.Query}}, func(path, expr string) {
		if _, e := s.engine.Expr.Compile(expr); e != nil && bad == nil {
			bad = fmt.Errorf("%s: %w", path, e)
		}
	})
	if bad != nil {
		return View{}, bad
	}
	var v View
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		for from, depth := in.Query.From, 0; strings.HasPrefix(from, "view:"); depth++ {
			next := strings.TrimPrefix(from, "view:")
			if next == in.ID || depth > maxViewDepth {
				return fmt.Errorf("view %q would reference itself through view: sources", in.ID)
			}
			n, e := s.View(tx, p, next)
			if e != nil {
				return e
			}
			from = n.Query.From
		}
		old, _ := s.View(tx, p, in.ID)
		v = View{Key: p.UserID + ":" + in.ID, UserID: p.UserID, ID: in.ID, Query: in.Query}
		_, e := s.audit.Write(tx, p, "view_upsert", audit.Target{Collection: "views", ID: in.ID}, "", old, v, func(tx context.Context) error {
			_, e := s.repo.db.Collection("views").ReplaceOne(tx, bson.M{"_id": v.Key}, v, options.Replace().SetUpsert(true))
			return e
		})
		return e
	})
	return v, e
}

// viewRows runs a saved view; the context carries the chain so a cycle (or runaway nesting) fails instead of recursing.
func (s *Sources) viewRows(ctx context.Context, p principal.Principal, id string) ([]map[string]any, error) {
	stack, _ := ctx.Value(viewStackKey{}).([]string)
	for _, seen := range stack {
		if seen == id {
			return nil, fmt.Errorf("view cycle: %s -> %s", strings.Join(stack, " -> "), id)
		}
	}
	if len(stack) >= maxViewDepth {
		return nil, fmt.Errorf("views nest deeper than %d", maxViewDepth)
	}
	v, e := s.Views.View(ctx, p, id)
	if e != nil {
		return nil, e
	}
	ctx = context.WithValue(ctx, viewStackKey{}, append(append([]string(nil), stack...), id))
	params, _ := ctx.Value(paramsKey{}).(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	x := &execution{ctx: ctx, engine: &Engine{Expr: s.Expr, Reader: s}, p: p, vars: map[string]any{"data": map[string]any{}, "params": params, "computed": map[string]any{}, "rates": map[string]any{}}}
	out, e := x.query(v.Query)
	if e != nil {
		return nil, fmt.Errorf("view %s: %w", id, e)
	}
	rows, ok := out.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("view %s must return rows, not as: map", id)
	}
	return rows, nil
}
