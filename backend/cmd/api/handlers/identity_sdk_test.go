package handlers

import (
	"context"
	"encoding/json"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestIdentitySDKUsesRegisteredOriginAndStateBoundInvitations(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issuer := "http://" + r.Host
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.Error(w, "fixture refuses exchange", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
	}))
	defer provider.Close()
	// This metadata-only provider refuses exchange; service methods are never called.
	h := &Handler{cfg: Config{PublicURL: "https://moneybag.example.test", TailnetURL: "https://moneybag.example.ts.net:8443"}, services: mcpserver.Services{Identity: identity.NewService(identity.NewPostgresRepository(nil)), Model: &model.Service{}}, ledgerRepo: ledger.NewPostgresRepository(nil)}
	cfg := IdentityConfig{Enabled: true, Issuer: provider.URL, ClientID: "fixture-client", CookieKey: [32]byte{42}}
	if err := h.ConfigureIdentity(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !h.cfg.IdentityMode || !h.identityOrigin(h.cfg.PublicURL) || !h.identityOrigin(h.cfg.TailnetURL) || h.identityOrigin("https://other.example.test") {
		t.Fatal("configured origin set incorrect")
	}
	for _, origin := range []string{h.cfg.PublicURL, h.cfg.TailnetURL} {
		w := httptest.NewRecorder()
		h.identityLogin(w, httptest.NewRequest("GET", origin+"/api/auth/id/login?invitation_token=fixture_invite&return_to=%2Fsettings", nil))
		if w.Code != 303 {
			t.Fatal("SDK login failed", w.Code)
		}
		location, err := url.Parse(w.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if location.Query().Get("redirect_uri") != origin+"/api/auth/id/callback" || location.Query().Get("code_challenge_method") != "S256" || location.Query().Get("client_id") != cfg.ClientID {
			t.Fatal("wrong SDK callback or PKCE configuration")
		}
		var invitation, flow *http.Cookie
		for _, cookie := range w.Result().Cookies() {
			switch cookie.Name {
			case invitationCookie:
				invitation = cookie
			case "__Host-idclient-flow":
				flow = cookie
			}
		}
		if invitation == nil || flow == nil || !invitation.Secure || !invitation.HttpOnly || invitation.Domain != "" || invitation.Path != "/" || invitation.MaxAge != 600 {
			t.Fatal("private invitation/flow cookie pair missing")
		}
		ticket, err := decodeInvitation(invitation.Value, cfg.CookieKey)
		if err != nil || ticket.Token != "fixture_invite" || ticket.State != location.Query().Get("state") {
			t.Fatal("invitation not bound to SDK state")
		}
		for _, query := range []string{"invitation_token=invalid%3Bvalue", "invitation_token=one&invitation_token=two", "invitation_token=valid&return_to=https%3A%2F%2Fevil.example.test"} {
			bad := httptest.NewRecorder()
			h.identityLogin(bad, httptest.NewRequest("GET", origin+"/api/auth/id/login?"+query, nil))
			if bad.Code != 400 || len(bad.Result().Cookies()) != 0 {
				t.Fatal("invalid login modified pending cookies")
			}
		}
		for _, value := range []string{invitation.Value + "tampered", encodeInvitation(invitationTicket{Token: "fixture_invite", State: "different"}, cfg.CookieKey)} {
			callback := httptest.NewRequest("GET", origin+"/api/auth/id/callback?state="+location.Query().Get("state")+"&code=invalid", nil)
			callback.AddCookie(flow)
			callback.AddCookie(&http.Cookie{Name: invitationCookie, Value: value})
			out := httptest.NewRecorder()
			h.identityCallback(out, callback)
			if out.Code != 403 {
				t.Fatal("invalid callback accepted")
			}
			cleared := map[string]bool{}
			for _, cookie := range out.Result().Cookies() {
				if cookie.MaxAge == -1 {
					cleared[cookie.Name] = true
				}
			}
			if !cleared[invitationCookie] || !cleared["__Host-idclient-flow"] {
				t.Fatal("failed callback did not consume both flow cookies")
			}
		}
		badLogout := httptest.NewRequest("POST", origin+"/api/auth/id/logout", nil)
		badLogout.Header.Set("Origin", "https://other.example.test")
		out := httptest.NewRecorder()
		h.identityLogout(out, badLogout)
		if out.Code != 403 || len(out.Result().Cookies()) != 0 {
			t.Fatal("wrong-origin logout modified cookies")
		}
		logout := httptest.NewRequest("POST", origin+"/api/auth/id/logout", nil)
		logout.Header.Set("Origin", origin)
		cleared := httptest.NewRecorder()
		h.identityLogout(cleared, logout)
		if cleared.Code != 303 || cleared.Header().Get("Location") != "/" {
			t.Fatal("SDK logout rejected its own origin")
		}
		names := map[string]bool{}
		for _, cookie := range cleared.Result().Cookies() {
			if cookie.MaxAge != -1 || !cookie.Secure || !cookie.HttpOnly {
				t.Fatal("SDK logout emitted unsafe cookie")
			}
			names[cookie.Name] = true
		}
		if len(names) != 3 || !names[invitationCookie] || !names["__Host-idclient-flow"] || !names["__Host-moneybag_session"] {
			t.Fatal("SDK logout did not clear exactly app-local cookies")
		}

	}
	unknown := httptest.NewRecorder()
	h.identityLogin(unknown, httptest.NewRequest("GET", "https://unregistered.example.test/api/auth/id/login", nil))
	if unknown.Code != 400 || len(unknown.Result().Cookies()) != 0 {
		t.Fatal("unknown host selected an SDK client")
	}
	wrong := cfg.CookieKey
	wrong[0]++
	value := encodeInvitation(invitationTicket{Token: "fixture", State: "state"}, cfg.CookieKey)
	if _, err := decodeInvitation(value, wrong); err == nil {
		t.Fatal("invitation accepted under wrong key")
	}
}

func TestIdentityConfigRejectsCallbackHostCollisions(t *testing.T) {
	cfg := IdentityConfig{Enabled: true, Issuer: "https://id.example.test", ClientID: "fixture", CookieKey: [32]byte{1}}
	for _, value := range []struct{ public, tailnet string }{{"http://localhost:6459", "https://localhost:6459"}, {"https://moneybag.example.test:443", ""}, {"https://Mixed.example.test", ""}, {"https://moneybag.example.test?", ""}} {
		if err := cfg.Validate(value.public, value.tailnet); err == nil {
			t.Fatal("ambiguous callback origin accepted")
		}
	}
}

func TestIdentityStartupRejectsMissingApplicationServices(t *testing.T) {
	h := &Handler{cfg: Config{PublicURL: "https://moneybag.example.test"}}
	cfg := IdentityConfig{Enabled: true, Issuer: "https://id.example.test", ClientID: "fixture", CookieKey: [32]byte{1}}
	if err := h.ConfigureIdentity(t.Context(), cfg); err == nil || h.cfg.IdentityMode || len(h.identityClients) != 0 {
		t.Fatal("incomplete composition activated SDK transport")
	}
	cfg.Enabled = false
	if err := h.ConfigureIdentity(t.Context(), cfg); err != nil || h.cfg.IdentityMode {
		t.Fatal("disabled SDK changed legacy composition")
	}
}
