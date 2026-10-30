package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

type authPage struct {
	Title, Action, Error, Token, Next, CSRF string
	Setup, Register, Consent                bool
	ClientName                              string
	Scopes                                  []string
	Authorization                           oauth.Authorization
}

func (h *Handler) authRender(w http.ResponseWriter, status int, data authPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if e := h.templates.ExecuteTemplate(w, "auth.html", data); e != nil {
		h.services.Logger.Error("render auth page", "error", e)
	}
}
func localNext(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\r\n\\") {
		return "/"
	}
	return raw
}
func (h *Handler) authOrigin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != h.cfg.PublicURL {
		http.Error(w, "origin not allowed", 403)
		return false
	}
	return true
}
func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	h.authRender(w, 200, authPage{Title: "Sign in to Moneybag", Action: "/login", Next: localNext(r.URL.Query().Get("next"))})
}
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.authOrigin(w, r) {
		return
	}
	u, e := h.services.Identity.Login(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if e != nil {
		h.authRender(w, 401, authPage{Title: "Sign in to Moneybag", Action: "/login", Error: "Email or password is incorrect.", Next: localNext(r.FormValue("next"))})
		return
	}
	h.startSession(w, r, u, localNext(r.FormValue("next")))
}
func (h *Handler) startSession(w http.ResponseWriter, r *http.Request, u identity.User, next string) {
	raw, _, e := h.services.Identity.StartSession(r.Context(), u)
	if e != nil {
		http.Error(w, "could not start session; try again", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "moneybag_session", Value: raw, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(h.cfg.PublicURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: 7 * 24 * 60 * 60})
	http.Redirect(w, r, next, http.StatusSeeOther)
}
func (h *Handler) setupPage(w http.ResponseWriter, r *http.Request) {
	needed, e := h.services.Identity.NeedsSetup(r.Context())
	if e != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	if !needed {
		http.Redirect(w, r, "/login", 303)
		return
	}
	h.authRender(w, 200, authPage{Title: "Set up Moneybag", Action: "/setup", Setup: true})
}
func (h *Handler) setup(w http.ResponseWriter, r *http.Request) {
	if !h.authOrigin(w, r) {
		return
	}
	if h.cfg.SetupToken == "" || subtle.ConstantTimeCompare([]byte(r.FormValue("token")), []byte(h.cfg.SetupToken)) != 1 {
		h.authRender(w, 403, authPage{Title: "Set up Moneybag", Action: "/setup", Setup: true, Error: "Enter the setup token from the server environment."})
		return
	}
	u, e := h.services.Identity.Setup(r.Context(), r.FormValue("email"), r.FormValue("name"), r.FormValue("password"))
	if e != nil {
		h.authRender(w, 400, authPage{Title: "Set up Moneybag", Action: "/setup", Setup: true, Error: e.Error()})
		return
	}
	if e = h.ledgerRepo.Init(r.Context(), identity.Principal(u)); e != nil {
		http.Error(w, "account created but indexes failed; check server logs", 500)
		h.services.Logger.Error("initialize ledger indexes", "error", e)
		return
	}
	if e = h.services.Model.Initialize(r.Context(), identity.Principal(u)); e != nil {
		http.Error(w, "account created; default model initialization failed", 500)
		h.services.Logger.Error("initialize model", "error", e)
		return
	}
	h.startSession(w, r, u, "/")
}
func (h *Handler) registerPage(w http.ResponseWriter, r *http.Request) {
	h.authRender(w, 200, authPage{Title: "Join Moneybag", Action: "/register", Register: true, Token: r.URL.Query().Get("token")})
}
func (h *Handler) registerUser(w http.ResponseWriter, r *http.Request) {
	if !h.authOrigin(w, r) {
		return
	}
	u, e := h.services.Identity.Register(r.Context(), r.FormValue("token"), r.FormValue("name"), r.FormValue("password"))
	if e != nil {
		h.authRender(w, 400, authPage{Title: "Join Moneybag", Action: "/register", Register: true, Token: r.FormValue("token"), Error: e.Error()})
		return
	}
	if e = h.ledgerRepo.Init(r.Context(), identity.Principal(u)); e != nil {
		http.Error(w, "account initialization failed", 500)
		return
	}
	if e = h.services.Model.Initialize(r.Context(), identity.Principal(u)); e != nil {
		http.Error(w, "account created; default model initialization failed", 500)
		h.services.Logger.Error("initialize model", "error", e)
		return
	}
	h.startSession(w, r, u, "/")
}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	_, session, e := h.session(r)
	if e != nil || !h.csrf(r, session) {
		http.Error(w, "reload and retry", 403)
		return
	}
	cookie, _ := r.Cookie("moneybag_session")
	if e = h.services.Identity.Logout(r.Context(), cookie.Value); e != nil {
		http.Error(w, "could not sign out; retry", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "moneybag_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(h.cfg.PublicURL, "https://"), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", 303)
}
func (h *Handler) registerClient(w http.ResponseWriter, r *http.Request) {
	var in oauth.RegisterInput
	// RFC 7591 §2: unknown client metadata (scope, logo_uri, software_id, ...) must be ignored, not rejected.
	if e := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&in); e != nil {
		respond(w, 400, map[string]string{"error": "invalid_client_metadata", "error_description": "body must be a JSON client metadata object"})
		return
	}
	v, e := h.oauth.Register(r.Context(), in)
	if e != nil {
		slog.Warn("oauth registration rejected", "client_name", in.Name, "redirect_uris", in.RedirectURIs, "error", e)
		respond(w, 400, map[string]string{"error": "invalid_client_metadata", "error_description": e.Error()})
		return
	}
	respond(w, 201, v)
}
func (h *Handler) authorization(r *http.Request) oauth.Authorization {
	q := r.URL.Query()
	if r.Method == "POST" {
		_ = r.ParseForm()
		q = r.PostForm
	}
	in := oauth.Authorization{ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), ResponseType: q.Get("response_type"), Scope: q.Get("scope"), State: q.Get("state"), Challenge: q.Get("code_challenge"), ChallengeMethod: q.Get("code_challenge_method"), Resource: q.Get("resource")}
	if in.Resource == "" {
		in.Resource = h.cfg.PublicURL + "/mcp" // resource is optional in RFC 8707; default to the full MCP endpoint
	}
	return in
}
func (h *Handler) authorizePage(w http.ResponseWriter, r *http.Request) {
	in := h.authorization(r)
	client, scopes, e := h.oauth.Validate(r.Context(), in)
	if e != nil {
		slog.Warn("oauth authorize rejected", "client_id", in.ClientID, "redirect_uri", in.RedirectURI, "error", e)
		http.Error(w, "Invalid authorization request. Start again from your connector.", 400)
		return
	}
	_, session, e := h.session(r)
	if e != nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), 303)
		return
	}
	h.authRender(w, 200, authPage{Title: "Connect " + client.Name, Action: "/oauth/authorize", Consent: true, ClientName: client.Name, Scopes: scopes, Authorization: in, CSRF: session.CSRF})
}
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) {
	p, session, e := h.session(r)
	if e != nil {
		http.Error(w, "sign in first", 401)
		return
	}
	if !h.csrf(r, session) {
		http.Error(w, "reload and retry", 403)
		return
	}
	in := h.authorization(r)
	if _, _, e = h.oauth.Validate(r.Context(), in); e != nil {
		http.Error(w, "invalid authorization request", 400)
		return
	}
	u, _ := url.Parse(in.RedirectURI)
	q := u.Query()
	q.Set("state", in.State)
	q.Set("iss", h.cfg.PublicURL)
	if r.FormValue("decision") == "deny" {
		q.Set("error", "access_denied")
	} else if r.FormValue("decision") == "allow" {
		code, e := h.oauth.Authorize(r.Context(), in, p)
		if e != nil {
			http.Error(w, "could not authorize; retry from your connector", 400)
			return
		}
		q.Set("code", code)
	} else {
		http.Error(w, "choose allow or deny", 400)
		return
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), 303)
}
func (h *Handler) oauthClient(r *http.Request) (oauth.Client, error) {
	id, secret := r.FormValue("client_id"), r.FormValue("client_secret")
	method := "none"
	if basicID, basicSecret, ok := r.BasicAuth(); ok {
		id, secret, method = basicID, basicSecret, "client_secret_basic"
	} else if secret != "" {
		method = "client_secret_post"
	}
	return h.oauth.AuthenticateClient(r.Context(), id, secret, method)
}
func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Pragma", "no-cache")
	if e := r.ParseForm(); e != nil {
		respond(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	c, e := h.oauthClient(r)
	if e != nil {
		slog.Warn("oauth token client rejected", "client_id", r.FormValue("client_id"), "error", e)
		respond(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	var tokens oauth.TokenResponse
	switch r.FormValue("grant_type") {
	case "authorization_code":
		tokens, e = h.oauth.Exchange(r.Context(), c, r.FormValue("code"), r.FormValue("redirect_uri"), r.FormValue("code_verifier"), r.FormValue("resource"))
	case "refresh_token":
		tokens, e = h.oauth.Refresh(r.Context(), c, r.FormValue("refresh_token"), r.FormValue("resource"), r.FormValue("scope"))
	default:
		e = fmt.Errorf("unsupported_grant_type")
	}
	if e != nil {
		if e == oauth.ErrAgentsSuspended {
			respond(w, 403, map[string]string{"error": "agents_suspended", "error_description": e.Error()})
			return
		}
		code := "invalid_grant"
		if e == oauth.ErrInvalidTarget {
			code = "invalid_target"
		} else if e == oauth.ErrInvalidScope {
			code = "invalid_scope"
		}
		slog.Warn("oauth token rejected", "client_id", c.ID, "grant_type", r.FormValue("grant_type"), "error", e)
		respond(w, 400, map[string]string{"error": code})
		return
	}
	respond(w, 200, tokens)
}
func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	c, e := h.oauthClient(r)
	if e != nil {
		respond(w, 401, map[string]string{"error": "invalid_client"})
		return
	}
	if e = h.oauth.Revoke(r.Context(), c, r.FormValue("token")); e != nil {
		http.Error(w, "revocation unavailable; retry", 503)
		return
	}
	w.WriteHeader(200)
}
