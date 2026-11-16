package handlers

import (
	idclient "github.com/olegpustovit/identity/idclient-go"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"golang.org/x/net/html"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdentitySignInPages(t *testing.T) {
	templates, err := parseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{cfg: Config{IdentityMode: true}, templates: templates, identityClients: map[string]*idclient.Client{"moneybag.example.test": new(idclient.Client)}}
	for _, tc := range []struct {
		path, token, next string
		register          bool
	}{
		{"/login?next=%2Fsettings", "", "/settings", false},
		{"/login?next=https%3A%2F%2Fevil.test", "", "/", false},
		{"/register?token=invite_123", "invite_123", "/", true},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "https://moneybag.example.test"+tc.path, nil)
		if tc.register {
			h.registerPage(w, r)
		} else {
			h.loginPage(w, r)
		}
		if w.Code != 200 {
			t.Fatal(tc.path, w.Code)
		}
		doc, err := html.Parse(strings.NewReader(w.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		inputs := map[string]string{}
		forms, passwords := 0, 0
		var visit func(*html.Node)
		visit = func(n *html.Node) {
			attrs := map[string]string{}
			for _, a := range n.Attr {
				attrs[a.Key] = a.Val
			}
			if n.Type == html.ElementNode && n.Data == "form" {
				forms++
				if attrs["method"] != "get" || attrs["action"] != "/api/auth/id/login" {
					t.Fatal("incorrect form", attrs)
				}
			}
			if n.Type == html.ElementNode && n.Data == "input" {
				inputs[attrs["name"]] = attrs["value"]
				if attrs["type"] == "password" {
					passwords++
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				visit(c)
			}
		}
		visit(doc)
		if forms != 1 || passwords != 0 || inputs["return_to"] != tc.next || inputs["invitation_token"] != tc.token || !strings.Contains(w.Body.String(), "Continue with ID") {
			t.Fatal("incorrect controls", tc.path, inputs)
		}
	}
	for _, page := range []func(*httptest.ResponseRecorder){
		func(w *httptest.ResponseRecorder) {
			h.loginPage(w, httptest.NewRequest("GET", "https://unknown.test/login", nil))
		},
		func(w *httptest.ResponseRecorder) {
			h.registerPage(w, httptest.NewRequest("GET", "https://unknown.test/register", nil))
		},
	} {
		w := httptest.NewRecorder()
		page(w)
		if w.Code != 400 {
			t.Fatal("unknown host accepted")
		}
	}
	h.cfg.IdentityMode = false
	w := httptest.NewRecorder()
	h.loginPage(w, httptest.NewRequest("GET", "https://moneybag.example.test/login", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="password"`) || strings.Contains(w.Body.String(), "Continue with ID") {
		t.Fatal("legacy sign-in changed")
	}
}
func TestIdentitySessionMetadata(t *testing.T) {
	h := &Handler{cfg: Config{Version: "test"}, identityAccountURL: "https://id.example.test/account"}
	if _, ok := h.sessionData(identity.User{}, identity.Session{})["identity_enabled"]; ok {
		t.Fatal("legacy session metadata changed")
	}
	h.cfg.IdentityMode = true
	data := h.sessionData(identity.User{}, identity.Session{CSRF: "csrf"})
	if data["identity_enabled"] != true || data["identity_account_url"] != h.identityAccountURL || data["csrf"] != "csrf" || len(data) != 6 {
		t.Fatal("incorrect public metadata")
	}
}

func TestConsentFormPolicyAllowsOnlySelectedRedirect(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct{ target, source string }{
		{"https://client.example.test:8443/callback?state=private", "https://client.example.test:8443"},
		{"http://127.0.0.1:8484/callback", "http://127.0.0.1:8484"},
		{"cursor://anysphere.cursor-mcp/oauth/callback", "cursor:"},
		{"com.example.app:/oauth2redirect", "com.example.app:"},
	} {
		w := httptest.NewRecorder()
		h.security(w)
		h.allowConsentRedirect(w, tc.target)
		policy := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(policy, "form-action 'self' "+tc.source+";") || strings.Contains(policy, "private") {
			t.Fatal("incorrect selected redirect policy", policy)
		}
	}
	for _, target := range []string{"javascript:alert(1)", "https://*.example.test/callback", "https://client.example.test;script-src/callback", "https://user@client.example.test/callback"} {
		w := httptest.NewRecorder()
		h.security(w)
		before := w.Header().Get("Content-Security-Policy")
		h.allowConsentRedirect(w, target)
		if w.Header().Get("Content-Security-Policy") != before {
			t.Fatal("unsafe target changed policy")
		}
	}
}
