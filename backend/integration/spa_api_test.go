package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/history"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func testSPAAPI(t *testing.T, ctx context.Context, endpoint string, p principal.Principal, services mcpserver.Services, db *mongo.Database) {
	t.Helper()
	user, err := services.Identity.User(ctx, p.UserID)
	if err != nil {
		t.Fatal(err)
	}
	raw, session, err := services.Identity.StartSession(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if _, err := services.Tables.CreateNamed(ctx, p, "catalog-test", tables.CreateInput{Name: "Catalog test", Columns: []tables.Column{{ID: "amount", Type: "number"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := services.Dashboard.UpsertView(ctx, p, dashboard.ViewInput{ID: "catalog-test", Query: dashboard.Query{From: "table:catalog-test", Derive: map[string]string{"doubled": "row.amount * 2"}}}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, form url.Values, signed, csrf, jsonAccept bool) (int, map[string]any, http.Header) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, endpoint+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		if signed {
			req.AddCookie(&http.Cookie{Name: "moneybag_session", Value: raw})
		}
		if csrf {
			req.Header.Set("X-CSRF-Token", session.CSRF)
		}
		if jsonAccept {
			req.Header.Set("Accept", "application/json")
		}
		if method == "POST" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body map[string]any
		if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
		}
		return res.StatusCode, body, res.Header
	}

	{
		token, err := services.Identity.Invite(ctx, p, "shared-spa@example.test")
		if err != nil {
			t.Fatal(err)
		}
		viewer, err := services.Identity.Register(ctx, token, "Shared viewer", "synthetic-test-password")
		if err != nil {
			t.Fatal(err)
		}
		viewerRaw, viewerSession, err := services.Identity.StartSession(ctx, viewer)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = services.Dashboard.Create(ctx, p, dashboard.CreateInput{ID: "spa-shared", Title: "Shared"}); err != nil {
			t.Fatal(err)
		}
		spec := dashboard.Spec{ID: "spa-shared", Title: "Shared", Data: map[string]dashboard.Query{}, Pages: []dashboard.Page{{ID: "main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 12, Component: "visible"}}}}}}, Components: []dashboard.Component{{ID: "visible", Type: "kpi", Value: "42", Format: "usd0"}, {ID: "hidden", Type: "text", MD: "hidden-secret"}}}
		ops, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "", "value": spec}})
		draft, err := services.Dashboard.Patch(ctx, p, "spa-shared", ops)
		if err != nil || !draft.Validation.OK {
			t.Fatal(draft, err)
		}
		if _, err = services.Dashboard.Publish(ctx, p, "spa-shared", draft.DraftVersion, "Synthetic"); err != nil {
			t.Fatal(err)
		}
		grant, err := services.Sharing.Share(ctx, p, sharing.ShareInput{DashboardID: "spa-shared", Grantee: viewer.ID, Role: "viewer"})
		if err != nil {
			t.Fatal(err)
		}
		path := "/api/shared/" + grant.ShareID
		if status, _, _ := request("GET", path, nil, false, false, true); status != 401 {
			t.Fatal("anonymous share", status)
		}
		if status, _, _ := request("GET", path, nil, true, false, true); status != 404 {
			t.Fatal("owner received viewer grant", status)
		}
		fetchShared := func() (int, map[string]any) {
			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(&http.Cookie{Name: "moneybag_session", Value: viewerRaw})
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var body map[string]any
			if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if res.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("shared response cached")
			}
			return res.StatusCode, body
		}
		status, body := fetchShared()
		if status != 200 {
			t.Fatal("viewer share", status, body)
		}
		rawBody, _ := json.Marshal(body)
		if strings.Contains(string(rawBody), "hidden-secret") || strings.Contains(string(rawBody), "computed") {
			t.Fatal("unplaced data exposed", string(rawBody))
		}
		values := body["values"].(map[string]any)
		if len(values) != 1 || values["visible"].(map[string]any)["value"] != nil || len(body["pages"].([]any)) != 1 {
			t.Fatal("snapshot projection", body)
		}

		editorRequest := func(method, suffix string, payload any, csrf bool) (int, map[string]any) {
			var text string
			if payload != nil {
				raw, e := json.Marshal(payload)
				if e != nil {
					t.Fatal(e)
				}
				text = string(raw)
			}
			req, e := http.NewRequestWithContext(ctx, method, endpoint+path+suffix, strings.NewReader(text))
			if e != nil {
				t.Fatal(e)
			}
			req.AddCookie(&http.Cookie{Name: "moneybag_session", Value: viewerRaw})
			req.Header.Set("Content-Type", "application/json")
			if csrf {
				req.Header.Set("X-CSRF-Token", viewerSession.CSRF)
			}
			res, e := client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			defer res.Body.Close()
			var body map[string]any
			if e = json.NewDecoder(res.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
			return res.StatusCode, body
		}
		if code, _ := editorRequest("GET", "/draft", nil, false); code != 404 {
			t.Fatal("viewer draft access", code)
		}
		if code, _ := editorRequest("GET", "/catalog", nil, false); code != 404 {
			t.Fatal("viewer catalog access", code)
		}
		if code, _ := editorRequest("POST", "/patch", map[string]any{"ops": []any{}}, false); code != 403 {
			t.Fatal("missing csrf", code)
		}
		if code, _ := editorRequest("POST", "/evaluate", map[string]any{"expr": "1", "data": map[string]any{}, "params": map[string]any{}}, true); code != 404 {
			t.Fatal("viewer evaluate", code)
		}
		if code, _ := editorRequest("POST", "/validate", map[string]any{"spec": spec}, true); code != 404 {
			t.Fatal("viewer validate", code)
		}
		for _, name := range []string{"shared-allowed", "shared-blocked"} {
			if _, err = services.Tables.CreateNamed(ctx, p, name, tables.CreateInput{Name: name, Columns: []tables.Column{{ID: "amount", Type: "number"}}}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = services.Tables.Update(ctx, p, tables.UpdateInput{DB: "shared-allowed", Updates: []tables.CellUpdate{{RowID: "one", Column: "amount", Value: 1.0}, {RowID: "two", Column: "amount", Value: 2.0}}}); err != nil {
			t.Fatal(err)
		}
		editorGrant, err := services.Sharing.Share(ctx, p, sharing.ShareInput{DashboardID: "spa-shared", Grantee: viewer.ID, Role: "editor", Mode: "live", Sources: []string{"table:shared-allowed"}, RowFilter: "row.amount >= 2.0"})
		if err != nil {
			t.Fatal(err)
		}
		if editorGrant.ShareID != grant.ShareID {
			t.Fatal("share identity changed")
		}
		viewerPrincipal := identity.Principal(viewer)
		if _, err = services.Dashboard.Create(ctx, viewerPrincipal, dashboard.CreateInput{ID: grant.ShareID, Title: "Own collision"}); err != nil {
			t.Fatal(err)
		}

		code, catalog := editorRequest("GET", "/catalog", nil, false)
		if code != 200 {
			t.Fatal("editor catalog", code, catalog)
		}
		sources := catalog["sources"].([]any)
		if len(sources) != 1 || sources[0].(map[string]any)["name"] != "table:shared-allowed" {
			t.Fatal("catalog leaked sources", sources)
		}
		code, editDraft := editorRequest("GET", "/draft", nil, false)
		if code != 200 || editDraft["spec"].(map[string]any)["id"] != "spa-shared" {
			t.Fatal("grant collision", code, editDraft)
		}
		evaluate := func(source string) (int, map[string]any) {
			return editorRequest("POST", "/evaluate", map[string]any{"expr": "data.rows", "data": map[string]any{"rows": map[string]any{"from": source}}, "params": map[string]any{}}, true)
		}
		code, evaluated := evaluate("table:shared-allowed")
		if code != 200 {
			t.Fatal("shared evaluate", code, evaluated)
		}
		rows := evaluated["value"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["amount"] != float64(2) {
			t.Fatal("row filter", rows)
		}
		if code, _ = evaluate("table:shared-blocked"); code != 400 {
			t.Fatal("restricted source", code)
		}
		candidate := editDraft["spec"].(map[string]any)
		candidate["data"] = map[string]any{"rows": map[string]any{"from": "table:shared-allowed"}}
		candidate["components"] = []any{map[string]any{"id": "visible", "type": "kpi", "value": "1 / (2 - size(data.rows))"}}
		code, validation := editorRequest("POST", "/validate", map[string]any{"spec": candidate}, true)
		if code != 200 || validation["ok"] != true {
			t.Fatal("validate unsaved spec", code, validation)
		}
		candidate["data"] = map[string]any{"rows": map[string]any{"from": "table:shared-blocked"}}
		code, validation = editorRequest("POST", "/validate", map[string]any{"spec": candidate}, true)
		if code != 200 || validation["ok"] != false {
			t.Fatal("validate restricted source", code, validation)
		}
		if code, _ = editorRequest("POST", "/validate", map[string]any{"spec": candidate}, false); code != 403 {
			t.Fatal("validate missing csrf", code)
		}
		if current, err := services.Dashboard.Get(ctx, p, "spa-shared", "draft"); err != nil || current.Version != int(editDraft["version"].(float64)) {
			t.Fatal("validation changed draft", current, err)
		}
		version := editDraft["version"].(float64)
		patch := map[string]any{"ops": []any{map[string]any{"op": "test", "path": "/version", "value": version}, map[string]any{"op": "replace", "path": "/title", "value": "Editor changed"}}}
		code, patched := editorRequest("POST", "/patch", patch, true)
		if code != 200 || patched["event_id"] == nil {
			t.Fatal("editor patch event", code, patched)
		}
		if code, _ = editorRequest("POST", "/patch", patch, true); code != 400 {
			t.Fatal("stale editor patch", code)
		}
		newVersion := patched["draft_version"].(float64)
		if code, _ = editorRequest("POST", "/publish", map[string]any{"version": version, "note": "stale"}, true); code != 400 {
			t.Fatal("stale publish", code)
		}
		code, published := editorRequest("POST", "/publish", map[string]any{"version": newVersion, "note": "Editor publish"}, true)
		if published["event_id"] == nil {
			t.Fatal("editor publish event missing", published)
		}
		if code != 200 {
			t.Fatal("editor publish", code, published)
		}
		own, err := services.Dashboard.Get(ctx, viewerPrincipal, grant.ShareID, "")
		if err != nil || own.Spec.Title != "Own collision" {
			t.Fatal("edited wrong dashboard", own, err)
		}
		owner, err := services.Dashboard.Get(ctx, p, "spa-shared", "published")
		if err != nil || owner.Spec.Title != "Editor changed" {
			t.Fatal("owner publish", owner, err)
		}
		if code, _ = editorRequest("POST", "/undo", map[string]any{"event_id": published["event_id"], "reason": "test"}, false); code != 403 {
			t.Fatal("undo missing csrf", code)
		}
		if code, _ = editorRequest("POST", "/undo", map[string]any{"event_id": patched["event_id"], "reason": "stale"}, true); code != 400 {
			t.Fatal("stale editor undo", code)
		}
		ownerEvents, err := services.Audit.List(ctx, p, audit.ListInput{Tool: "dashboard_patch", Target: "spa-shared", Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		var ownerEvent string
		for _, event := range ownerEvents.Events {
			if event.GrantID == "" {
				ownerEvent = event.ID
				break
			}
		}
		if ownerEvent == "" {
			t.Fatal("owner event missing")
		}
		if code, _ = editorRequest("POST", "/undo", map[string]any{"event_id": ownerEvent, "reason": "not mine"}, true); code != 404 {
			t.Fatal("owner event undo allowed", code)
		}
		access, err := services.Sharing.EditorAccess(ctx, viewerPrincipal, grant.ShareID)
		if err != nil {
			t.Fatal(err)
		}
		otherActor := viewerPrincipal
		otherActor.Actor.ID = "different-actor"
		if _, err = services.History.UndoShared(ctx, otherActor, access, history.UndoInput{EventID: published["event_id"].(string), Reason: "not my event"}); !errors.Is(err, principal.ErrForbidden) {
			t.Fatal("other actor undo allowed", err)
		}
		if code, body = editorRequest("POST", "/undo", map[string]any{"event_id": published["event_id"], "reason": "Undo publication"}, true); code != 200 {
			t.Fatal("editor undo publication", code, body)
		}
		if restored, err := services.Dashboard.Get(ctx, p, "spa-shared", "draft"); err != nil || restored.Version != int(newVersion) {
			t.Fatal("editor undo did not restore draft", restored, err)
		}
		if code, _ = editorRequest("POST", "/undo", map[string]any{"event_id": published["event_id"], "reason": "repeat"}, true); code != 400 {
			t.Fatal("repeated undo allowed", code)
		}
		if code, body = editorRequest("POST", "/publish", map[string]any{"version": newVersion, "note": "republish"}, true); code != 200 {
			t.Fatal("editor republish", code, body)
		}
		code, editAgain := editorRequest("POST", "/patch", map[string]any{"ops": []any{map[string]any{"op": "replace", "path": "/title", "value": "Temporary"}}}, true)
		if code != 200 {
			t.Fatal("editor second patch", code, editAgain)
		}
		if code, body = editorRequest("POST", "/undo", map[string]any{"event_id": editAgain["event_id"], "reason": "Undo edit"}, true); code != 200 {
			t.Fatal("editor undo edit", code, body)
		}
		if restored, err := services.Dashboard.Get(ctx, p, "spa-shared", "draft"); err != nil || restored.Spec.Title != "Editor changed" {
			t.Fatal("editor undo did not restore title", restored, err)
		}
		if err := services.Audit.Verify(ctx, p); err != nil {
			t.Fatal("audit chain after shared undo", err)
		}

		events, err := services.Audit.List(ctx, p, audit.ListInput{Tool: "dashboard_shared_read", Target: grant.ShareID, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		foundEditor := false
		for _, event := range events.Events {
			if event.Actor.ID == viewer.ID && strings.Contains(string(event.After), `"mode":"editor"`) {
				foundEditor = true
			}
		}
		if !foundEditor {
			t.Fatal("shared editor read was not audited")
		}

		created, err := services.Dashboard.Create(ctx, p, dashboard.CreateInput{ID: "spa-shared-race", Title: "Before"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = services.Dashboard.Publish(ctx, p, "spa-shared-race", created.Version, ""); err != nil {
			t.Fatal(err)
		}
		raceGrant, err := services.Sharing.Share(ctx, p, sharing.ShareInput{DashboardID: "spa-shared-race", Grantee: viewer.ID, Role: "editor"})
		if err != nil {
			t.Fatal(err)
		}
		original, err := services.Sharing.EditorAccess(ctx, viewerPrincipal, raceGrant.ShareID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = services.History.UndoShared(ctx, viewerPrincipal, original, history.UndoInput{EventID: published["event_id"].(string), Reason: "wrong grant"}); !errors.Is(err, principal.ErrForbidden) {
			t.Fatal("other grant undo allowed", err)
		}
		readOnlyEditor := viewerPrincipal
		readOnlyEditor.Scopes = []string{"ledger:read"}
		readClient := mcpSession(t, ctx, readOnlyEditor, services, true)
		readResult, err := readClient.CallTool(ctx, &mcp.CallToolParams{Name: "dashboard_get", Arguments: map[string]any{"id": raceGrant.ShareID}})
		if err != nil || readResult.IsError {
			t.Fatal("read-only shared editor MCP access", readResult, err)
		}
		mcpClient := mcpSession(t, ctx, viewerPrincipal, services, false)
		sharedPatch, err := mcpClient.CallTool(ctx, &mcp.CallToolParams{Name: "dashboard_patch", Arguments: map[string]any{"dashboard_id": raceGrant.ShareID, "ops": []any{map[string]any{"op": "replace", "path": "/title", "value": "Before"}}}})
		if err != nil || sharedPatch.IsError {
			t.Fatal("shared MCP patch", sharedPatch, err)
		}
		encodedPatch, err := json.Marshal(sharedPatch.StructuredContent)
		if err != nil || strings.Contains(string(encodedPatch), "event_id") {
			t.Fatal("shared MCP patch exposed owner event", string(encodedPatch), err)
		}
		mcpEvents, err := services.Audit.List(ctx, p, audit.ListInput{Tool: "dashboard_patch", Target: "spa-shared-race", Limit: 1})
		if err != nil || len(mcpEvents.Events) != 1 || mcpEvents.Events[0].GrantID != raceGrant.ShareID || mcpEvents.Events[0].GrantHash == "" {
			t.Fatal("MCP grant provenance", mcpEvents, err)
		}
		if _, err = services.Sharing.Share(ctx, p, sharing.ShareInput{DashboardID: "spa-shared-race", Grantee: viewer.ID, Role: "editor"}); err != nil {
			t.Fatal("reissue grant", err)
		}
		if _, err = services.History.UndoShared(ctx, viewerPrincipal, original, history.UndoInput{EventID: mcpEvents.Events[0].ID, Reason: "reissued"}); !errors.Is(err, principal.ErrConflict) {
			t.Fatal("reissued grant undo allowed", err)
		}
		locked, release, started := make(chan struct{}), make(chan struct{}), make(chan struct{}, 2)
		ownerDone := make(chan error, 1)
		go func() {
			ownerDone <- services.Audit.Workflow(ctx, func(tx context.Context) error {
				close(locked)
				<-release
				return services.Sharing.Unshare(tx, p, raceGrant.ShareID)
			})
		}()
		<-locked
		editorDone := make(chan error, 1)
		go func() {
			started <- struct{}{}
			editorDone <- services.Sharing.EditorWrite(ctx, viewerPrincipal, original, func(tx context.Context, current sharing.Access) error {
				_, e := current.Dashboard.Patch(tx, current.Principal, current.ID, json.RawMessage(`[{"op":"replace","path":"/title","value":"Should not write"}]`))
				return e
			})
		}()
		mcpDone := make(chan error, 1)
		go func() {
			started <- struct{}{}
			result, e := mcpClient.CallTool(ctx, &mcp.CallToolParams{Name: "dashboard_patch", Arguments: map[string]any{"dashboard_id": raceGrant.ShareID, "ops": []any{map[string]any{"op": "replace", "path": "/title", "value": "Should not write"}}}})
			if e == nil && result.IsError {
				e = principal.ErrNotFound
			}
			mcpDone <- e
		}()
		<-started
		<-started
		close(release)
		if err := <-ownerDone; err != nil {
			t.Fatal("revoke", err)
		}
		if _, err = services.History.UndoShared(ctx, viewerPrincipal, original, history.UndoInput{EventID: published["event_id"].(string), Reason: "revoked"}); !errors.Is(err, principal.ErrNotFound) {
			t.Fatal("revoked grant undo allowed", err)
		}
		if err := <-editorDone; !errors.Is(err, principal.ErrNotFound) {
			t.Fatal("queued shared edit after revoke", err)
		}
		if err := <-mcpDone; err == nil {
			t.Fatal("queued MCP edit after revoke succeeded")
		}
		unchanged, err := services.Dashboard.Get(ctx, p, "spa-shared-race", "")
		if err != nil || unchanged.Spec.Title != "Before" {
			t.Fatal("revoked queued write changed dashboard", unchanged, err)
		}
		if err = services.Sharing.Unshare(ctx, p, grant.ShareID); err != nil {
			t.Fatal(err)
		}
		if status, _ = fetchShared(); status != 404 {
			t.Fatal("revoked share", status)
		}
		if code, _ := editorRequest("GET", "/draft", nil, false); code != 404 {
			t.Fatal("revoked editor draft", code)
		}
		if code, _ := evaluate("table:shared-allowed"); code != 404 {
			t.Fatal("revoked editor evaluate", code)
		}
	}
	_, err = db.Collection("oauth_grants").InsertMany(ctx, []any{
		bson.M{"_id": "activity-old", "user_id": p.UserID, "client_id": "activity-client", "client_name": "Old name", "created_at": time.Unix(1, 0), "revoked": true, "expires_at": time.Unix(2, 0)},
		bson.M{"_id": "activity-new", "user_id": p.UserID, "client_id": "activity-client", "client_name": "Former agent", "created_at": time.Unix(3, 0), "revoked": true, "expires_at": time.Unix(4, 0)},
		bson.M{"_id": "activity-blank", "user_id": p.UserID, "client_id": "activity-client", "client_name": "", "created_at": time.Unix(5, 0), "revoked": true, "expires_at": time.Unix(6, 0)},
		bson.M{"_id": "activity-expired", "user_id": p.UserID, "client_id": "expired-client", "client_name": "Expired agent", "created_at": time.Unix(4, 0), "revoked": false, "expires_at": time.Unix(6, 0)},
		bson.M{"_id": "activity-other", "user_id": "other-user", "client_id": "private-client", "client_name": "Private name", "created_at": time.Unix(7, 0)},
		bson.M{"_id": "activity-overlap", "user_id": "other-user", "client_id": "activity-client", "client_name": "Other user's name", "created_at": time.Unix(8, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Collection("oauth_grants").DeleteMany(context.Background(), bson.M{"_id": bson.M{"$in": []string{"activity-old", "activity-new", "activity-blank", "activity-expired", "activity-other", "activity-overlap"}}})
	})
	beforeImports, err := services.Imports.StagedCount(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	beforeDiscrepancies, err := services.Reconcile.OpenCount(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	batches := make([]any, 0, 105)
	for i := 0; i < 102; i++ {
		batches = append(batches, bson.M{"_id": fmt.Sprintf("navigation-count-%d", i), "user_id": p.UserID, "status": "ready"})
	}
	batches = append(batches, bson.M{"_id": "navigation-staging", "user_id": p.UserID, "status": "staging"}, bson.M{"_id": "navigation-committing", "user_id": p.UserID, "status": "committing"}, bson.M{"_id": "navigation-committed", "user_id": p.UserID, "status": "committed"}, bson.M{"_id": "navigation-other", "user_id": "other-user", "status": "ready"})
	if _, err = db.Collection("import_batches").InsertMany(ctx, batches); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Collection("discrepancies").InsertMany(ctx, []any{bson.M{"_id": "navigation-open", "user_id": p.UserID, "status": "open"}, bson.M{"_id": "navigation-archived", "user_id": p.UserID, "status": "open", "archived": true}, bson.M{"_id": "navigation-resolved", "user_id": p.UserID, "status": "resolved"}, bson.M{"_id": "navigation-other", "user_id": "other-user", "status": "open"}}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int64{"/api/imports/count": beforeImports + 104, "/api/reconcile/count": beforeDiscrepancies + 1} {
		status, body, _ := request("GET", path, nil, true, false, true)
		if status != 200 || body["count"] != float64(want) {
			t.Fatalf("%s: %d %v, want %d", path, status, body, want)
		}
	}
	for _, collection := range []string{"import_batches", "discrepancies"} {
		if _, err = db.Collection(collection).DeleteMany(ctx, bson.M{"_id": bson.M{"$regex": "^navigation-"}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/api/session", "/api/imports", "/api/imports/count", "/api/reconcile/count", "/api/settings", "/api/activity/agents", "/api/expr/catalog"} {
		status, _, headers := request("GET", path, nil, false, false, true)
		if status != 401 || headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s unauthenticated: %d", path, status)
		}
		status, body, headers := request("GET", path, nil, true, false, true)
		if status != 200 || headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %v", path, status, body)
		}
		switch path {
		case "/api/session":
			if body["csrf"] != session.CSRF || body["mcp_url"] != "/api/mcp" || body["version"] != "integration" || body["user"].(map[string]any)["id"] != p.UserID {
				t.Fatalf("session response: %v", body)
			}
		case "/api/imports":
			if _, ok := body["batches"]; !ok {
				t.Fatalf("imports: %v", body)
			}
		case "/api/imports/count", "/api/reconcile/count":
			if _, ok := body["count"].(float64); !ok {
				t.Fatalf("count response: %v", body)
			}
		case "/api/settings":
			if _, ok := body["agents"]; !ok {
				t.Fatalf("settings: %v", body)
			}
			if _, ok := body["feeds"]; !ok {
				t.Fatalf("settings: %v", body)
			}
			for _, item := range body["agents"].([]any) {
				if agent := item.(map[string]any); agent["client_id"] == "activity-client" || agent["client_id"] == "expired-client" {
					t.Fatalf("inactive agent in settings: %v", agent)
				}
			}
		case "/api/activity/agents":
			found, expired := false, false
			for _, item := range body["agents"].([]any) {
				agent := item.(map[string]any)
				if len(agent) != 2 || agent["client_id"] == "private-client" {
					t.Fatalf("activity agent leak: %v", agent)
				}
				if agent["client_id"] == "activity-client" && agent["client_name"] == "Former agent" {
					found = true
				}
				if agent["client_id"] == "expired-client" && agent["client_name"] == "Expired agent" {
					expired = true
				}
			}
			if !found || !expired {
				t.Fatalf("historical agent missing: %v", body)
			}
		case "/api/expr/catalog":
			if len(body["functions"].([]any)) == 0 || len(body["aggregates"].([]any)) == 0 {
				t.Fatalf("catalog functions: %v", body)
			}
			found := map[string]bool{}
			for _, item := range body["sources"].([]any) {
				source := item.(map[string]any)
				name := source["name"].(string)
				fields := source["fields"].(map[string]any)
				if name == "postings" && fields["value_eur"] == "double" {
					found[name] = true
				}
				if name == "table:catalog-test" && fields["amount"] == "double" {
					found[name] = true
				}
				if name == "view:catalog-test" && fields["amount"] == "double" && fields["doubled"] == "dyn" {
					found[name] = true
				}
			}
			if len(found) != 3 {
				t.Fatalf("catalog sources: %v", found)
			}
		}
	}
	for _, path := range []string{"/settings/invite", "/settings/revoke", "/settings/password", "/settings/feed"} {
		status, _, _ := request("POST", path, nil, false, false, true)
		if status != 401 {
			t.Fatalf("%s unauthenticated: %d", path, status)
		}
		status, _, _ = request("POST", path, nil, true, false, true)
		if status != 403 {
			t.Fatalf("%s csrf: %d", path, status)
		}
	}
	form := url.Values{"email": {"api-invited@example.test"}}
	status, body, _ := request("POST", "/settings/invite", form, true, true, true)
	if status != 200 || !strings.HasPrefix(body["invitation_url"].(string), "http://localhost:6459/register?token=") {
		t.Fatalf("JSON invite: %d %v", status, body)
	}
	status, _, headers := request("POST", "/settings/invite", url.Values{"email": {"form-invited@example.test"}}, true, true, false)
	if status != 200 || !strings.HasPrefix(headers.Get("Content-Type"), "text/html") {
		t.Fatalf("form invite: %d", status)
	}
	status, body, _ = request("POST", "/settings/feed", nil, true, true, true)
	if status != 400 || body["error"] == nil {
		t.Fatalf("feed validation: %d %v", status, body)
	}
	check := func(payload string, signed, csrf bool) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/api/expr/check", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if signed {
			req.AddCookie(&http.Cookie{Name: "moneybag_session", Value: raw})
		}
		if csrf {
			req.Header.Set("X-CSRF-Token", session.CSRF)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var result map[string]any
		if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, result
	}
	payload := `{"expr":"1 / 0", "role":"value", "from":"postings"}`
	if status, _ := check(payload, false, false); status != 401 {
		t.Fatalf("expression auth: %d", status)
	}
	if status, _ := check(payload, true, false); status != 403 {
		t.Fatalf("expression csrf: %d", status)
	}
	if status, result := check(payload, true, true); status != 200 || result["ok"] != true {
		t.Fatalf("expression compile: %d %v", status, result)
	}
	if status, result := check(`{"expr":"sum(missing)","role":"agg"}`, true, true); status != 200 || result["ok"] != false || len(result["issues"].([]any)) == 0 {
		t.Fatalf("expression issue: %d %v", status, result)
	}
	if status, _ := check(`{"expr":"1","role":"unknown"}`, true, true); status != 400 {
		t.Fatalf("expression role: %d", status)
	}
}
