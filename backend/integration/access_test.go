package integration

import (
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/components/preview"
)

func newAccessHarness(t *testing.T) (*readHarness, *atomic.Int32) {
	t.Helper()
	var mode atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if r.URL.Path != "/api/access/check" || !ok || id != "app_eval_moneybag" || secret != "eval-confidential-fixture" {
			http.Error(w, "invalid client", 401)
			return
		}
		var input struct {
			UserID string `json:"user_id"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.UserID != "usr_eval_moneybag" {
			http.Error(w, "invalid subject", 422)
			return
		}
		state := mode.Load()
		if state == 2 {
			http.Error(w, "owned policy unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		reason := "group_grant"
		if state == 1 {
			reason = "user_block"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": id, "subject": input.UserID, "allowed": state == 0, "reason_code": reason, "policy_revision": 1, "lease_seconds": 60})
	}))
	t.Cleanup(provider.Close)
	h := newReadHarnessMode(t, "_access", nil, true, func(h *readHarness) {
		u, err := h.services.Identity.User(h.ctx, h.owner.UserID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.services.Identity.LinkIdentity(h.ctx, "usr_eval_moneybag", u.Email); err != nil {
			t.Fatal(err)
		}
		if err = h.services.Identity.ConfigureAccess(provider.URL, "app_eval_moneybag", "eval-confidential-fixture"); err != nil {
			t.Fatal(err)
		}
		h.services.Previews.WithAccessGuard(h.services.Identity.WatchAccess)
		h.services.Dashboard.WithAccessGuard(h.services.Identity.WatchAccess)
	})
	return h, &mode
}

func TestAccessCookiesOAuthAndIndependentPrivateRenderCredentials(t *testing.T) {
	h, mode := newAccessHarness(t)
	client := &http.Client{Transport: h.http.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, body, token, cookie string, render bool) int {
		t.Helper()
		r, err := http.NewRequestWithContext(h.ctx, method, h.endpoint+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://public.example.test")
		r.Header.Set("X-CSRF-Token", h.csrf)
		if render {
			r.AddCookie(&http.Cookie{Name: "moneybag_render", Value: cookie})
			r.Header.Set("X-CSRF-Token", "render")
		} else if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "__Host-moneybag_session", Value: cookie})
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	const origin = "https://public.example.test"
	oa := oauth.NewService(oauth.NewPostgresRepository(h.db), h.services.Identity, origin)
	registered, err := oa.Register(h.ctx, oauth.RegisterInput{Name: "eval agent", RedirectURIs: []string{"http://127.0.0.1:8787/callback"}, AuthMethod: "none"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	authorization := oauth.Authorization{ClientID: registered.ID, RedirectURI: registered.RedirectURIs[0], ResponseType: "code", Scope: "ledger:read", Challenge: oauth.Challenge(verifier), ChallengeMethod: "S256", Resource: origin + "/mcp"}
	code, err := oa.Authorize(h.ctx, authorization, h.owner)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := oa.Exchange(h.ctx, registered.Client, code, authorization.RedirectURI, verifier, authorization.Resource)
	if err != nil {
		t.Fatal(err)
	}
	routeToken, err := h.services.Previews.Issue(h.owner, "", preview.Target{Route: "/budget"})
	if err != nil {
		t.Fatal(err)
	}
	dashToken, err := h.services.Previews.Issue(h.owner, "", preview.Target{Dashboard: "owned-dashboard"})
	if err != nil {
		t.Fatal(err)
	}
	if got := request("GET", "/api/session", "", "", h.cookie, false); got != 200 {
		t.Fatal("eligible cookie failed", got)
	}
	if got := request("GET", "/render/"+dashToken, "", "", "", false); got != 200 {
		t.Fatal("eligible private render shell failed", got)
	}
	for _, state := range []int32{1, 2} {
		mode.Store(state)
		want := 403
		if state == 2 {
			want = 503
		}
		for _, path := range []string{"/api/session", "/api/saved-views", "/api/expr/catalog", "/api/settings", "/api/changes", "/api/shared/owned-share", "/oauth/authorize"} {
			if got := request("GET", path, "", "", h.cookie, false); got != want {
				t.Fatal("cookie boundary bypassed central policy", path, got)
			}
		}
		for _, path := range []string{"/render/" + dashToken, "/render/" + routeToken + "/page", "/api/render/" + dashToken + "/view", "/api/render/" + dashToken + "/cube"} {
			if got := request("GET", path, "", "", "", false); got != want {
				t.Fatal("private token bypassed central policy", path, got)
			}
		}
		if got := request("POST", "/api/render/"+dashToken+"/report", `{}`, "", "", false); got != want {
			t.Fatal("render report bypassed central policy", got)
		}
		if got := request("POST", "/api/mcp", mcpCall("whoami"), "", routeToken, true); got != want {
			t.Fatal("render MCP bypassed central policy", got)
		}
		if got := request("POST", "/mcp", mcpCall("whoami"), tokens.AccessToken, "", false); got != want {
			t.Fatal("independent OAuth bypassed central policy", got)
		}
		form := url.Values{"grant_type": {"refresh_token"}, "client_id": {registered.ID}, "refresh_token": {tokens.RefreshToken}}.Encode()
		if got := request("POST", "/oauth/token", form, "", "", false); got != want {
			t.Fatal("refresh bypassed central policy", got)
		}
	}
	mode.Store(0)
	if got := request("GET", "/api/session", "", "", h.cookie, false); got != 200 {
		t.Fatal("restored cookie failed", got)
	}
	if _, err := oa.Refresh(h.ctx, registered.Client, tokens.RefreshToken, "", ""); err != nil {
		t.Fatal("denial consumed a valid refresh", err)
	}
	mode.Store(1)
	if err = h.services.Identity.CheckAccess(h.ctx, h.owner.UserID, true); !errors.Is(err, identity.ErrAccessDenied) {
		t.Fatal("block fixture failed")
	}
	// SDK logout is independently mounted in the actual-provider suite. The old
	// local logout route must still revoke this cookie while access is blocked.
	form := url.Values{"csrf": {h.csrf}}.Encode()
	if got := request("POST", "/logout", form, "", h.cookie, false); got != 303 {
		t.Fatal("blocked local logout failed", got)
	}
}

func TestScheduledShareSnapshotRetainsItsStateWhileOwnerIsBlocked(t *testing.T) {
	h, mode := newAccessHarness(t)
	spec := dashboard.Spec{ID: "eval-access-share", Title: "eval share", Version: 1, Data: map[string]dashboard.Query{"rows": {From: "journal"}}, Pages: []dashboard.Page{{ID: "main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 12, Component: "count"}}}}}}, Components: []dashboard.Component{{ID: "count", Type: "kpi", Title: "Rows", Value: "size(data.rows)", Format: "int"}}}
	h.publish(t, spec)
	invitation, err := h.services.Identity.Invite(h.ctx, h.owner, "eval-grantee@example.test")
	if err != nil {
		t.Fatal(err)
	}
	grantee, err := h.services.Identity.Register(h.ctx, invitation, "eval grantee", "synthetic-test-password")
	if err != nil {
		t.Fatal(err)
	}
	shared, err := h.services.Sharing.Share(h.ctx, h.owner, sharing.ShareInput{DashboardID: spec.ID, Grantee: grantee.ID, Role: "viewer", Mode: "snapshot", Refresh: "hourly"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(h.ctx, `UPDATE shares SET last_refresh=date_trunc('milliseconds',now()-interval '2 hour') WHERE id=@id`, pgx.NamedArgs{"id": shared.ShareID}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var value string
		if err = h.db.QueryRow(h.ctx, `SELECT jsonb_build_object('share',(SELECT to_jsonb(s) FROM shares s WHERE id=@id),'snapshot',(SELECT to_jsonb(s) FROM dashboard_snapshots s WHERE id=@id))::text`, pgx.NamedArgs{"id": shared.ShareID}).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	mode.Store(1)
	if err = h.services.Sharing.RefreshDue(h.ctx); !errors.Is(err, identity.ErrAccessDenied) {
		t.Fatal("scheduled owner did not pause", err)
	}
	if snapshot() != before {
		t.Fatal("denied snapshot mutated a grant, timestamp or financial output")
	}
	mode.Store(0)
	if err = h.services.Sharing.RefreshDue(h.ctx); err != nil {
		t.Fatal("restored scheduled snapshot failed", err)
	}
	if snapshot() == before {
		t.Fatal("restored owner snapshot did not resume")
	}
}
