package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/admin"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/feedback"
	"github.com/olegpustovit/moneybag/backend/internal/components/history"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
)

func undoInput(id string) history.UndoInput {
	return history.UndoInput{EventID: id, Reason: "Eval check"}
}

func auditListFor(tool, target string) audit.ListInput {
	return audit.ListInput{Tool: tool, Target: target}
}

// withWrite is the owner's other connection; undo needs ledger:write and the admin scope together.
func withWrite(p principal.Principal) principal.Principal {
	p.Scopes = append(append([]string{}, p.Scopes...), "ledger:write")
	return p
}

type browserSession struct {
	t        *testing.T
	ctx      context.Context
	endpoint string
	raw      string
	csrf     string
	client   *http.Client
}

func newBrowser(t *testing.T, ctx context.Context, endpoint string, s mcpserver.Services, u identity.User) browserSession {
	t.Helper()
	raw, session, e := s.Identity.StartSession(ctx, u)
	if e != nil {
		t.Fatal(e)
	}
	return browserSession{t, ctx, endpoint, raw, session.CSRF, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b browserSession) do(method, path, body string, headers map[string]string) (int, string, http.Header) {
	b.t.Helper()
	req, e := http.NewRequestWithContext(b.ctx, method, b.endpoint+path, strings.NewReader(body))
	if e != nil {
		b.t.Fatal(e)
	}
	if b.raw != "" {
		req.AddCookie(&http.Cookie{Name: "moneybag_session", Value: b.raw})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, e := b.client.Do(req)
	if e != nil {
		b.t.Fatal(e)
	}
	defer res.Body.Close()
	data, e := io.ReadAll(res.Body)
	if e != nil {
		b.t.Fatal(e)
	}
	return res.StatusCode, string(data), res.Header
}

func (b browserSession) signed(method, path, body string) (int, string) {
	b.t.Helper()
	status, data, _ := b.do(method, path, body, map[string]string{"X-CSRF-Token": b.csrf, "Content-Type": "application/json"})
	return status, data
}

func (b browserSession) form(path string, form url.Values) (int, string) {
	b.t.Helper()
	form.Set("csrf", b.csrf)
	status, data, _ := b.do("POST", path, form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"})
	return status, data
}

func testTelemetryHTTP(t *testing.T, ctx context.Context, endpoint string, oa *oauth.Service, owner principal.Principal, s mcpserver.Services) {
	t.Helper()
	ownerUser, e := s.Identity.User(ctx, owner.UserID)
	if e != nil {
		t.Fatal(e)
	}
	browser := newBrowser(t, ctx, endpoint, s, ownerUser)
	anonymous := browserSession{t, ctx, endpoint, "", "", browser.client}
	token, e := s.Identity.Invite(ctx, owner, "telemetry-member@example.test")
	if e != nil {
		t.Fatal(e)
	}
	memberUser, e := s.Identity.Register(ctx, token, "Member", "synthetic-test-password")
	if e != nil {
		t.Fatal(e)
	}
	member := newBrowser(t, ctx, endpoint, s, memberUser)

	t.Run("cube", func(t *testing.T) {
		if status, _, _ := anonymous.do("GET", "/api/cube", "", nil); status != 401 {
			t.Fatal("anonymous cube", status)
		}
		status, body, header := browser.do("GET", "/api/cube", "", nil)
		if status != 200 || header.Get("ETag") == "" {
			t.Fatal(status, body)
		}
		var cube map[string]any
		if e := json.Unmarshal([]byte(body), &cube); e != nil {
			t.Fatal(e)
		}
		if cube["base"] != "EUR" {
			t.Fatal("cube base", cube["base"])
		}
		for _, key := range []string{"accounts", "payees", "tag_sets", "flows", "balances", "rates", "metrics", "annotations", "labels", "start", "end", "as_of", "revision"} {
			if _, ok := cube[key]; !ok {
				t.Error("cube misses", key)
			}
		}
		flows := cube["flows"].(map[string]any)
		if len(flows["d"].([]any)) == 0 || len(flows["d"].([]any)) != len(flows["v"].([]any)) {
			t.Fatal("flows", flows)
		}
		if status, body, _ = browser.do("GET", "/api/cube", "", map[string]string{"If-None-Match": header.Get("ETag")}); status != 304 || body != "" {
			t.Fatal("cube should revalidate", status, body)
		}
		// Another user's revision and identity make another tag, so a shared cache can never cross users.
		if _, _, other := member.do("GET", "/api/cube", "", nil); other.Get("ETag") == header.Get("ETag") {
			t.Fatal("two users share an ETag")
		}
	})

	t.Run("changes", func(t *testing.T) {
		status, body, header := browser.do("GET", "/api/changes", "", nil)
		if status != 200 || header.Get("ETag") == "" {
			t.Fatal(status, body)
		}
		var page struct {
			Groups []struct {
				Summary string `json:"summary"`
				Actor   struct {
					Kind  string `json:"kind"`
					Label string `json:"label"`
				} `json:"actor"`
				Events []map[string]any `json:"events"`
			} `json:"groups"`
		}
		if e := json.Unmarshal([]byte(body), &page); e != nil {
			t.Fatal(e)
		}
		if len(page.Groups) == 0 || page.Groups[0].Summary == "" || page.Groups[0].Actor.Label == "" || page.Groups[0].Actor.Label == owner.UserID {
			t.Fatal(body[:min(len(body), 500)])
		}
		if strings.Contains(body, `"before"`) || strings.Contains(body, `"after"`) {
			t.Fatal("the feed must not carry payloads")
		}
		if status, _, _ = browser.do("GET", "/api/changes?cursor=abc", "", nil); status != 400 {
			t.Fatal("bad cursor", status)
		}
	})

	t.Run("flags, usage and saved views", func(t *testing.T) {
		flag := `{"target":{"dashboard":"fixed","component":"c","metric":"m","value":"€1.00"},"note":"Looks off"}`
		if status, _, _ := browser.do("POST", "/api/feedback", flag, map[string]string{"Content-Type": "application/json"}); status != 403 {
			t.Fatal("flag without CSRF", status)
		}
		if status, body := browser.signed("POST", "/api/feedback", flag); status != 201 || !strings.Contains(body, `"status":"open"`) {
			t.Fatal(status, body)
		}
		if status, _ := browser.signed("POST", "/api/feedback", `{"note":"`+strings.Repeat("x", 3000)+`"}`); status != 400 {
			t.Fatal("oversized flag", status)
		}
		revision, e := s.Audit.Revision(ctx, owner)
		if e != nil {
			t.Fatal(e)
		}
		if status, body := browser.signed("POST", "/api/usage", `{"path":"/dashboards/fixed?month=2026-01","dashboard":"fixed"}`); status != 204 {
			t.Fatal(status, body)
		}
		if status, _ := browser.signed("POST", "/api/usage", `{"path":"https://elsewhere.example"}`); status != 400 {
			t.Fatal("foreign path", status)
		}
		if after, _ := s.Audit.Revision(ctx, owner); after != revision {
			t.Fatal("a usage beacon must not invalidate cached views")
		}
		report, e := s.Feedback.Usage(ctx, owner, feedback.UsageInput{})
		if e != nil || len(report.Pages) == 0 || report.Pages[0].Path != "/dashboards/fixed" {
			t.Fatal(report, e)
		}
		if status, _ := browser.signed("PUT", "/api/saved-views", `{"name":"","page":"/"}`); status != 400 {
			t.Fatal("nameless view", status)
		}
		status, body := browser.signed("PUT", "/api/saved-views", `{"name":"Lisbon spending","page":"/spending","state":{"from":"2026-05-01"}}`)
		if status != 200 {
			t.Fatal(status, body)
		}
		var saved feedback.SavedView
		if e := json.Unmarshal([]byte(body), &saved); e != nil || saved.ID == "" || !strings.Contains(string(saved.State), "2026-05-01") {
			t.Fatal(body, e)
		}
		if _, body, _ = browser.do("GET", "/api/saved-views", "", nil); !strings.Contains(body, "Lisbon spending") {
			t.Fatal(body)
		}
		if _, body, _ = member.do("GET", "/api/saved-views", "", nil); strings.Contains(body, "Lisbon spending") {
			t.Fatal("saved view crossed users", body)
		}
		if status, _ = member.signed("DELETE", "/api/saved-views?id="+saved.ID, ""); status != 404 {
			t.Fatal("another user deleted a view", status)
		}
		if status, _ = browser.signed("DELETE", "/api/saved-views?id="+saved.ID, ""); status != 200 {
			t.Fatal("delete", status)
		}
	})

	reg, e := oa.Register(ctx, oauth.RegisterInput{Name: "Telemetry agent", RedirectURIs: []string{"http://127.0.0.1:8484/callback"}, AuthMethod: "none"})
	if e != nil {
		t.Fatal(e)
	}
	resource := oa.Issuer + "/mcp"
	connect := func(scope string) oauth.TokenResponse {
		t.Helper()
		verifier := identity.Random()
		auth := oauth.Authorization{ClientID: reg.ID, RedirectURI: reg.RedirectURIs[0], ResponseType: "code", Scope: scope, State: "synthetic-state", Challenge: oauth.Challenge(verifier), ChallengeMethod: "S256", Resource: resource}
		code, e := oa.Authorize(ctx, auth, owner)
		if e != nil {
			t.Fatal(e)
		}
		tokens, e := oa.Exchange(ctx, reg.Client, code, auth.RedirectURI, verifier, resource)
		if e != nil {
			t.Fatal(e)
		}
		return tokens
	}

	t.Run("admin:users cannot be asked for at consent", func(t *testing.T) {
		auth := oauth.Authorization{ClientID: reg.ID, RedirectURI: reg.RedirectURIs[0], ResponseType: "code", Scope: "ledger:read admin:users", State: "s", Challenge: oauth.Challenge(identity.Random()), ChallengeMethod: "S256", Resource: resource}
		if _, _, e := oa.Validate(ctx, auth); !errors.Is(e, oauth.ErrInvalidScope) {
			t.Fatal("consent offered admin:users", e)
		}
	})

	t.Run("escalation needs the owner's page for this exact action", func(t *testing.T) {
		tokens := connect("ledger:read")
		agent, e := oa.Bearer(ctx, tokens.AccessToken, resource)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Admin.Users(ctx, agent); !errors.Is(e, principal.ErrForbidden) {
			t.Fatal("agent without the scope listed users", e)
		}
		pending, e := s.Admin.RequestAdmin(ctx, agent, admin.RequestInput{Reason: "Tidy up old accounts"})
		if e != nil || pending.Status != "needs_confirmation" {
			t.Fatal(pending, e)
		}
		link, e := url.Parse(pending.ConfirmURL)
		if e != nil {
			t.Fatal(e)
		}
		if status, _, header := anonymous.do("GET", link.Path, "", nil); status != 303 || !strings.HasPrefix(header.Get("Location"), "/login") {
			t.Fatal("confirmation page needs sign-in", status)
		}
		if status, _, _ := member.do("GET", link.Path, "", nil); status != 404 {
			t.Fatal("a non-owner saw the request", status)
		}
		status, page, _ := browser.do("GET", link.Path, "", nil)
		if status != 200 || !strings.Contains(page, "Telemetry agent") || !strings.Contains(page, "Tidy up old accounts") {
			t.Fatal(status, page)
		}
		if status, _ = member.form(link.Path, url.Values{"decision": {"allow"}}); status != 404 {
			t.Fatal("a non-owner approved", status)
		}
		if status, _, _ = browser.do("POST", link.Path, "decision=allow", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); status != 403 {
			t.Fatal("approval without CSRF", status)
		}
		if agent, _ = oa.Bearer(ctx, tokens.AccessToken, resource); agent.Require(principal.ScopeAdminUsers) == nil {
			t.Fatal("scope granted before approval")
		}
		if status, page = browser.form(link.Path, url.Values{"decision": {"allow"}}); status != 200 || !strings.Contains(page, "Done") {
			t.Fatal(status, page)
		}
		if agent, e = oa.Bearer(ctx, tokens.AccessToken, resource); e != nil || agent.Require(principal.ScopeAdminUsers) != nil || !agent.Admin {
			t.Fatal("approval did not reach the connection", agent, e)
		}
		if status, _ = browser.form(link.Path, url.Values{"decision": {"allow"}}); status != 404 {
			t.Fatal("the link replayed", status)
		}
		users, e := s.Admin.Users(ctx, agent)
		if e != nil || len(users) < 2 {
			t.Fatal(users, e)
		}
		if raw, _ := json.Marshal(users); strings.Contains(string(raw), "hash") || strings.Contains(string(raw), "salt") {
			t.Fatal("password material in user_list", string(raw))
		}

		t.Run("and an agent with the scope runs people", func(t *testing.T) {
			invite, e := s.Admin.Invite(ctx, agent, admin.InviteInput{Email: "telemetry-new@example.test", Reason: "New partner"})
			if e != nil || !strings.Contains(invite.InvitationURL, "/register?token=") {
				t.Fatal(invite, e)
			}
			if _, e = s.Admin.Invite(ctx, agent, admin.InviteInput{Email: "telemetry-noreason@example.test"}); e == nil {
				t.Fatal("invite without a reason")
			}
			// An ordinary user goes at once, and takes their sessions with them.
			done, e := s.Admin.DisableUser(ctx, agent, admin.DisableInput{UserID: memberUser.ID, Reason: "Left the household"})
			if e != nil || done.Status != "disabled" {
				t.Fatal(done, e)
			}
			if status, _, _ := member.do("GET", "/api/cube", "", nil); status != 401 {
				t.Fatal("disabled user still has a session", status)
			}
			if _, e = s.Identity.Login(ctx, memberUser.Email, "synthetic-test-password"); !errors.Is(e, identity.ErrCredentials) {
				t.Fatal("disabled user signed in", e)
			}
			events, e := s.Audit.List(ctx, agent, auditListFor("user_disable", memberUser.ID))
			if e != nil || len(events.Events) != 1 || events.Events[0].Reason != "Left the household" || !strings.Contains(string(events.Events[0].After), "admin:users") || events.Events[0].Actor.Kind != "agent" {
				t.Fatal("disable must record agent, user, scopes and reason", events, e)
			}
			if _, e = s.History.Undo(ctx, withWrite(agent), undoInput(events.Events[0].ID)); e != nil {
				t.Fatal(e)
			}
			if _, e = s.Identity.Login(ctx, memberUser.Email, "synthetic-test-password"); e != nil {
				t.Fatal("undo did not re-enable", e)
			}
			member = newBrowser(t, ctx, endpoint, s, memberUser) // Disabling ended the old session for good.
			// The owner needs the page.
			pending, e := s.Admin.DisableUser(ctx, agent, admin.DisableInput{UserID: owner.UserID, Reason: "Hostile takeover"})
			if e != nil || pending.Status != "needs_confirmation" || pending.ConfirmURL == "" {
				t.Fatal(pending, e)
			}
			if _, e = s.Identity.Login(ctx, ownerUser.Email, "synthetic-test-password"); e != nil {
				t.Fatal("owner disabled without confirmation", e)
			}
			link, _ := url.Parse(pending.ConfirmURL)
			if status, page := browser.form(link.Path, url.Values{"decision": {"deny"}}); status != 200 || !strings.Contains(page, "Declined") {
				t.Fatal(status, page)
			}
			if _, e = s.Identity.Login(ctx, ownerUser.Email, "synthetic-test-password"); e != nil {
				t.Fatal("declining disabled the owner", e)
			}
			grants, e := s.Admin.Agents(ctx, agent)
			if e != nil || len(grants) == 0 {
				t.Fatal(grants, e)
			}
			extra := connect("ledger:read")
			held, e := oa.Bearer(ctx, extra.AccessToken, resource)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Admin.RevokeAgent(ctx, agent, admin.RevokeInput{ID: held.GrantID, Reason: "No longer needed"}); e != nil {
				t.Fatal(e)
			}
			if _, e = oa.Bearer(ctx, extra.AccessToken, resource); e == nil {
				t.Fatal("revoked agent still works")
			}
		})
	})

	t.Run("the emergency stop suspends every agent until re-enabled", func(t *testing.T) {
		tokens := connect("ledger:read")
		call := func() (int, string) {
			t.Helper()
			req, e := http.NewRequestWithContext(ctx, "POST", endpoint+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
			if e != nil {
				t.Fatal(e)
			}
			req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			res, e := http.DefaultClient.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			return res.StatusCode, string(body)
		}
		if status, body := call(); status != 200 || !strings.Contains(body, "entries_list") || strings.Contains(body, "entry_post") {
			t.Fatal("tools/list over HTTP should follow the grant", status, body[:min(len(body), 300)])
		}
		if status, _ := member.form("/settings/agents/stop", url.Values{}); status != 403 {
			t.Fatal("a non-owner used the emergency stop", status)
		}
		if status, _, _ := browser.do("POST", "/settings/agents/stop", "", map[string]string{"Accept": "application/json"}); status != 403 {
			t.Fatal("stop without CSRF", status)
		}
		if status, body := browser.form("/settings/agents/stop", url.Values{}); status != 200 || !strings.Contains(body, "true") {
			t.Fatal(status, body)
		}
		status, body := call()
		if status != 403 || !strings.Contains(body, "agents_suspended") || !strings.Contains(body, "re-enable") {
			t.Fatal("suspended agent got no clear error", status, body)
		}
		if _, e := oa.Refresh(ctx, reg.Client, tokens.RefreshToken, resource, ""); !errors.Is(e, oauth.ErrAgentsSuspended) {
			t.Fatal("a suspended agent refreshed", e)
		}
		if _, settings, _ := browser.do("GET", "/api/settings", "", nil); !strings.Contains(settings, `"agents_suspended":true`) {
			t.Fatal(settings)
		}
		if status, body = browser.form("/settings/agents/resume", url.Values{}); status != 200 || !strings.Contains(body, "false") {
			t.Fatal(status, body)
		}
		if status, _ = call(); status != 200 {
			t.Fatal("re-enabling did not restore the grant", status)
		}
	})

	t.Run("feed setup keeps secrets away from agents", func(t *testing.T) {
		result, e := s.Imports.ConfigureFeed(ctx, owner, imports.FeedInput{ID: "wise", URL: "https://feed.example.test/statement", Account: "assets:bank", Format: "wise_csv"})
		if e != nil {
			t.Fatal(e)
		}
		if s.Imports.HasFeedSecret(owner, result) {
			t.Fatal("a new feed has no secret")
		}
		if status, page, _ := browser.do("GET", "/secure/feed/wise", "", nil); status != 200 || !strings.Contains(page, `type="password"`) {
			t.Fatal(status, page)
		}
		if status, _, _ := anonymous.do("GET", "/secure/feed/wise", "", nil); status != 303 {
			t.Fatal("secure page needs sign-in", status)
		}
		if status, _ := browser.form("/secure/feed/wise", url.Values{"token": {"synthetic-bank-secret"}}); status != 303 {
			t.Fatal("save secret", status)
		}
		feeds, e := s.Imports.Feeds(ctx, owner)
		if e != nil {
			t.Fatal(e)
		}
		saved := false
		for _, feed := range feeds {
			saved = saved || feed.ID == "wise" && s.Imports.HasFeedSecret(owner, feed)
		}
		if !saved {
			t.Fatal("the secret was not saved")
		}
		raw, _ := json.Marshal(feeds)
		if strings.Contains(string(raw), "synthetic-bank-secret") || strings.Contains(string(raw), "token") {
			t.Fatal("secret leaked", string(raw))
		}
	})
}
