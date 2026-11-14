package dashboard

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"strings"
)

// View is a named, per-user saved Query usable as the data source "view:<id>".
type View struct {
	Key    string `json:"-" bson:"_id" db:"storage_key"`
	UserID string `json:"-" bson:"user_id" db:"user_id"`
	ID     string `json:"id" bson:"id" db:"id"`
	Query  Query  `json:"query" bson:"query" db:"query"`
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
	rows, e := s.repo.db.Query(ctx, `SELECT * FROM views WHERE user_id=@user AND id=@id`, pgx.NamedArgs{"user": p.UserID, "id": id})
	if e != nil {
		return v, e
	}
	v, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[View])
	if postgres.IsNotFound(e) {
		e = fmt.Errorf("view %q: %w", id, principal.ErrNotFound)
	}
	return v, e
}
func (s *Service) Views(ctx context.Context, p principal.Principal) ([]View, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	cur, e := s.repo.db.Query(ctx, `SELECT * FROM views WHERE user_id=@user ORDER BY id LIMIT 501`, pgx.NamedArgs{"user": p.UserID})
	if e != nil {
		return nil, e
	}
	out, e := pgx.CollectRows(cur, pgx.RowToStructByName[View])
	if len(out) > 500 {
		return nil, fmt.Errorf("view catalog exceeds 500")
	}
	if out == nil {
		out = []View{}
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
			_, e := s.repo.db.Exec(tx, `INSERT INTO views(storage_key,user_id,id,query) VALUES (@key,@user,@id,@query) ON CONFLICT(storage_key) DO UPDATE SET id=EXCLUDED.id,query=EXCLUDED.query WHERE views.user_id=EXCLUDED.user_id`, pgx.NamedArgs{"key": v.Key, "user": p.UserID, "id": v.ID, "query": v.Query})
			return e
		})
		return e
	})
	return v, e
}

// viewRows runs a saved view; the context carries the chain so a cycle (or runaway nesting) fails instead of recursing.
func (s *Sources) viewRows(ctx context.Context, p principal.Principal, id string) ([]map[string]any, error) {
	v, _, _, e := s.viewDefinition(ctx, p, id)
	if e != nil {
		return nil, e
	}
	return s.evaluateView(ctx, p, v)
}

func (s *Sources) evaluateView(ctx context.Context, p principal.Principal, v View) ([]map[string]any, error) {
	stack, _ := ctx.Value(viewStackKey{}).([]string)
	id := v.ID
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

func (s *Sources) viewDefinition(ctx context.Context, p principal.Principal, id string) (View, uint64, bool, error) {
	if err := p.Require("ledger:read"); err != nil {
		return View{}, 0, false, err
	}
	epoch, stable := s.Snapshots.memoEpoch(p.UserID)
	if !stable {
		v, err := s.Views.View(ctx, p, id)
		return v, epoch, false, err
	}
	v, err := reqcache.Do(ctx, fmt.Sprintf("view-definition|%s|%d|%s", p.UserID, epoch, id), func() (View, error) { return s.Views.View(ctx, p, id) })
	return v, epoch, true, err
}

func memoizableLeaf(v View) bool {
	if v.Query.From != "postings" && v.Query.From != "journal" && v.Query.From != "effective_entries" {
		return false
	}
	volatile := false
	walkExpressions(Spec{Data: map[string]Query{v.ID: v.Query}}, func(_ string, expr string) {
		volatile = volatile || expressions.MayUseNow(expr)
	})
	return !volatile
}

// Only a statically acyclic chain ending in a safe leaf can share an in-flight result.
func memoizableChain(v View, stack []string, load func(string) (View, bool)) bool {
	path := append([]string(nil), stack...)
	for {
		if len(path) >= maxViewDepth {
			return false
		}
		for _, id := range path {
			if id == v.ID {
				return false
			}
		}
		path = append(path, v.ID)
		next, nested := strings.CutPrefix(v.Query.From, "view:")
		if !nested {
			return memoizableLeaf(v)
		}
		volatile := false
		walkExpressions(Spec{Data: map[string]Query{v.ID: v.Query}}, func(_ string, expr string) {
			volatile = volatile || expressions.MayUseNow(expr)
		})
		if volatile {
			return false
		}
		var ok bool
		v, ok = load(next)
		if !ok {
			return false
		}
	}
}
