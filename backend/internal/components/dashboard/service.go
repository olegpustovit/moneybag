package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/reqcache"
	"github.com/tiendc/go-deepcopy"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var dashboardID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo      *MongoRepository
	engine    *Engine
	audit     *audit.Service
	snapshots *Snapshots
	// Lint, when set, returns report-only consistency findings that Publish attaches as warnings.
	// It is a hook because the consistency component imports dashboard.
	Lint func(context.Context, principal.Principal, Spec) ([]string, error)
}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func NewService(r *MongoRepository, e *Engine, a *audit.Service) *Service {
	return &Service{repo: r, engine: e, audit: a}
}
func (s *Service) List(ctx context.Context, p principal.Principal) ([]Head, error) {
	if e := p.Require("ledger:read"); e != nil {
		return nil, e
	}
	cur, e := s.repo.db.Collection("dashboards").Find(ctx, bson.M{"user_id": p.UserID, "archived": bson.M{"$ne": true}}, options.Find().SetSort(bson.D{{Key: "id", Value: 1}}).SetLimit(501))
	if e != nil {
		return nil, e
	}
	defer cur.Close(ctx)
	out := []Head{}
	e = cur.All(ctx, &out)
	if len(out) > 500 {
		return nil, fmt.Errorf("dashboard catalog exceeds 500")
	}
	return out, e
}
func (s *Service) head(ctx context.Context, p principal.Principal, id string) (Head, error) {
	if e := p.Valid(); e != nil {
		return Head{}, e
	}
	var h Head
	e := s.repo.db.Collection("dashboards").FindOne(ctx, bson.M{"user_id": p.UserID, "id": id, "archived": bson.M{"$ne": true}}).Decode(&h)
	if errors.Is(e, mongo.ErrNoDocuments) {
		e = principal.ErrNotFound
	}
	return h, e
}

type GetResult struct {
	Spec        Spec            `json:"spec"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	Author      principal.Actor `json:"author"`
	Note        string          `json:"note"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
}

func (s *Service) UseSnapshots(cache *Snapshots) { s.snapshots = cache }

func (s *Service) Evaluate(ctx context.Context, p principal.Principal, expr string, data map[string]Query, params map[string]any) (expressions.Result, error) {
	if err := p.Require("ledger:read"); err != nil {
		return expressions.Result{}, err
	}
	return s.engine.Evaluate(ctx, p, expr, data, params)
}

func (s *Service) ValidateSpec(ctx context.Context, p principal.Principal, spec Spec) (Validation, error) {
	if err := p.Require("ledger:read"); err != nil {
		return Validation{}, err
	}
	result, err := s.engine.Execute(ctx, p, spec)
	return result.Validation, err
}

func (s *Service) Get(ctx context.Context, p principal.Principal, id, ref string) (GetResult, error) {
	if s.snapshots == nil || mongodb.InWorkflow(ctx) {
		return s.get(ctx, p, id, ref)
	}
	if err := p.Require("ledger:read"); err != nil {
		return GetResult{}, err
	}
	value, err := s.snapshots.read(ctx, p, "dashboard", id, ref, func() (any, error) {
		result, err := s.get(ctx, p, id, ref)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}, nil)
	if err != nil {
		return GetResult{}, err
	}
	var result GetResult
	if err := json.Unmarshal(value.([]byte), &result); err != nil {
		return GetResult{}, err
	}
	return result, nil
}

