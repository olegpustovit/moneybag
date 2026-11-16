package integration

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func TestIdentityBrowserSessionsProtectConsentPreviewAndAgents(t *testing.T) {
	const origin = "https://public.example.test"
	h := newReadHarnessMode(t, "_identity_sessions", nil, true)
	user, err := h.services.Identity.User(h.ctx, h.owner.UserID)
	if err != nil {
		t.Fatal(err)
	}
	legacy, _, err := h.services.Identity.StartSession(h.ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	currentCookie := &http.Cookie{Name: "__Host-moneybag_session", Value: h.cookie}
	client := &http.Client{Transport: h.http.Transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, body, requestOrigin, csrf, bearer string, cookies ...*http.Cookie) (int, string, http.Header) {
		t.Helper()
		req, err := http.NewRequestWithContext(h.ctx, method, h.endpoint+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", requestOrigin)
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Accept", "application/json, text/event-stream")
		contentType := "application/json"
		if method == "POST" && path == "/oauth/authorize" {
			contentType = "application/x-www-form-urlencoded"
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", "tools/call")
		req.Header.Set("Mcp-Name", mcpName(body))
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(data), response.Header
	}
	if status, _, _ := request("GET", "/api/session", "", "", "", "", currentCookie); status != 200 {
		t.Fatal("ID session rejected", status)
	}
	for _, cookie := range []*http.Cookie{{Name: "moneybag_session", Value: legacy}, {Name: "__Host-moneybag_session", Value: legacy}} {
		if status, _, _ := request("GET", "/api/session", "", "", "", "", cookie); status != 401 {
			t.Fatal("legacy/renamed token bypassed ID mode", status)
		}
	}
	for _, route := range []string{"GET /login", "GET /setup", "GET /register", "POST /login", "POST /setup", "POST /register", "POST /settings/password"} {
		method, path, _ := strings.Cut(route, " ")
		if status, _, _ := request(method, path, `{}`, origin, h.csrf, "", currentCookie); status != 403 {
			t.Fatal("ID mode exposed local credentials", route, status)
		}
	}
	for _, requestOrigin := range []string{"", "https://wrong.example.test", tailnetOrigin} {
		if status, _, _ := request("POST", "/api/mcp", mcpCall("whoami"), requestOrigin, h.csrf, "", currentCookie); status != 403 {
			t.Fatal("ID mutation accepted an inexact origin", status)
		}
	}
	if status, body, _ := request("POST", "/api/mcp", mcpCall("whoami"), origin, h.csrf, "", currentCookie); status != 200 || strings.Contains(body, `"isError":true`) {
		t.Fatal("valid ID browser MCP refused", status)
	}
	if _, err := h.db.Exec(h.ctx, `UPDATE users SET is_admin=false WHERE id=@id`, pgx.NamedArgs{"id": user.ID}); err != nil {
		t.Fatal(err)
	}
	status, body, _ := request("GET", "/api/session", "", "", "", "", currentCookie)
	var session struct {
		User struct {
			Admin bool `json:"is_admin"`
		}
	}
	if err := json.Unmarshal([]byte(body), &session); err != nil || status != 200 || session.User.Admin {
		t.Fatal("ID session retained stale administrator authority")
	}

	// Authorize an agent through the real consent route using this ID browser session.
	oa := oauth.NewService(oauth.NewPostgresRepository(h.db), h.services.Identity, origin)
	registered, err := oa.Register(h.ctx, oauth.RegisterInput{Name: "Owned agent", RedirectURIs: []string{"http://127.0.0.1:8484/callback"}, AuthMethod: "none"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	query := url.Values{"client_id": {registered.ID}, "redirect_uri": {registered.RedirectURIs[0]}, "response_type": {"code"}, "scope": {"ledger:read"}, "state": {"fixture-state"}, "code_challenge": {oauth.Challenge(verifier)}, "code_challenge_method": {"S256"}, "resource": {origin + "/mcp"}}
	if status, body, _ := request("GET", "/oauth/authorize?"+query.Encode(), "", "", "", "", currentCookie); status != 200 || !strings.Contains(body, h.csrf) {
		t.Fatal("ID session did not reach OAuth consent", status)
	}
	query.Set("decision", "allow")
	query.Set("csrf", h.csrf)
	if status, _, _ := request("POST", "/oauth/authorize", query.Encode(), "", h.csrf, "", currentCookie); status != 403 {
		t.Fatal("consent accepted missing Origin", status)
	}
	status, _, headers := request("POST", "/oauth/authorize", query.Encode(), origin, h.csrf, "", currentCookie)
	if status != 303 {
		t.Fatal("ID consent failed", status)
	}
	callback, err := url.Parse(headers.Get("Location"))
	if err != nil || callback.Query().Get("state") != "fixture-state" || callback.Query().Get("code") == "" {
		t.Fatal("consent callback missing code/state")
	}
	tokens, err := oa.Exchange(h.ctx, registered.Client, callback.Query().Get("code"), registered.RedirectURIs[0], verifier, origin+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := oa.Bearer(h.ctx, tokens.AccessToken, origin+"/mcp")
	if err != nil || agent.Actor.Kind != "agent" || agent.UserID != user.ID || !errors.Is(agent.Require("ledger:write"), principal.ErrForbidden) {
		t.Fatal("agent identity or read-only scopes changed")
	}
	invalidBrowser := &http.Cookie{Name: "__Host-moneybag_session", Value: "irrelevant-invalid-browser-token"}
	if status, body, _ := request("POST", "/mcp", mcpCall("whoami"), "", "", tokens.AccessToken, invalidBrowser); status != 200 || strings.Contains(body, `"isError":true`) || !strings.Contains(body, "ledger:read") {
		t.Fatal("agent bearer inherited browser-cookie/Origin policy", status)
	}
	write := strings.Replace(mcpCall("entry_post"), `"arguments":{}`, `"arguments":{"date":"2026-11-01","payee":"Must refuse","commit":true,"postings":[{"account":"assets:bank","amount":{"v":"1.00","c":"EUR"}},{"account":"expenses:test","amount":{"v":"-1.00","c":"EUR"}}]}`, 1)
	if status, body, _ := request("POST", "/mcp", write, "", "", tokens.AccessToken, invalidBrowser); status != 200 || !strings.Contains(body, "ledger:write") {
		t.Fatal("read-only agent write was not refused by scope", status)
	}

	if status, _, _ := request("POST", "/logout", "", origin, "wrong", "", currentCookie); status != 403 {
		t.Fatal("ID logout accepted wrong CSRF", status)
	}
	status, _, headers = request("POST", "/logout", "", origin, h.csrf, "", currentCookie)
	if status != 303 {
		t.Fatal("ID logout failed", status)
	}
	cookies := (&http.Response{Header: headers}).Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-moneybag_session" || !cookies[0].Secure || cookies[0].MaxAge != -1 {
		t.Fatal("ID logout cleared the wrong cookie")
	}
	if status, _, _ := request("GET", "/api/session", "", "", "", "", currentCookie); status != 401 {
		t.Fatal("ID logout left session usable", status)
	}
	if _, _, err := h.services.Identity.Session(h.ctx, legacy); err != nil {
		t.Fatal("ID logout revoked legacy rollback session")
	}
	if status, _, _ := request("POST", "/mcp", mcpCall("whoami"), "", "", tokens.AccessToken); status != 200 {
		t.Fatal("browser logout revoked agent bearer", status)
	}
	if err := h.services.Identity.SetDisabled(h.ctx, user.ID, true); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := request("POST", "/mcp", mcpCall("whoami"), "", "", tokens.AccessToken); status != 401 {
		t.Fatal("disabled user retained agent access", status)
	}
}
