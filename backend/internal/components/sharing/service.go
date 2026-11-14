package sharing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"slices"
	"strings"
	"time"
)

type Grant struct {
	ID          string     `json:"id" db:"id"`
	DashboardID string     `json:"dashboard_id" db:"dashboard_id"`
	OwnerUser   string     `json:"owner_user" db:"owner_user"`
	GranteeUser string     `json:"grantee_user" db:"grantee_user"`
	Role        string     `json:"role" db:"role"`
	Mode        string     `json:"mode" db:"mode"`
	Sources     []string   `json:"sources" db:"sources"`
	RowFilter   string     `json:"row_filter,omitempty" db:"row_filter"`
	Refresh     string     `json:"refresh" db:"refresh"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty" db:"revoked_at"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	LastRefresh time.Time  `json:"last_refresh" db:"last_refresh"`
}
type Snapshot struct {
	GrantHash   string             `json:"-" db:"grant_hash"`
	ID          string             `json:"-" db:"id"`
	OwnerUser   string             `json:"-" db:"owner_user"`
	DashboardID string             `json:"dashboard_id" db:"dashboard_id"`
	Version     int                `json:"version" db:"version"`
	Title       string             `json:"title" db:"title"`
	Rendered    dashboard.Rendered `json:"rendered" db:"rendered"`
	At          time.Time          `json:"at" db:"at"`
}
type ShareInput struct {
	DashboardID string   `json:"dashboard_id"`
	Grantee     string   `json:"grantee"`
	Role        string   `json:"role"`
	Mode        string   `json:"mode,omitempty"`
	Sources     []string `json:"sources,omitempty"`
	RowFilter   string   `json:"row_filter,omitempty"`
	Refresh     string   `json:"refresh,omitempty"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
}
type ShareResult struct {
	ShareID string `json:"share_id"`
	URL     string `json:"url"`
}
type SharedView struct {
	Grant    Grant    `json:"grant"`
	Snapshot Snapshot `json:"snapshot"`
	// LegacyHTML is the stored output of a snapshot frozen before page layouts existed.
	LegacyHTML string `json:"legacy_html,omitempty"`
}
type Access struct {
	Dashboard *dashboard.Service
	Principal principal.Principal
	ID        string
	Grant     *Grant
}
type PostgresRepository struct{ db *pgxpool.Pool }
type Service struct {
	repo      *PostgresRepository
	identity  *identity.Service
	dashboard *dashboard.Service
	reader    dashboard.Reader
	expr      *expressions.Engine
	audit     *audit.Service
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository { return &PostgresRepository{db} }
func NewService(r *PostgresRepository, i *identity.Service, d *dashboard.Service, reader dashboard.Reader, e *expressions.Engine, a *audit.Service) *Service {
	return &Service{r, i, d, reader, e, a}
}

type ScopedReader struct {
	Reader    dashboard.Reader
	OwnerID   string
	Sources   []string
	RowFilter string
	Expr      *expressions.Engine
}

// AllowsMetric lets a share read a metric only when its grant names it and no row filter limits it: a metric's total cannot honor a row filter.
func (s ScopedReader) AllowsMetric(id string) bool {
	return s.RowFilter == "" && slices.Contains(s.Sources, "metric:"+id)
}

func (s ScopedReader) Rows(ctx context.Context, p principal.Principal, source string) ([]map[string]any, error) {
	if p.UserID != s.OwnerID || !slices.Contains(s.Sources, source) {
		return nil, fmt.Errorf("source not granted: %s", source)
	}
	rows, e := s.Reader.Rows(ctx, p, source)
	if e != nil {
		return nil, e
	}
	if len(rows) > 5000 {
		return nil, fmt.Errorf("shared source exceeds 5000 rows")
	}
	if s.RowFilter == "" {
		return rows, nil
	}
	out := []map[string]any{}
	for _, row := range rows {
		v, e := s.Expr.Eval(ctx, s.RowFilter, map[string]any{"row": row})
		if e != nil {
			return nil, fmt.Errorf("share row filter: %w", e)
		}
		ok, isBool := v.Value.(bool)
		if !isBool {
			return nil, fmt.Errorf("share row filter must return boolean")
		}
		if ok {
			out = append(out, row)
		}
	}
	return out, nil
}
func ownerPrincipal(g Grant, actor principal.Actor) principal.Principal {
	return principal.Principal{UserID: g.OwnerUser, Scopes: []string{"ledger:read", "dash:write"}, Actor: actor}
}
func (s *Service) scoped(g Grant) *dashboard.Service {
	return s.dashboard.WithReader(ScopedReader{Reader: s.reader, OwnerID: g.OwnerUser, Sources: g.Sources, RowFilter: g.RowFilter, Expr: s.expr})
}

const saveSnapshotSQL = `INSERT INTO dashboard_snapshots (id,grant_hash,owner_user,dashboard_id,version,title,rendered,at)
 VALUES (@id,@hash,@owner,@dashboard,@version,@title,@rendered,@at)
 ON CONFLICT (id) DO UPDATE SET grant_hash=EXCLUDED.grant_hash,dashboard_id=EXCLUDED.dashboard_id,version=EXCLUDED.version,
 title=EXCLUDED.title,rendered=EXCLUDED.rendered,at=EXCLUDED.at WHERE dashboard_snapshots.owner_user=EXCLUDED.owner_user`

func (s *Service) Share(ctx context.Context, p principal.Principal, in ShareInput) (ShareResult, error) {
	if e := p.Require("dash:share"); e != nil {
		return ShareResult{}, e
	}
	if in.Role != "viewer" && in.Role != "editor" {
		return ShareResult{}, fmt.Errorf("role must be viewer or editor")
	}
	if in.Mode == "" {
		in.Mode = "snapshot"
	}
	if in.Mode != "snapshot" && in.Mode != "live" {
		return ShareResult{}, fmt.Errorf("mode must be snapshot or live")
	}
	if in.Refresh == "" {
		in.Refresh = "on_publish"
	}
	if in.Refresh != "on_publish" && in.Refresh != "hourly" && in.Refresh != "daily" {
		return ShareResult{}, fmt.Errorf("refresh must be on_publish, hourly or daily")
	}
	if len(in.Sources) > 50 {
		return ShareResult{}, fmt.Errorf("at most 50 sources")
	}
	var expires *time.Time
	if in.ExpiresAt != "" {
		v, e := time.Parse(time.RFC3339, in.ExpiresAt)
		if e != nil || !v.After(time.Now()) {
			return ShareResult{}, fmt.Errorf("expires_at must be a future RFC3339 date")
		}
		v = v.UTC().Truncate(time.Millisecond)
		expires = &v
	}
	if in.RowFilter != "" {
		if _, e := s.expr.Compile(in.RowFilter); e != nil {
			return ShareResult{}, e
		}
	}
	var grantee identity.User
	var e error
	if strings.Contains(in.Grantee, "@") {
		grantee, e = s.identity.ByEmail(ctx, in.Grantee)
	} else {
		grantee, e = s.identity.User(ctx, in.Grantee)
	}
	if e != nil {
		return ShareResult{}, fmt.Errorf("grantee: %w", e)
	}
	if grantee.ID == p.UserID {
		return ShareResult{}, fmt.Errorf("dashboard is already yours")
	}
	v, e := s.dashboard.Get(ctx, p, in.DashboardID, "published")
	if e != nil {
		return ShareResult{}, e
	}
	sources := slices.Clone(in.Sources)
	if len(sources) == 0 {
		for _, q := range v.Spec.Data {
			if q.From != "range" && !strings.HasPrefix(q.From, "query:") && !slices.Contains(sources, q.From) {
				sources = append(sources, q.From)
			}
		}
		for _, id := range v.Spec.MetricIDs() {
			if !slices.Contains(sources, "metric:"+id) {
				sources = append(sources, "metric:"+id)
			}
		}
	}
	g := Grant{ID: ulid.Make().String(), DashboardID: in.DashboardID, OwnerUser: p.UserID, GranteeUser: grantee.ID, Role: in.Role, Mode: in.Mode, Sources: sources, RowFilter: in.RowFilter, Refresh: in.Refresh, ExpiresAt: expires, CreatedAt: time.Now().UTC().Truncate(time.Millisecond).Truncate(time.Millisecond)}
	var existing Grant
	const existingSQL = `SELECT * FROM shares WHERE owner_user=@owner AND dashboard_id=@dashboard AND grantee_user=@grantee LIMIT 1`
	rows, err := s.repo.db.Query(ctx, existingSQL, pgx.NamedArgs{"owner": p.UserID, "dashboard": in.DashboardID, "grantee": grantee.ID})
	if err != nil {
		return ShareResult{}, err
	}
	existing, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[Grant])
	if e == nil {
		g.ID = existing.ID
		g.CreatedAt = existing.CreatedAt
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return ShareResult{}, e
	}
	rendered, e := s.scoped(g).Preview(ctx, ownerPrincipal(g, p.Actor), g.DashboardID, "published", nil)
	if e != nil {
		return ShareResult{}, e
	}
	if !rendered.Validation.OK {
		return ShareResult{}, fmt.Errorf("shared dashboard does not validate within the grant: %v", rendered.Validation.Errors)
	}
	snapshot := Snapshot{GrantHash: PermissionHash(g), ID: g.ID, OwnerUser: g.OwnerUser, DashboardID: g.DashboardID, Version: v.Version, Title: v.Spec.Title, Rendered: rendered, At: time.Now().UTC().Truncate(time.Millisecond)}
	g.LastRefresh = snapshot.At
	_, e = s.audit.Write(ctx, p, "dashboard_share", audit.Target{Collection: "shares", ID: g.ID}, "", existing, g, func(tx context.Context) error {
		if _, e := s.repo.db.Exec(tx, `INSERT INTO shares (id,dashboard_id,owner_user,grantee_user,role,mode,sources,row_filter,refresh,expires_at,revoked_at,created_at,last_refresh)
 VALUES (@id,@dashboard,@owner,@grantee,@role,@mode,@sources,@filter,@refresh,@expires,@revoked,@created,@last)
 ON CONFLICT (id) DO UPDATE SET dashboard_id=EXCLUDED.dashboard_id,grantee_user=EXCLUDED.grantee_user,role=EXCLUDED.role,mode=EXCLUDED.mode,
 sources=EXCLUDED.sources,row_filter=EXCLUDED.row_filter,refresh=EXCLUDED.refresh,expires_at=EXCLUDED.expires_at,revoked_at=EXCLUDED.revoked_at,
 created_at=EXCLUDED.created_at,last_refresh=EXCLUDED.last_refresh WHERE shares.owner_user=EXCLUDED.owner_user`, pgx.NamedArgs{"id": g.ID, "dashboard": g.DashboardID, "owner": g.OwnerUser, "grantee": g.GranteeUser, "role": g.Role, "mode": g.Mode, "sources": g.Sources, "filter": g.RowFilter, "refresh": g.Refresh, "expires": g.ExpiresAt, "revoked": g.RevokedAt, "created": g.CreatedAt, "last": g.LastRefresh}); e != nil {
			return e
		}
		_, e := s.repo.db.Exec(tx, saveSnapshotSQL, pgx.NamedArgs{"id": snapshot.ID, "hash": snapshot.GrantHash, "owner": snapshot.OwnerUser, "dashboard": snapshot.DashboardID, "version": snapshot.Version, "title": snapshot.Title, "rendered": snapshot.Rendered, "at": snapshot.At})
		return e
	})
	return ShareResult{g.ID, "/shared/" + g.ID}, e
}
func (s *Service) Resolve(ctx context.Context, p principal.Principal, id string) (Grant, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Grant{}, e
	}
	const resolveSQL = `SELECT * FROM shares WHERE id=@id AND grantee_user=@user AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>@now)`
	rows, e := s.repo.db.Query(ctx, resolveSQL, pgx.NamedArgs{"id": id, "user": p.UserID, "now": time.Now().UTC().Truncate(time.Millisecond)})
	if e != nil {
		return Grant{}, e
	}
	g, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[Grant])
	if errors.Is(e, pgx.ErrNoRows) {
		e = principal.ErrNotFound
	}
	if e == nil {
		published, err := s.dashboard.Published(ctx, ownerPrincipal(g, p.Actor), g.DashboardID)
		if err != nil {
			return Grant{}, err
		}
		if !published {
			return Grant{}, principal.ErrNotFound
		}
	}
	return g, e
}
func (s *Service) Access(ctx context.Context, p principal.Principal, id string, write bool) (Access, error) {
	if _, e := s.dashboard.Get(ctx, p, id, ""); e == nil {
		return Access{s.dashboard, p, id, nil}, nil
	} else if !errors.Is(e, principal.ErrNotFound) {
		return Access{}, e
	}
	g, e := s.Resolve(ctx, p, id)
	if e != nil {
		return Access{}, e
	}
	if write {
		if e = p.Require("dash:write"); e != nil {
			return Access{}, e
		}
		if g.Role != "editor" {
			return Access{}, principal.ErrForbidden
		}
	}
	if !write && g.Role == "viewer" {
		return Access{}, fmt.Errorf("%w: viewer grants expose rendered output only; open /shared/%s", principal.ErrForbidden, g.ID)
	}
	return Access{s.scoped(g), ownerPrincipal(g, p.Actor), g.DashboardID, &g}, nil
}