func (s *Service) get(ctx context.Context, p principal.Principal, id, ref string) (GetResult, error) {
	if e := p.Require("ledger:read"); e != nil {
		return GetResult{}, e
	}
	h, e := s.head(ctx, p, id)
	if e != nil {
		return GetResult{}, e
	}
	n := h.DraftVersion
	if n == 0 {
		n = h.PublishedVersion
	}
	switch ref {
	case "", "draft":
	case "published":
		n = h.PublishedVersion
	default:
		n, e = strconv.Atoi(ref)
		if e != nil || n < 1 {
			return GetResult{}, fmt.Errorf("version must be draft, published or positive number")
		}
	}
	if n == 0 {
		return GetResult{}, fmt.Errorf("dashboard has no published version")
	}
	var v Version
	e = s.repo.db.Collection("dashboard_versions").FindOne(ctx, bson.M{"user_id": p.UserID, "dashboard_id": id, "version": n}).Decode(&v)
	if errors.Is(e, mongo.ErrNoDocuments) {
		e = principal.ErrNotFound
	}
	if e != nil {
		return GetResult{}, e
	}
	status := "superseded"
	if n == h.DraftVersion {
		status = "draft"
	}
	if n == h.PublishedVersion {
		status = "published"
		if h.PublishedBy.ID != "" {
			v.Author = h.PublishedBy
			v.Note = h.PublishNote
		}
	}
	result := GetResult{Spec: v.Spec, Version: n, Status: status, Author: v.Author, Note: v.Note}
	if status == "published" {
		result.PublishedAt = h.PublishedAt
		if result.PublishedAt == nil && s.audit != nil {
			event, err := s.audit.LatestCompleted(ctx, p, "dashboard_publish", audit.Target{Collection: "dashboards", ID: id})
			if err != nil {
				if ctx.Err() != nil {
					return GetResult{}, ctx.Err()
				}
				slog.Warn("legacy dashboard publication time unavailable", "error", err)
			} else if event != nil && event.Actor == result.Author {
				var published struct {
					Version int `json:"published_version"`
				}
				if json.Unmarshal(event.Outcome.After, &published) == nil && published.Version == n && !event.Outcome.At.IsZero() {
					at := event.Outcome.At
					result.PublishedAt = &at
				}
			}
		}
	}
	return result, e
}

type CreateInput struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	FromTemplate string `json:"from_template,omitempty"`
}

func (s *Service) Create(ctx context.Context, p principal.Principal, in CreateInput) (GetResult, error) {
	if e := p.Require("dash:write"); e != nil {
		return GetResult{}, e
	}
	if !dashboardID.MatchString(in.ID) || strings.TrimSpace(in.Title) == "" || len(in.Title) > 200 {
		return GetResult{}, fmt.Errorf("id must use 1..100 letters, digits, hyphens or underscores; title is required")
	}

	spec := Spec{ID: in.ID, Title: in.Title, Version: 1, Data: map[string]Query{}, Pages: []Page{{ID: "main", Title: "Overview", Layout: []Row{}}}, Components: []Component{}}
	if in.FromTemplate != "" {
		var ok bool
		spec, ok = Templates[in.FromTemplate]
		if !ok {
			return GetResult{}, fmt.Errorf("unknown template; choose budget-vs-actual or forecast")
		}
		b, _ := json.Marshal(spec)
		var copy Spec
		_ = json.Unmarshal(b, &copy)
		spec = copy
		spec.ID = in.ID
		spec.Title = in.Title
		spec.Version = 1
		if _, ok := spec.Params["month"]; ok {
			spec.Params["month"] = time.Now().Format("2006-01")
		}
	}
	h := Head{Key: p.UserID + ":" + in.ID, UserID: p.UserID, ID: in.ID, Title: in.Title, DraftVersion: 1, Revision: 1}
	v := Version{Key: h.Key + ":1", UserID: p.UserID, DashboardID: in.ID, Version: 1, Spec: spec, Author: p.Actor, CreatedAt: time.Now().UTC()}
	_, e := s.audit.Write(ctx, p, "dashboard_create", audit.Target{Collection: "dashboards", ID: in.ID}, "", nil, v, func(tx context.Context) error {
		if _, e := s.repo.db.Collection("dashboards").InsertOne(tx, h); e != nil {
			if mongo.IsDuplicateKeyError(e) {
				return principal.ErrConflict
			}
			return e
		}
		_, e := s.repo.db.Collection("dashboard_versions").InsertOne(tx, v)
		return e
	})
	return GetResult{Spec: spec, Version: 1, Status: "draft", Author: p.Actor}, e
}

type EditResult struct {
	EventID      string     `json:"event_id,omitempty"`
	DraftVersion int        `json:"draft_version"`
	ComponentID  string     `json:"component_id,omitempty"`
	Validation   Validation `json:"validation"`
}

