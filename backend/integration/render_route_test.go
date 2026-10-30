package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/preview"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
)

func (h *readHarness) renderReq(t *testing.T, method, path, body string, cookies ...*http.Cookie) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(h.ctx, method, h.endpoint+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	req.Header.Set("Origin", "https://public.example.test")
	req.Header.Set("X-CSRF-Token", "render") // the fixed value: even a correct CSRF must not unlock writes
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", mcpName(body))
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(data), res.Header
}

func mcpName(body string) string {
	if strings.Contains(body, "entry_post") {
		return "entry_post"
	}
	return "whoami"
}

func mcpCall(name string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"t","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
}

func (h *readHarness) docCounts(t *testing.T) map[string]int64 {
	t.Helper()
	names, err := h.db.ListCollectionNames(h.ctx, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, name := range names {
		if out[name], err = h.db.Collection(name).CountDocuments(h.ctx, map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestRouteRenderIsReadOnly(t *testing.T) {
	h := newReadHarness(t, "_route_render", nil)
	ctx := context.Background()
	token, err := h.services.Previews.Issue(h.owner, "", preview.Target{Route: "/budget", Theme: "dark"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "moneybag_render", Value: token}

	// The capture URL sets the cookie and lands on the route with the theme.
	status, _, header := h.renderReq(t, "GET", "/render/"+token+"/page", "")
	set := header.Get("Set-Cookie")
	if status != 302 || header.Get("Location") != "/budget?theme=dark" || !strings.Contains(set, "moneybag_render="+token) || !strings.Contains(set, "HttpOnly") || !strings.Contains(set, "SameSite=Strict") || !strings.Contains(set, "Max-Age=60") {
		t.Fatal("capture redirect", status, header)
	}

	// Reads work and carry the token; a normal session never sees it and never shares the render bytes.
	status, body, _ := h.renderReq(t, "GET", "/api/bootstrap", "", cookie)
	if status != 200 || !strings.Contains(body, `"render_token":"`+token+`"`) {
		t.Fatal("render bootstrap", status, body)
	}
	if status, body, _ = h.renderReq(t, "GET", "/api/bootstrap", "", &http.Cookie{Name: "moneybag_session", Value: h.cookie}); status != 200 || strings.Contains(body, "render_token") || !strings.Contains(body, h.csrf) {
		t.Fatal("normal bootstrap", status, body)
	}
	if status, body, _ = h.renderReq(t, "GET", "/api/bootstrap", "", cookie); !strings.Contains(body, "render_token") {
		t.Fatal("render bootstrap after a normal one was cached", status, body)
	}
	if status, body, _ = h.renderReq(t, "GET", "/api/bootstrap", "", cookie, &http.Cookie{Name: "moneybag_session", Value: h.cookie}); strings.Contains(body, "render_token") {
		t.Fatal("a normal session must win", status)
	}
	if status, body, _ = h.renderReq(t, "POST", "/api/mcp", mcpCall("whoami"), cookie); status != 200 || strings.Contains(body, `"isError":true`) || !strings.Contains(body, "ledger:read") || strings.Contains(body, "ledger:write") {
		t.Fatal("render mcp read", status, body)
	}
	for _, path := range []string{"/api/session", "/api/cube", "/api/changes"} {
		if status, body, _ = h.renderReq(t, "GET", path, "", cookie); status != 200 {
			t.Fatal(path, status, body)
		}
	}

	// Every mutating route is refused and writes nothing.
	before := h.docCounts(t)
	mutating := []string{
		"POST /api/feedback", "POST /api/usage", "PUT /api/saved-views", "DELETE /api/saved-views", "POST /api/mcp", "POST /api/expr/check",
		"POST /api/dashboards/x/validate", "POST /api/shared/x/patch", "POST /api/shared/x/publish", "POST /api/shared/x/undo", "POST /api/shared/x/evaluate", "POST /api/shared/x/validate",
		"POST /settings/invite", "POST /settings/revoke", "POST /settings/password", "POST /settings/feed", "POST /settings/agents/stop", "POST /settings/agents/resume",
		"POST /logout", "POST /confirm/x", "POST /secure/feed/x",
	}
	payloads := []string{`{"path":"/budget","note":"n","target":{}}`, mcpCall("entry_post"), `{}`}
	for _, route := range mutating {
		method, path, _ := strings.Cut(route, " ")
		for _, payload := range payloads {
			status, body, _ := h.renderReq(t, method, path, payload, cookie)
			if path == "/api/mcp" { // read tools answer here; a write tool must come back as an error and change nothing
				if strings.Contains(payload, "entry_post") && !strings.Contains(body, "rror") && !strings.Contains(body, "not found") && !strings.Contains(body, "unknown") {
					t.Fatal("entry_post over the render cookie answered", status, body)
				}
				continue
			}
			if status != 401 && status != 403 && status != 404 && status != 302 && status != 303 {
				t.Fatal(route, "answered", status)
			}
			if status == 404 && !strings.HasPrefix(path, "/confirm") && !strings.HasPrefix(path, "/secure") {
				t.Fatal(route, "answered 404")
			}
		}
	}
	if after := h.docCounts(t); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatal("a refused write changed data", before, after)
	}

	// Nothing but a live route token counts: a dashboard token, a revoked token and a grant token grant nothing.
	dash, err := h.services.Previews.Issue(h.owner, "", preview.Target{Dashboard: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ = h.renderReq(t, "GET", "/api/bootstrap", "", &http.Cookie{Name: "moneybag_render", Value: dash}); status != 401 {
		t.Fatal("dashboard token as cookie", status)
	}
	if status, _, _ = h.renderReq(t, "GET", "/render/"+dash+"/page", ""); status != 404 {
		t.Fatal("dashboard token on the page route", status)
	}
	if _, err = h.services.Previews.Issue(h.owner, sharing.PermissionHash(sharing.Grant{}), preview.Target{Route: "/budget"}); err == nil {
		t.Fatal("a grant-bound route token was issued")
	}
	h.services.Previews.Revoke(token)
	if status, _, _ = h.renderReq(t, "GET", "/api/bootstrap", "", cookie); status != 401 {
		t.Fatal("revoked token", status)
	}
	_ = ctx
}