// EditorAccess resolves a grant directly, even when its ID matches an owned dashboard.
func (s *Service) EditorAccess(ctx context.Context, p principal.Principal, id string) (Access, error) {
	if err := p.Require("dash:write"); err != nil {
		return Access{}, err
	}
	g, err := s.Resolve(ctx, p, id)
	if err != nil {
		return Access{}, err
	}
	if g.Role != "editor" {
		return Access{}, principal.ErrForbidden
	}
	return Access{s.scoped(g), ownerPrincipal(g, p.Actor), g.DashboardID, &g}, nil
}

func (s *Service) RecordEditorRead(ctx context.Context, p principal.Principal, original Access) error {
	if original.Grant == nil {
		return nil
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		current, err := s.Resolve(tx, p, original.Grant.ID)
		if err != nil {
			return err
		}
		if current.Role != "editor" || PermissionHash(current) != PermissionHash(*original.Grant) {
			return principal.ErrForbidden
		}
		_, err = s.audit.Write(tx, ownerPrincipal(current, p.Actor), "dashboard_shared_read", audit.Target{Collection: "shares", ID: current.ID}, "", nil, struct {
			Mode string `json:"mode"`
		}{Mode: "editor"}, nil)
		return err
	})
}

func (s *Service) EditorWrite(ctx context.Context, p principal.Principal, original Access, fn func(context.Context, Access) error) error {
	if original.Grant == nil {
		return principal.ErrForbidden
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		current, err := s.EditorAccess(tx, p, original.Grant.ID)
		if err != nil {
			return err
		}
		if PermissionHash(*current.Grant) != PermissionHash(*original.Grant) {
			return principal.ErrForbidden
		}
		return fn(audit.WithGrant(tx, current.Grant.ID, PermissionHash(*current.Grant)), current)
	})
}
func (s *Service) View(ctx context.Context, p principal.Principal, id string) (SharedView, error) {
	g, e := s.Resolve(ctx, p, id)
	if e != nil {
		return SharedView{}, e
	}
	var snap Snapshot
	legacy := ""
	if g.Mode == "snapshot" {
		rows, err := s.repo.db.Query(ctx, `SELECT * FROM dashboard_snapshots WHERE id=@id AND owner_user=@owner`, pgx.NamedArgs{"id": g.ID, "owner": g.OwnerUser})
		if err != nil {
			return SharedView{}, err
		}
		snap, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[Snapshot])
		if errors.Is(e, pgx.ErrNoRows) {
			return SharedView{}, fmt.Errorf("shared snapshot is pending refresh")
		}
		if e != nil {
			return SharedView{}, e
		}
		if snap.GrantHash != PermissionHash(g) {
			return SharedView{}, fmt.Errorf("shared snapshot needs refresh")
		}
		if len(snap.Rendered.Pages) == 0 {
			const legacySQL = `SELECT COALESCE(rendered->>'html','') FROM dashboard_snapshots WHERE id=@id AND owner_user=@owner`
			if e = s.repo.db.QueryRow(ctx, legacySQL, pgx.NamedArgs{"id": g.ID, "owner": g.OwnerUser}).Scan(&legacy); e != nil {
				return SharedView{}, e
			}
		}
	} else {
		owner := ownerPrincipal(g, p.Actor)
		v, e := s.scoped(g).Get(ctx, owner, g.DashboardID, "published")
		if e != nil {
			return SharedView{}, e
		}
		rendered, e := s.scoped(g).Preview(ctx, owner, g.DashboardID, "published", nil)
		if e != nil {
			return SharedView{}, e
		}
		if !rendered.Validation.OK {
			if len(rendered.Pages) == 0 || len(rendered.Validation.Errors) == 0 {
				return SharedView{}, fmt.Errorf("shared dashboard failed validation")
			}
			for i, issue := range rendered.Validation.Errors {
				if issue.ComponentID == "" {
					return SharedView{}, fmt.Errorf("shared dashboard failed validation")
				}
				rendered.Validation.Errors[i] = dashboard.Issue{ComponentID: issue.ComponentID, Msg: "This component could not be displayed."}
			}
			rendered.Validation.Warnings = nil
			rendered.Computed = nil
		}
		snap = Snapshot{GrantHash: PermissionHash(g), ID: g.ID, OwnerUser: g.OwnerUser, DashboardID: g.DashboardID, Version: v.Version, Title: v.Spec.Title, Rendered: rendered, At: time.Now().UTC().Truncate(time.Millisecond)}
	}
	e = s.audit.Workflow(ctx, func(tx context.Context) error {
		current, err := s.Resolve(tx, p, id)
		if err != nil {
			return err
		}
		if PermissionHash(current) != PermissionHash(g) {
			return principal.ErrForbidden
		}
		_, err = s.audit.Write(tx, ownerPrincipal(g, p.Actor), "dashboard_shared_read", audit.Target{Collection: "shares", ID: g.ID}, "", nil, struct {
			Version int `json:"version"`
		}{snap.Version}, nil)
		return err
	})
	return SharedView{g, snap, legacy}, e
}