func (s *Service) edit(ctx context.Context, p principal.Principal, id, tool string, change func(*Spec) error) (EditResult, error) {
	if e := p.Require("dash:write"); e != nil {
		return EditResult{}, e
	}
	var out EditResult
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		h, e := s.head(tx, p, id)
		if e != nil {
			return e
		}
		old, e := s.Get(tx, p, id, "")
		if e != nil {
			return e
		}
		encoded, err := json.Marshal(old.Spec)
		if err != nil {
			return err
		}
		var spec Spec
		if err = json.Unmarshal(encoded, &spec); err != nil {
			return err
		}
		if e = change(&spec); e != nil {
			return e
		}
		spec.ID = id
		spec.Version = h.Revision + 1
		v := Version{Key: h.Key + ":" + strconv.Itoa(spec.Version), UserID: p.UserID, DashboardID: id, Version: spec.Version, Spec: spec, Author: p.Actor, CreatedAt: time.Now().UTC()}
		eventID, e := s.audit.Write(tx, p, tool, audit.Target{Collection: "dashboards", ID: id}, "", old, v, func(tx context.Context) error {
			if _, e := s.repo.db.Collection("dashboard_versions").InsertOne(tx, v); e != nil {
				return e
			}
			r, e := s.repo.db.Collection("dashboards").UpdateOne(tx, bson.M{"user_id": p.UserID, "id": id, "revision": h.Revision}, bson.M{"$set": bson.M{"draft_version": spec.Version, "revision": spec.Version, "title": spec.Title}})
			if e == nil && r.ModifiedCount != 1 {
				return principal.ErrConflict
			}
			return e
		})
		if e != nil {
			return e
		}
		r, e := s.engine.Execute(tx, p, spec)
		if e != nil {
			return e
		}
		out = EditResult{EventID: eventID, DraftVersion: spec.Version, Validation: r.Validation}
		return nil
	})
	return out, e
}

type AddInput struct {
	DashboardID string        `json:"dashboard_id"`
	Page        string        `json:"page,omitempty"`
	Component   Component     `json:"component"`
	Placement   *AddPlacement `json:"placement,omitempty"`
}
type AddPlacement struct {
	Row  int  `json:"row,omitempty"`
	New  bool `json:"new,omitempty"`
	Span int  `json:"span,omitempty"`
}

