package integration

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/expressions"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestDashboardPatchPreviewWritesNothing(t *testing.T) {
	uri := os.Getenv("MONEYBAG_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set MONEYBAG_TEST_MONGO_URI")
	}
	parsed, err := url.Parse(uri)
	if err != nil || !strings.HasPrefix(parsed.Path, "/moneybag_test_") {
		t.Fatal("dedicated test database required")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := client.Database(strings.TrimPrefix(parsed.Path, "/") + "_preview_apply")
	if err := db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Drop(ctx); _ = client.Disconnect(ctx) })
	p := principal.Principal{UserID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Scopes: []string{"ledger:read", "dash:write"}}
	a := audit.NewService(audit.NewMongoRepository(db))
	expr, err := expressions.New()
	if err != nil {
		t.Fatal(err)
	}
	service := dashboard.NewService(dashboard.NewMongoRepository(db), &dashboard.Engine{Expr: expr, Reader: &snapshotReader{}}, a)
	spec := dashboard.Spec{ID: "apply", Title: "Draft", Version: 2, Pages: []dashboard.Page{{ID: "main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 12, Component: "value"}}}}}}, Components: []dashboard.Component{{ID: "value", Type: "kpi", Value: "1"}}}
	head := dashboard.Head{Key: p.UserID + ":apply", UserID: p.UserID, ID: "apply", Title: "Draft", DraftVersion: 2, Revision: 2}
	if _, err := db.Collection("dashboards").InsertOne(ctx, head); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("dashboard_versions").InsertOne(ctx, dashboard.Version{Key: p.UserID + ":apply:2", UserID: p.UserID, DashboardID: "apply", Version: 2, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	ops, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "/title", "value": "Previewed"}})
	result, err := service.PatchPreview(ctx, p, "apply", ops)
	if err != nil || !result.Validation.OK || result.DraftVersion != 2 {
		t.Fatalf("preview: %+v %v", result, err)
	}
	draft, err := service.Get(ctx, p, "apply", "")
	if err != nil || draft.Spec.Title != "Draft" || draft.Version != 2 {
		t.Fatalf("preview wrote the draft: %+v %v", draft, err)
	}
	missing, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "/nope", "value": 1}})
	bad, err := service.PatchPreview(ctx, p, "apply", missing)
	if err != nil || bad.Validation.OK || len(bad.Validation.Errors) != 1 || bad.Validation.Errors[0].Path != "/ops/0" || bad.Validation.Errors[0].Field != "/nope" || !strings.Contains(bad.Validation.Errors[0].Hint, "read valid paths") {
		t.Fatalf("missing path: %+v %v", bad.Validation, err)
	}
	stale, _ := json.Marshal([]map[string]any{{"op": "test", "path": "/version", "value": 1}, {"op": "replace", "path": "/title", "value": "Stale"}})
	if _, err := service.Patch(ctx, p, "apply", stale); err == nil || !strings.Contains(err.Error(), "the draft changed") {
		t.Fatalf("stale if_version accepted: %v", err)
	}
	current, _ := json.Marshal([]map[string]any{{"op": "test", "path": "/version", "value": 2}, {"op": "replace", "path": "/title", "value": "Edited"}})
	if _, err := service.Patch(ctx, p, "apply", current); err != nil {
		t.Fatalf("current if_version rejected: %v", err)
	}
	edited, err := service.Get(ctx, p, "apply", "")
	if err != nil || edited.Spec.Title != "Edited" {
		t.Fatalf("current write missing: %+v %v", edited, err)
	}
}