func (s *Service) ListOwned(ctx context.Context, p principal.Principal, id string) ([]Grant, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	return s.list(ctx, p.UserID, id, false)
}
func (s *Service) ListReceived(ctx context.Context, p principal.Principal) ([]Grant, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	return s.list(ctx, p.UserID, "", true)
}
func (s *Service) list(ctx context.Context, user, id string, received bool) ([]Grant, error) {
	const listSQL = `SELECT * FROM shares WHERE
 (@received AND grantee_user=@user AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>@now))
 OR (NOT @received AND owner_user=@user AND (@id='' OR dashboard_id=@id)) ORDER BY created_at DESC LIMIT 501`
	rows, e := s.repo.db.Query(ctx, listSQL, pgx.NamedArgs{"received": received, "user": user, "id": id, "now": time.Now().UTC().Truncate(time.Millisecond)})
	if e != nil {
		return nil, e
	}
	out, e := pgx.CollectRows(rows, pgx.RowToStructByName[Grant])
	if len(out) > 500 {
		return nil, fmt.Errorf("too many shares; select a dashboard")
	}
	return out, e
}

func (s *Service) Unshare(ctx context.Context, p principal.Principal, id string) error {
	if e := p.Require("dash:share"); e != nil {
		return e
	}
	rows, e := s.repo.db.Query(ctx, `SELECT * FROM shares WHERE id=@id AND owner_user=@owner`, pgx.NamedArgs{"id": id, "owner": p.UserID})
	if e != nil {
		return e
	}
	g, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[Grant])
	if errors.Is(e, pgx.ErrNoRows) {
		return principal.ErrNotFound
	}
	if e != nil {
		return e
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, e = s.audit.Write(ctx, p, "dashboard_unshare", audit.Target{Collection: "shares", ID: id}, "", g, struct {
		Revoked bool `json:"revoked"`
	}{true}, func(tx context.Context) error {
		if _, e := s.repo.db.Exec(tx, `UPDATE shares SET revoked_at=@now WHERE id=@id AND owner_user=@owner`, pgx.NamedArgs{"now": now, "id": id, "owner": p.UserID}); e != nil {
			return e
		}
		_, e := s.repo.db.Exec(tx, `DELETE FROM dashboard_snapshots WHERE id=@id AND owner_user=@owner`, pgx.NamedArgs{"id": id, "owner": p.UserID})
		return e
	})
	return e
}
func (s *Service) RefreshDashboard(ctx context.Context, p principal.Principal, id string) error {
	published, e := s.dashboard.Published(ctx, p, id)
	if e != nil {
		return e
	}
	if !published {
		_, e = s.repo.db.Exec(ctx, `DELETE FROM dashboard_snapshots WHERE owner_user=@owner AND dashboard_id=@dashboard`, pgx.NamedArgs{"owner": p.UserID, "dashboard": id})
		return e
	}
	grants, e := s.ListOwned(ctx, p, id)
	if e != nil {
		return e
	}
	for _, g := range grants {
		if g.RevokedAt != nil || g.ExpiresAt != nil && !g.ExpiresAt.After(time.Now()) {
			continue
		}
		if e = s.refresh(ctx, g, p.Actor); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) refresh(ctx context.Context, g Grant, actor principal.Actor) error {
	owner := ownerPrincipal(g, actor)
	v, e := s.scoped(g).Get(ctx, owner, g.DashboardID, "published")
	if e != nil {
		return e
	}
	rendered, e := s.scoped(g).Preview(ctx, owner, g.DashboardID, "published", nil)
	if e != nil {
		return e
	}
	if !rendered.Validation.OK {
		return fmt.Errorf("snapshot %s failed validation", g.ID)
	}
	snap := Snapshot{GrantHash: PermissionHash(g), ID: g.ID, OwnerUser: g.OwnerUser, DashboardID: g.DashboardID, Version: v.Version, Title: v.Spec.Title, Rendered: rendered, At: time.Now().UTC().Truncate(time.Millisecond)}
	if _, e = s.repo.db.Exec(ctx, saveSnapshotSQL, pgx.NamedArgs{"id": snap.ID, "hash": snap.GrantHash, "owner": snap.OwnerUser, "dashboard": snap.DashboardID, "version": snap.Version, "title": snap.Title, "rendered": snap.Rendered, "at": snap.At}); e != nil {
		return e
	}
	_, e = s.repo.db.Exec(ctx, `UPDATE shares SET last_refresh=@at WHERE id=@id AND owner_user=@owner AND revoked_at IS NULL`, pgx.NamedArgs{"at": snap.At, "id": g.ID, "owner": g.OwnerUser})
	return e
}
func (s *Service) RefreshDue(ctx context.Context) error {
	const dueSQL = `SELECT * FROM shares WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at>@now)`
	rows, err := s.repo.db.Query(ctx, dueSQL, pgx.NamedArgs{"now": time.Now().UTC().Truncate(time.Millisecond)})
	if err != nil {
		return err
	}
	defer rows.Close()
	var failures []error
	for rows.Next() {
		g, e := pgx.RowToStructByName[Grant](rows)
		if e != nil {
			err = e
			return err
		}
		if err = s.refreshDueGrant(ctx, g); err != nil {
			failures = append(failures, fmt.Errorf("share %s: %w", g.ID, err))
		}
	}
	return errors.Join(append(failures, rows.Err())...)
}
func (s *Service) refreshDueGrant(ctx context.Context, g Grant) error {
	actor := principal.Actor{Kind: "system", ID: "snapshot-refresh"}
	owner := ownerPrincipal(g, actor)
	published, err := s.dashboard.Published(ctx, owner, g.DashboardID)
	if errors.Is(err, principal.ErrNotFound) || err == nil && !published {
		return nil
	}
	if err != nil {
		return err
	}
	due := g.Refresh == "hourly" && time.Since(g.LastRefresh) >= time.Hour || g.Refresh == "daily" && time.Since(g.LastRefresh) >= 24*time.Hour
	if g.Refresh == "on_publish" {
		v, err := s.dashboard.Get(ctx, owner, g.DashboardID, "published")
		if err != nil {
			return err
		}
		rows, e := s.repo.db.Query(ctx, `SELECT * FROM dashboard_snapshots WHERE id=@id AND owner_user=@owner`, pgx.NamedArgs{"id": g.ID, "owner": g.OwnerUser})
		if e != nil {
			return e
		}
		snap, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[Snapshot])
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		due = err != nil || snap.Version != v.Version || snap.GrantHash != PermissionHash(g)
	}
	if due {
		return s.refresh(ctx, g, actor)
	}
	return nil
}

func PermissionHash(g Grant) string {
	sources := slices.Clone(g.Sources)
	slices.Sort(sources)
	b, _ := json.Marshal(struct {
		Owner, Dashboard, Grantee, Role, Mode, Filter string
		Sources                                       []string
		Expires                                       *time.Time
	}{g.OwnerUser, g.DashboardID, g.GranteeUser, g.Role, g.Mode, g.RowFilter, sources, g.ExpiresAt})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
