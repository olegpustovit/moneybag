// Package feedback is what people send back: flags on numbers, page usage and their own saved views.
package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"slices"
	"strings"
	"time"
)

type Target struct {
	Dashboard string         `json:"dashboard,omitempty" db:"dashboard"`
	Component string         `json:"component,omitempty" db:"component"`
	Metric    string         `json:"metric,omitempty" db:"metric"`
	Value     string         `json:"value,omitempty" db:"value"`
	Params    map[string]any `json:"params,omitempty" db:"params"`
}
type Flag struct {
	ID         string          `json:"id" db:"id"`
	UserID     string          `json:"-" db:"user_id"`
	Target     Target          `json:"target" db:"target"`
	Note       string          `json:"note" db:"note"`
	Status     string          `json:"status" db:"status"`
	Resolution string          `json:"resolution,omitempty" db:"resolution"`
	Answer     string          `json:"answer,omitempty" db:"answer"`
	CreatedBy  principal.Actor `json:"created_by" db:"created_by"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
	ResolvedBy principal.Actor `json:"resolved_by,omitzero" db:"resolved_by"`
	ResolvedAt *time.Time      `json:"resolved_at,omitempty" db:"resolved_at"`
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
	Path      string `json:"path" db:"path"`
	Dashboard string `json:"dashboard,omitempty" db:"dashboard"`
}
type UsageRow struct {
	Path      string `json:"path" db:"path"`
	Dashboard string `json:"dashboard,omitempty" db:"dashboard"`
	Views     int64  `json:"views" db:"views"`
	LastAt    string `json:"last_at" db:"last_at"`
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
	Key    string          `json:"-" db:"storage_key"`
	UserID string          `json:"-" db:"user_id"`
	ID     string          `json:"id" db:"id"`
	Name   string          `json:"name" db:"name"`
	Page   string          `json:"page" db:"page"`
	State  json.RawMessage `json:"state,omitempty" db:"state"`
}
type PostgresRepository struct{ db *pgxpool.Pool }
type Service struct {
	repo  *PostgresRepository
	audit *audit.Service
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository  { return &PostgresRepository{db} }
func NewService(r *PostgresRepository, a *audit.Service) *Service { return &Service{r, a} }

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
		const insertSQL = `INSERT INTO feedback (id,user_id,target,note,status,resolution,answer,created_by,created_at,resolved_by,resolved_at)
 VALUES (@id,@user,@target,@note,@status,@resolution,@answer,@created_by,@created_at,@resolved_by,@resolved_at)`
		_, e := s.repo.db.Exec(tx, insertSQL, pgx.NamedArgs{"id": flag.ID, "user": flag.UserID, "target": flag.Target, "note": flag.Note, "status": flag.Status, "resolution": flag.Resolution, "answer": flag.Answer, "created_by": flag.CreatedBy, "created_at": flag.CreatedAt, "resolved_by": flag.ResolvedBy, "resolved_at": flag.ResolvedAt})
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
	status := ""
	switch in.Status {
	case "", "open":
		status = "open"
	case "resolved":
		status = "resolved"
	case "all":
	default:
		return nil, fmt.Errorf("status must be open, resolved or all")
	}
	const listSQL = `SELECT * FROM feedback WHERE user_id=@user AND (@status='' OR status=@status) ORDER BY id DESC LIMIT @limit`
	rows, e := s.repo.db.Query(ctx, listSQL, pgx.NamedArgs{"user": p.UserID, "status": status, "limit": in.Limit})
	if e != nil {
		return nil, e
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[Flag])
}

func (s *Service) OpenCount(ctx context.Context, p principal.Principal) (int64, error) {
	if e := p.Require("ledger:read"); e != nil {
		return 0, e
	}
	const countSQL = `SELECT count(*) FROM feedback WHERE user_id=@user AND status='open'`
	var count int64
	err := s.repo.db.QueryRow(ctx, countSQL, pgx.NamedArgs{"user": p.UserID}).Scan(&count)
	return count, err
}

func (s *Service) load(ctx context.Context, p principal.Principal, id string) (Flag, error) {
	const loadSQL = `SELECT * FROM feedback WHERE id=@id AND user_id=@user`
	rows, e := s.repo.db.Query(ctx, loadSQL, pgx.NamedArgs{"id": id, "user": p.UserID})
	if e != nil {
		return Flag{}, e
	}
	flag, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[Flag])
	if errors.Is(e, pgx.ErrNoRows) {
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
			const updateSQL = `UPDATE feedback SET status=@status,resolution=@resolution,answer=@answer,resolved_by=@actor,resolved_at=@at WHERE id=@id AND user_id=@user`
			_, e := s.repo.db.Exec(tx, updateSQL, pgx.NamedArgs{"id": out.ID, "user": p.UserID, "status": out.Status, "resolution": out.Resolution, "answer": out.Answer, "actor": out.ResolvedBy, "at": out.ResolvedAt})
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
			const updateSQL = `UPDATE feedback SET status=@status,resolution=@resolution,answer=@answer,resolved_by=@actor,resolved_at=@at WHERE id=@id AND user_id=@user`
			_, e := s.repo.db.Exec(tx, updateSQL, pgx.NamedArgs{"id": next.ID, "user": p.UserID, "status": next.Status, "resolution": next.Resolution, "answer": next.Answer, "actor": next.ResolvedBy, "at": next.ResolvedAt})
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
	const recordSQL = `INSERT INTO usage (id,user_id,date,path,dashboard,views,last_at) VALUES (@id,@user,@date,@path,@dashboard,1,@at)
 ON CONFLICT (id) DO UPDATE SET views=usage.views+1,last_at=GREATEST(usage.last_at,EXCLUDED.last_at)`
	_, e := s.repo.db.Exec(ctx, recordSQL, pgx.NamedArgs{"id": key, "user": p.UserID, "date": date, "path": path, "dashboard": in.Dashboard, "at": now.Format(time.RFC3339)})
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
	const usageSQL = `SELECT path,dashboard,SUM(views)::bigint AS views,MAX(last_at) AS last_at FROM usage
 WHERE user_id=@user AND date>=@from AND date<=@to GROUP BY path,dashboard ORDER BY views DESC,path LIMIT 200`
	rows, e := s.repo.db.Query(ctx, usageSQL, pgx.NamedArgs{"user": p.UserID, "from": in.From, "to": in.To})
	if e != nil {
		return UsageReport{}, e
	}
	pages, e := pgx.CollectRows(rows, pgx.RowToStructByName[UsageRow])
	if e != nil {
		return UsageReport{}, e
	}
	out := UsageReport{From: in.From, To: in.To, Pages: pages}
	return out, nil
}

func (s *Service) SavedViews(ctx context.Context, p principal.Principal) ([]SavedView, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	const viewsSQL = `SELECT * FROM saved_views WHERE user_id=@user ORDER BY name LIMIT 200`
	rows, e := s.repo.db.Query(ctx, viewsSQL, pgx.NamedArgs{"user": p.UserID})
	if e != nil {
		return nil, e
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[SavedView])
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
		const countSQL = `SELECT count(*) FROM saved_views WHERE user_id=@user`
		var n int64
		e := s.repo.db.QueryRow(ctx, countSQL, pgx.NamedArgs{"user": p.UserID}).Scan(&n)
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
	const saveSQL = `INSERT INTO saved_views (storage_key,user_id,id,name,page,state) VALUES (@key,@user,@id,@name,@page,@state)
 ON CONFLICT (storage_key) DO UPDATE SET id=EXCLUDED.id,name=EXCLUDED.name,page=EXCLUDED.page,state=EXCLUDED.state
 WHERE saved_views.user_id=EXCLUDED.user_id`
	result, e := s.repo.db.Exec(ctx, saveSQL, pgx.NamedArgs{"key": v.Key, "user": v.UserID, "id": v.ID, "name": v.Name, "page": v.Page, "state": v.State})
	if e == nil && result.RowsAffected() != 1 {
		return SavedView{}, principal.ErrConflict
	}
	return v, e
}

func (s *Service) DeleteView(ctx context.Context, p principal.Principal, id string) error {
	if e := p.Require("ledger:read"); e != nil {
		return e
	}
	const deleteSQL = `DELETE FROM saved_views WHERE storage_key=@key AND user_id=@user`
	result, e := s.repo.db.Exec(ctx, deleteSQL, pgx.NamedArgs{"key": p.UserID + ":" + id, "user": p.UserID})
	if e == nil && result.RowsAffected() != 1 {
		return principal.ErrNotFound
	}
	return e
}