func (s *Service) Add(ctx context.Context, p principal.Principal, in AddInput) (EditResult, error) {
	r, e := s.edit(ctx, p, in.DashboardID, "dashboard_add_component", func(spec *Spec) error {
		if in.Component.ID == "" {
			return fmt.Errorf("component id required")
		}
		for _, c := range spec.Components {
			if c.ID == in.Component.ID {
				return fmt.Errorf("duplicate component id")
			}
		}
		index := -1
		for i, pg := range spec.Pages {
			if in.Page == "" && i == 0 || pg.ID == in.Page {
				index = i
				break
			}
		}
		if index < 0 && in.Page == "" {
			return fmt.Errorf("page not found")
		}
		if index < 0 {
			spec.Pages = append(spec.Pages, Page{ID: in.Page, Title: in.Page})
			index = len(spec.Pages) - 1
		}
		span := 12
		if in.Placement != nil && in.Placement.Span != 0 {
			span = in.Placement.Span
		}
		if span < 1 || span > 12 {
			return fmt.Errorf("span must be 1..12")
		}
		col := Placement{span, in.Component.ID}
		if in.Placement == nil || in.Placement.New {
			spec.Pages[index].Layout = append(spec.Pages[index].Layout, Row{Cols: []Placement{col}})
		} else {
			row := in.Placement.Row
			if row < 0 || row >= len(spec.Pages[index].Layout) {
				return fmt.Errorf("placement row out of range")
			}
			spec.Pages[index].Layout[row].Cols = append(spec.Pages[index].Layout[row].Cols, col)
		}
		spec.Components = append(spec.Components, in.Component)
		return nil
	})
	r.ComponentID = in.Component.ID
	return r, e
}
func (s *Service) Compose(ctx context.Context, p principal.Principal, id, page string, layout []Row) (EditResult, error) {
	return s.edit(ctx, p, id, "dashboard_compose", func(spec *Spec) error {
		for i, pg := range spec.Pages {
			if page == "" && i == 0 || pg.ID == page {
				spec.Pages[i].Layout = layout
				return nil
			}
		}
		spec.Pages = append(spec.Pages, Page{ID: page, Title: page, Layout: layout})
		return nil
	})
}
func (s *Service) Remove(ctx context.Context, p principal.Principal, id, component string) (EditResult, error) {
	return s.edit(ctx, p, id, "dashboard_remove_component", func(spec *Spec) error {
		found := false
		out := []Component{}
		for _, c := range spec.Components {
			if c.ID == component {
				found = true
			} else {
				out = append(out, c)
			}
		}
		if !found {
			return principal.ErrNotFound
		}
		spec.Components = out
		for pi := range spec.Pages {
			for ri := range spec.Pages[pi].Layout {
				cols := []Placement{}
				for _, col := range spec.Pages[pi].Layout[ri].Cols {
					if col.Component != component {
						cols = append(cols, col)
					}
				}
				spec.Pages[pi].Layout[ri].Cols = cols
			}
		}
		return nil
	})
}
func (s *Service) UpdateComponent(ctx context.Context, p principal.Principal, id, component string, set map[string]json.RawMessage) (EditResult, error) {
	return s.edit(ctx, p, id, "dashboard_update_component", func(spec *Spec) error {
		for i, c := range spec.Components {
			if c.ID != component {
				continue
			}
			b, _ := json.Marshal(c)
			var m map[string]json.RawMessage
			_ = json.Unmarshal(b, &m)
			for k, v := range set {
				if k == "id" {
					return fmt.Errorf("component id cannot change")
				}
				m[k] = v
			}
			b, e := json.Marshal(m)
			if e != nil {
				return e
			}
			decoder := json.NewDecoder(strings.NewReader(string(b)))
			decoder.DisallowUnknownFields()
			return decoder.Decode(&spec.Components[i])
		}
		return principal.ErrNotFound
	})
}
func (s *Service) Patch(ctx context.Context, p principal.Principal, id string, ops json.RawMessage) (EditResult, error) {
	patch, e := decodePatch(ops)
	if e != nil {
		return EditResult{}, e
	}
	return s.edit(ctx, p, id, "dashboard_patch", func(spec *Spec) error {
		return applyPatch(spec, patch)
	})
}

// PatchPreview applies ops to the current draft in memory and validates the result without writing.
func (s *Service) PatchPreview(ctx context.Context, p principal.Principal, id string, ops json.RawMessage) (EditResult, error) {
	patch, e := decodePatch(ops)
	if e != nil {
		return EditResult{}, e
	}
	old, e := s.Get(ctx, p, id, "")
	if e != nil {
		return EditResult{}, e
	}
	encoded, e := json.Marshal(old.Spec)
	if e != nil {
		return EditResult{}, e
	}
	var spec Spec
	if e = json.Unmarshal(encoded, &spec); e != nil {
		return EditResult{}, e
	}
	if e = applyPatch(&spec, patch); e != nil {
		var pe *patchError
		if errors.As(e, &pe) {
			return EditResult{DraftVersion: old.Version, Validation: Validation{Errors: []Issue{{Path: fmt.Sprintf("/ops/%d", pe.Op), Msg: pe.Msg, Field: pe.Path, Hint: pe.Hint}}}}, nil
		}
		return EditResult{}, e
	}
	spec.ID = id
	v, e := s.ValidateSpec(ctx, p, spec)
	return EditResult{DraftVersion: old.Version, Validation: v}, e
}

func decodePatch(ops json.RawMessage) (jsonpatch.Patch, error) {
	// ops may arrive as a JSON string containing the array.
	var text string
	if json.Unmarshal(ops, &text) == nil {
		ops = json.RawMessage(text)
	}
	if len(ops) > 1<<20 {
		return nil, fmt.Errorf("patch exceeds 1 MiB")
	}
	patch, e := jsonpatch.DecodePatch(ops)
	if e != nil {
		return nil, fmt.Errorf("invalid RFC6902 patch: %w", e)
	}
	if len(patch) > 500 {
		return nil, fmt.Errorf("at most 500 patch operations")
	}
	return patch, nil
}

