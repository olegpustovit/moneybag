package sharing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"slices"
	"strings"
	"time"
)

type Grant struct {
	ID          string     `json:"id" bson:"_id"`
	DashboardID string     `json:"dashboard_id" bson:"dashboard_id"`
	OwnerUser   string     `json:"owner_user" bson:"owner_user"`
	GranteeUser string     `json:"grantee_user" bson:"grantee_user"`
	Role        string     `json:"role" bson:"role"`
	Mode        string     `json:"mode" bson:"mode"`
	Sources     []string   `json:"sources" bson:"sources"`
	RowFilter   string     `json:"row_filter,omitempty" bson:"row_filter,omitempty"`
	Refresh     string     `json:"refresh" bson:"refresh"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" bson:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty" bson:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at" bson:"created_at"`
	LastRefresh time.Time  `json:"last_refresh" bson:"last_refresh"`
}
type Snapshot struct {
	GrantHash   string             `json:"-" bson:"grant_hash"`
	ID          string             `json:"-" bson:"_id"`
	OwnerUser   string             `json:"-" bson:"owner_user"`
	DashboardID string             `json:"dashboard_id" bson:"dashboard_id"`
	Version     int                `json:"version" bson:"version"`
	Title       string             `json:"title" bson:"title"`
	Rendered    dashboard.Rendered `json:"rendered" bson:"rendered"`
	At          time.Time          `json:"at" bson:"at"`
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
}
type Access struct {
	Dashboard *dashboard.Service
	Principal principal.Principal
	ID        string
	Grant     *Grant
}
type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo      *MongoRepository
	identity  *identity.Service
	dashboard *dashboard.Service
	reader    dashboard.Reader
	expr      *expressions.Engine
	audit     *audit.Service
}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func NewService(r *MongoRepository, i *identity.Service, d *dashboard.Service, reader dashboard.Reader, e *expressions.Engine, a *audit.Service) *Service {
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
func activeFilter() bson.M {
	return bson.M{"revoked_at": bson.M{"$exists": false}, "$or": bson.A{bson.M{"expires_at": bson.M{"$exists": false}}, bson.M{"expires_at": bson.M{"$gt": time.Now()}}}}
}
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
	g := Grant{ID: ulid.Make().String(), DashboardID: in.DashboardID, OwnerUser: p.UserID, GranteeUser: grantee.ID, Role: in.Role, Mode: in.Mode, Sources: sources, RowFilter: in.RowFilter, Refresh: in.Refresh, ExpiresAt: expires, CreatedAt: time.Now().UTC()}
	var existing Grant
	e = s.repo.db.Collection("shares").FindOne(ctx, bson.M{"owner_user": p.UserID, "dashboard_id": in.DashboardID, "grantee_user": grantee.ID}).Decode(&existing)
	if e == nil {
		g.ID = existing.ID
		g.CreatedAt = existing.CreatedAt
	} else if !errors.Is(e, mongo.ErrNoDocuments) {
		return ShareResult{}, e
	}
	rendered, e := s.scoped(g).Preview(ctx, ownerPrincipal(g, p.Actor), g.DashboardID, "published", nil)
	if e != nil {
		return ShareResult{}, e
	}
	if !rendered.Validation.OK {
		return ShareResult{}, fmt.Errorf("shared dashboard does not validate within the grant: %v", rendered.Validation.Errors)
	}
	snapshot := Snapshot{GrantHash: PermissionHash(g), ID: g.ID, OwnerUser: g.OwnerUser, DashboardID: g.DashboardID, Version: v.Version, Title: v.Spec.Title, Rendered: rendered, At: time.Now().UTC()}
	g.LastRefresh = snapshot.At
	_, e = s.audit.Write(ctx, p, "dashboard_share", audit.Target{Collection: "shares", ID: g.ID}, "", existing, g, func(tx context.Context) error {
		if _, e := s.repo.db.Collection("shares").ReplaceOne(tx, bson.M{"_id": g.ID, "owner_user": p.UserID}, g, options.Replace().SetUpsert(true)); e != nil {
			return e
		}
		_, e := s.repo.db.Collection("dashboard_snapshots").ReplaceOne(tx, bson.M{"_id": g.ID, "owner_user": p.UserID}, snapshot, options.Replace().SetUpsert(true))
		return e
	})
	return ShareResult{g.ID, "/shared/" + g.ID}, e
}
func (s *Service) Resolve(ctx context.Context, p principal.Principal, id string) (Grant, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Grant{}, e
	}
	f := activeFilter()
	f["_id"] = id
	f["grantee_user"] = p.UserID
	var g Grant
	e := s.repo.db.Collection("shares").FindOne(ctx, f).Decode(&g)
	if errors.Is(e, mongo.ErrNoDocuments) {
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
		return Access{}, fmt.Errorf("viewer grants expose rendered output only; open /shared/%s", g.ID)
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
		}{Mode: "editor"}, func(context.Context) error { return nil })
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
	if g.Mode == "snapshot" {
		e = s.repo.db.Collection("dashboard_snapshots").FindOne(ctx, bson.M{"_id": g.ID, "owner_user": g.OwnerUser}).Decode(&snap)
		if errors.Is(e, mongo.ErrNoDocuments) {
			return SharedView{}, fmt.Errorf("shared snapshot is pending refresh")
		}
		if e != nil {
			return SharedView{}, e
		}
		if snap.GrantHash != PermissionHash(g) {
			return SharedView{}, fmt.Errorf("shared snapshot needs refresh")
		}
		for key, value := range snap.Rendered.Values {
			snap.Rendered.Values[key] = snapshotValue(value)
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
		snap = Snapshot{GrantHash: PermissionHash(g), ID: g.ID, OwnerUser: g.OwnerUser, DashboardID: g.DashboardID, Version: v.Version, Title: v.Spec.Title, Rendered: rendered, At: time.Now().UTC()}
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
		}{snap.Version}, func(context.Context) error { return nil })
		return err
	})
	return SharedView{g, snap}, e
}

