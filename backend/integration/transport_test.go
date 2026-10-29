package integration

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v3"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/cmd/api/handlers"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testHTTPAndStdio(t *testing.T, ctx context.Context, db *mongo.Database, p principal.Principal, s mcpserver.Services) {
	t.Helper()
	const issuer = "http://localhost:6459"
	oa := oauth.NewService(oauth.NewMongoRepository(db), s.Identity, issuer)
	web := handlers.New(handlers.Config{PublicURL: issuer, Version: "integration"}, s, oa, ledger.NewMongoRepository(db), db.Client())
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	endpoint := "http://" + listener.Addr().String()
	app := fiber.New()
	app.All("/mcp", web.RemoteMCP(false))
	app.All("/mcp/readonly", web.RemoteMCP(true))
	app.All("/*", web.Handler())
	done := make(chan error, 1)
	go func() { done <- app.Listener(listener, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = app.ShutdownWithContext(shutdown)
		<-done
	})
	response, e := http.Get(endpoint + "/mcp")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 401 || !strings.Contains(response.Header.Get("WWW-Authenticate"), "oauth-protected-resource/mcp") || !strings.Contains(response.Header.Get("WWW-Authenticate"), "dash:share") {
		t.Fatal("HTTP auth challenge", response.StatusCode)
	}
	response, e = http.Get(endpoint + "/.well-known/oauth-protected-resource/mcp/readonly")
	if e != nil {
		t.Fatal(e)
	}
	var meta map[string]any
	e = json.NewDecoder(response.Body).Decode(&meta)
	response.Body.Close()
	if e != nil || meta["resource"] != issuer+"/mcp/readonly" {
		t.Fatal("resource metadata", meta, e)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for path, want := range map[string]int{"oauth-authorization-server": 200, "oauth-authorization-server/mcp": 200, "oauth-protected-resource": 200, "openid-configuration": 404, "unknown/doc": 404} {
		r, e := noRedirect.Get(endpoint + "/.well-known/" + path)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != want || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Fatal("well-known", path, r.StatusCode, r.Header.Get("Content-Type"))
		}
	}
	// Shape sent by hosted connectors such as Grok: extra RFC 7591 metadata and an extra grant type.
	dcr, e := http.Post(endpoint+"/oauth/register", "application/json", strings.NewReader(`{"client_name":"Grok Bot","redirect_uris":["https://grok.com/oauth/callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token","client_credentials"],"response_types":["code"],"scope":"ledger:read","client_uri":"https://grok.com","logo_uri":"https://grok.com/logo.png","software_id":"grok"}`))
	if e != nil {
		t.Fatal(e)
	}
	dcr.Body.Close()
	if dcr.StatusCode != 201 {
		t.Fatal("registration with extra metadata", dcr.StatusCode)
	}
	bad, e := http.Post(endpoint+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["javascript:alert(1)"]}`))
	if e != nil {
		t.Fatal(e)
	}
	var reason map[string]string
	_ = json.NewDecoder(bad.Body).Decode(&reason)
	bad.Body.Close()
	if bad.StatusCode != 400 || !strings.Contains(reason["error_description"], "redirect_uri") {
		t.Fatal("rejected registration should explain itself", bad.StatusCode, reason)
	}
	reg, e := oa.Register(ctx, oauth.RegisterInput{Name: "Transport test", RedirectURIs: []string{"http://127.0.0.1:8484/callback"}, AuthMethod: "none"})
	if e != nil {
		t.Fatal(e)
	}
	issue := func(resource, scope string) string {
		t.Helper()
		verifier := identity.Random()
		auth := oauth.Authorization{ClientID: reg.ID, RedirectURI: reg.RedirectURIs[0], ResponseType: "code", Scope: scope, State: "synthetic-state", Challenge: oauth.Challenge(verifier), ChallengeMethod: "S256", Resource: resource}
		code, e := oa.Authorize(ctx, auth, p)
		if e != nil {
			t.Fatal(e)
		}
		tokens, e := oa.Exchange(ctx, reg.Client, code, auth.RedirectURI, verifier, resource)
		if e != nil {
			t.Fatal(e)
		}
		return tokens.AccessToken
	}
	full := issue(issuer+"/mcp", strings.Join(p.Scopes, " "))
	tokenPath := filepath.Join(t.TempDir(), "token")
	if e = os.WriteFile(tokenPath, []byte(full), 0600); e != nil {
		t.Fatal(e)
	}
	proxy, remote, e := mcpserver.Proxy(ctx, endpoint+"/mcp", tokenPath, s.Logger)
	if e != nil {
		t.Fatal("HTTP/stdio bridge", e)
	}
	defer remote.Close()
	st, ct := mcp.NewInMemoryTransports()
	ss, e := proxy.Connect(ctx, st, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "1"}, nil)
	cs, e := client.Connect(ctx, ct, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer cs.Close()
	result, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "account_create", Arguments: map[string]any{"id": "assets:via-proxy", "name": "HTTP and stdio integration", "type": "asset", "commodities": []string{"EUR"}}})
	if e != nil || result.IsError {
		t.Fatal("proxy write workflow", result, e)
	}
	if _, e = s.Ledger.Account(ctx, p, "assets:via-proxy"); e != nil {
		t.Fatal("proxy did not use shared services", e)
	}
	readOnly := issue(issuer+"/mcp/readonly", "ledger:read")
	if e = os.WriteFile(tokenPath, []byte(readOnly), 0600); e != nil {
		t.Fatal(e)
	}
	wrong, e := http.NewRequestWithContext(ctx, "POST", endpoint+"/mcp", strings.NewReader(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	wrong.Header.Set("Authorization", "Bearer "+readOnly)
	response, e = http.DefaultClient.Do(wrong)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("wrong audience reached HTTP MCP", response.StatusCode)
	}
	roProxy, roRemote, e := mcpserver.Proxy(ctx, endpoint+"/mcp/readonly", tokenPath, s.Logger)
	if e != nil {
		t.Fatal(e)
	}
	defer roRemote.Close()
	rst, rct := mcp.NewInMemoryTransports()
	rss, e := roProxy.Connect(ctx, rst, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer rss.Close()
	rcs, e := client.Connect(ctx, rct, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer rcs.Close()
	tools, e := rcs.ListTools(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, tool := range tools.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Fatal("stdio readonly catalog leaked a writer", tool.Name)
		}
	}
	if e = os.Chmod(tokenPath, 0644); e != nil {
		t.Fatal(e)
	}
	if _, _, e = mcpserver.Proxy(ctx, endpoint+"/mcp/readonly", tokenPath, s.Logger); e == nil {
		t.Fatal("public token file accepted")
	}
}