// patchError names the failing op and what to do about it; Error keeps the text the write path returns.
type patchError struct {
	Op        int
	Path, Msg string
	Hint      string
}

func (e *patchError) Error() string {
	return fmt.Sprintf("op %d: %s; %s", e.Op, e.Msg, e.Hint)
}

func applyPatch(spec *Spec, patch jsonpatch.Patch) error {
	b, _ := json.Marshal(spec)
	next := b
	for i, op := range patch {
		path, _ := op.Path()
		var e error
		if next, e = (jsonpatch.Patch{op}).Apply(next); e == nil {
			continue
		}
		if errors.Is(e, jsonpatch.ErrMissing) {
			return &patchError{Op: i, Path: path, Msg: fmt.Sprintf("path %s not found", path), Hint: "read valid paths with dashboard_summary or dashboard_get"}
		}
		if op.Kind() == "test" {
			return &patchError{Op: i, Path: path, Msg: fmt.Sprintf("test %s failed", path), Hint: "the draft changed, so re-read it with dashboard_summary and retry"}
		}
		return &patchError{Op: i, Path: path, Msg: fmt.Sprintf("%s %s: %v", op.Kind(), path, e), Hint: "check the value type against dashboard_get"}
	}
	// Decode into a fresh Spec: decoding into the old one merges maps, so removed queries would survive.
	var patched Spec
	decoder := json.NewDecoder(strings.NewReader(string(next)))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&patched); e != nil {
		return fmt.Errorf("invalid spec: %w; unknown fields are rejected, so check field names in dashboard_get", e)
	}
	*spec = patched
	return nil
}
func (s *Service) Preview(ctx context.Context, p principal.Principal, id, version string, params map[string]any) (Rendered, error) {
	if s.snapshots == nil || mongodb.InWorkflow(ctx) {
		return s.preview(ctx, p, id, version, params)
	}
	if err := p.Require("ledger:read"); err != nil {
		return Rendered{}, err
	}
	ctx = expressions.WithVolatility(reqcache.With(ctx))
	if len(params) == 0 {
		params = nil
	}
	key := struct {
		Version string
		Params  map[string]any
	}{version, params}
	value, err := s.snapshots.read(ctx, p, "render", id, key, func() (any, error) {
		return s.preview(ctx, p, id, version, params)
	}, func(v any) bool { return v.(Rendered).Validation.OK })
	if err != nil {
		return Rendered{}, err
	}
	var result Rendered
	if err := deepcopy.Copy(&result, value); err != nil {
		return Rendered{}, fmt.Errorf("copy dashboard snapshot: %w", err)
	}
	return result, nil
}

func (s *Service) preview(ctx context.Context, p principal.Principal, id, version string, params map[string]any) (Rendered, error) {
	v, e := s.Get(ctx, p, id, version)
	if e != nil {
		return Rendered{}, e
	}
	if len(params) > 0 {
		if v.Spec.Params == nil {
			v.Spec.Params = map[string]any{}
		}
		for k, vv := range params {
			v.Spec.Params[k] = vv
		}
	}
	return s.engine.Execute(ctx, p, v.Spec)
}

type PublishResult struct {
	EventID          string     `json:"event_id,omitempty"`
	PublishedVersion int        `json:"published_version"`
	Gate             Validation `json:"gate"`
	URL              string     `json:"url"`
	Warnings         []string   `json:"warnings,omitempty"`
}