func snapshotValue(value any) any {
	switch v := value.(type) {
	case bson.D:
		out := make(map[string]any, len(v))
		for _, field := range v {
			out[field.Key] = snapshotValue(field.Value)
		}
		return out
	case bson.A:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = snapshotValue(item)
		}
		return out
	case bson.M:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = snapshotValue(item)
		}
		return out
	default:
		return value
	}
}
func (s *Service) ListOwned(ctx context.Context, p principal.Principal, id string) ([]Grant, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	f := bson.M{"owner_user": p.UserID}
	if id != "" {
		f["dashboard_id"] = id
	}
	return s.list(ctx, f)
}
func (s *Service) ListReceived(ctx context.Context, p principal.Principal) ([]Grant, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	f := activeFilter()
	f["grantee_user"] = p.UserID
	return s.list(ctx, f)
}
func (s *Service) list(ctx context.Context, f bson.M) ([]Grant, error) {
	cur, e := s.repo.db.Collection("shares").Find(ctx, f, options.Find().SetLimit(501).SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if e != nil {
		return nil, e
	}
	defer cur.Close(ctx)
	out := []Grant{}
	e = cur.All(ctx, &out)
	if len(out) > 500 {
		return nil, fmt.Errorf("too many shares; select a dashboard")
	}
	return out, e
}
func (s *Service) Unshare(ctx context.Context, p principal.Principal, id string) error {
	if e := p.Require("dash:share"); e != nil {
		return e
	}
	var g Grant
	e := s.repo.db.Collection("shares").FindOne(ctx, bson.M{"_id": id, "owner_user": p.UserID}).Decode(&g)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return principal.ErrNotFound
	}
	if e != nil {
		return e
	}
	now := time.Now().UTC()
	_, e = s.audit.Write(ctx, p, "dashboard_unshare", audit.Target{Collection: "shares", ID: id}, "", g, struct {
		Revoked bool `json:"revoked"`
	}{true}, func(tx context.Context) error {
		if _, e := s.repo.db.Collection("shares").UpdateOne(tx, bson.M{"_id": id, "owner_user": p.UserID}, bson.M{"$set": bson.M{"revoked_at": now}}); e != nil {
			return e
		}
		_, e := s.repo.db.Collection("dashboard_snapshots").DeleteOne(tx, bson.M{"_id": id, "owner_user": p.UserID})
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
		_, e = s.repo.db.Collection("dashboard_snapshots").DeleteMany(ctx, bson.M{"owner_user": p.UserID, "dashboard_id": id})
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
	snap := Snapshot{GrantHash: PermissionHash(g), ID: g.ID, OwnerUser: g.OwnerUser, DashboardID: g.DashboardID, Version: v.Version, Title: v.Spec.Title, Rendered: rendered, At: time.Now().UTC()}
	if _, e = s.repo.db.Collection("dashboard_snapshots").ReplaceOne(ctx, bson.M{"_id": g.ID, "owner_user": g.OwnerUser}, snap, options.Replace().SetUpsert(true)); e != nil {
		return e
	}
	_, e = s.repo.db.Collection("shares").UpdateOne(ctx, bson.M{"_id": g.ID, "owner_user": g.OwnerUser, "revoked_at": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"last_refresh": snap.At}})
	return e
}
func (s *Service) RefreshDue(ctx context.Context) error {
	cur, err := s.repo.db.Collection("shares").Find(ctx, activeFilter(), options.Find().SetBatchSize(100))
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	var failures []error
	for cur.Next(ctx) {
		var g Grant
		if err = cur.Decode(&g); err != nil {
			return err
		}
		if err = s.refreshDueGrant(ctx, g); err != nil {
			failures = append(failures, fmt.Errorf("share %s: %w", g.ID, err))
		}
	}
	return errors.Join(append(failures, cur.Err())...)
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
		var snap Snapshot
		err = s.repo.db.Collection("dashboard_snapshots").FindOne(ctx, bson.M{"_id": g.ID, "owner_user": g.OwnerUser}).Decode(&snap)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
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
