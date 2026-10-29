package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
)

func testSPAAPI(t *testing.T, ctx context.Context, endpoint string, p principal.Principal, services mcpserver.Services) {
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
	for _, path := range []string{"/api/session", "/api/imports", "/api/settings", "/api/expr/catalog"} {
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
		case "/api/settings":
			if _, ok := body["agents"]; !ok {
				t.Fatalf("settings: %v", body)
			}
			if _, ok := body["feeds"]; !ok {
				t.Fatalf("settings: %v", body)
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