func (s *Service) Publish(ctx context.Context, p principal.Principal, id string, version int, note string) (PublishResult, error) {
	return s.publish(ctx, p, id, version, note, false)
}
func (s *Service) PublishDraft(ctx context.Context, p principal.Principal, id string, version int, note string) (PublishResult, error) {
	return s.publish(ctx, p, id, version, note, true)
}
func (s *Service) publish(ctx context.Context, p principal.Principal, id string, version int, note string, draftOnly bool) (PublishResult, error) {
	if e := p.Require("dash:write"); e != nil {
		return PublishResult{}, e
	}
	var out PublishResult
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		h, e := s.head(tx, p, id)
		if e != nil {
			return e
		}
		if version == 0 {
			version = h.DraftVersion
		}
		if draftOnly && (version == 0 || version != h.DraftVersion) {
			return principal.ErrConflict
		}
		if version == 0 {
			return fmt.Errorf("no draft to publish")
		}
		v, e := s.Get(tx, p, id, strconv.Itoa(version))
		if e != nil {
			return e
		}
		rendered, e := s.engine.Execute(tx, p, v.Spec)
		if e != nil {
			return e
		}
		out = PublishResult{PublishedVersion: version, Gate: rendered.Validation, URL: "/dashboards/" + id}
		if s.Lint != nil {
			if found, e := s.Lint(tx, p, v.Spec); e == nil {
				out.Warnings = append(out.Warnings, found...)
			}
		}
		if !rendered.Validation.OK {
			reasons := []string{}
			for _, i := range rendered.Validation.Errors {
				reasons = append(reasons, i.String())
			}
			return fmt.Errorf("publish gate failed: %s", strings.Join(reasons, "; "))
		}
		eventID, e := s.audit.Write(tx, p, "dashboard_publish", audit.Target{Collection: "dashboards", ID: id}, note, h, out, func(tx context.Context) error {
			update := bson.M{"published_version": version, "published_by": p.Actor, "publish_note": note, "published_at": time.Now().UTC()}
			if h.DraftVersion == version {
				update["draft_version"] = 0
			}
			r, e := s.repo.db.Collection("dashboards").UpdateOne(tx, bson.M{"user_id": p.UserID, "id": id, "revision": h.Revision}, bson.M{"$set": update})
			if e == nil && r.MatchedCount != 1 {
				return principal.ErrConflict
			}
			return e
		})
		if e == nil {
			out.EventID = eventID
		}
		return e
	})
	return out, e
}
func (s *Service) Rollback(ctx context.Context, p principal.Principal, id string, version int) (PublishResult, error) {
	if version < 1 {
		return PublishResult{}, fmt.Errorf("positive to_version required")
	}
	return s.Publish(ctx, p, id, version, "Rollback to v"+strconv.Itoa(version))
}

type Change struct {
	Path   string `json:"path"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

func (s *Service) Diff(ctx context.Context, p principal.Principal, id, from, to string) ([]Change, error) {
	a, e := s.Get(ctx, p, id, from)
	if e != nil {
		return nil, e
	}
	b, e := s.Get(ctx, p, id, to)
	if e != nil {
		return nil, e
	}
	am, e := native(a.Spec)
	if e != nil {
		return nil, e
	}
	bm, e := native(b.Spec)
	if e != nil {
		return nil, e
	}
	out := []Change{}
	keys := map[string]bool{}
	for k := range am {
		keys[k] = true
	}
	for k := range bm {
		keys[k] = true
	}
	for k := range keys {
		x, _ := json.Marshal(am[k])
		y, _ := json.Marshal(bm[k])
		if string(x) != string(y) {
			out = append(out, Change{Path: "/" + k, Before: am[k], After: bm[k]})
		}
	}
	return out, nil
}

// Unpublish takes the dashboard offline without touching drafts or versions; undo or a new publish restores it.
func (s *Service) Unpublish(ctx context.Context, p principal.Principal, id, reason string) error {
	if e := p.Require("dash:write"); e != nil {
		return e
	}
	return s.audit.Workflow(ctx, func(tx context.Context) error {
		h, e := s.head(tx, p, id)
		if e != nil {
			return e
		}
		if h.PublishedVersion == 0 {
			return fmt.Errorf("dashboard is not published")
		}
		draft := h.DraftVersion
		if draft == 0 {
			draft = h.PublishedVersion
		}
		_, e = s.audit.Write(tx, p, "dashboard_unpublish", audit.Target{Collection: "dashboards", ID: id}, reason, h, struct {
			PublishedVersion int `json:"published_version"`
		}{0}, func(tx context.Context) error {
			r, e := s.repo.db.Collection("dashboards").UpdateOne(tx, bson.M{"user_id": p.UserID, "id": id, "revision": h.Revision}, bson.M{"$set": bson.M{"published_version": 0, "draft_version": draft}})
			if e == nil && r.MatchedCount != 1 {
				return principal.ErrConflict
			}
			return e
		})
		return e
	})
}
