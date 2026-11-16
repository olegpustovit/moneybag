package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	idclient "github.com/olegpustovit/identity/idclient-go"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

// ConfigureIdentity runs during composition, before serving any requests.
func (h *Handler) ConfigureIdentity(ctx context.Context, cfg IdentityConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if err := cfg.Validate(h.cfg.PublicURL, h.cfg.TailnetURL); err != nil {
		return err
	}
	if h.services.Identity == nil || h.services.Model == nil || h.ledgerRepo == nil {
		return errors.New("Identity requires account, ledger and model services")
	}
	clients := make(map[string]*idclient.Client)
	origins := make(map[string]bool)
	for _, origin := range []string{h.cfg.PublicURL, h.cfg.TailnetURL} {
		if origin == "" || origins[origin] {
			continue
		}
		client, err := idclient.New(ctx, idclient.Config{Issuer: cfg.Issuer, ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: origin + "/api/auth/id/callback", CookieKey: cfg.CookieKey, Sessions: identitySessions{h}, ResolveUser: h.resolveIdentity})
		if err != nil {
			return err
		}
		parsed, _ := url.Parse(origin)
		clients[strings.ToLower(parsed.Host)] = client
		origins[origin] = true
	}
	h.identityClients, h.identityOrigins, h.identityFlowKey = clients, origins, cfg.CookieKey
	h.identityAccountURL = strings.TrimRight(cfg.Issuer, "/") + "/account"
	h.cfg.IdentityMode = true
	return nil
}
func (h *Handler) identityClient(w http.ResponseWriter, r *http.Request) *idclient.Client {
	client := h.identityClients[strings.ToLower(r.Host)]
	if client == nil {
		http.Error(w, "Unregistered application origin", http.StatusBadRequest)
	}
	return client
}
func (h *Handler) identityLogin(w http.ResponseWriter, r *http.Request) {
	client := h.identityClient(w, r)
	if client == nil {
		return
	}
	query := r.URL.Query()
	token := query.Get("invitation_token")
	if len(query["invitation_token"]) > 1 || !validInvitationToken(token) {
		http.Error(w, "Invalid invitation", 400)
		return
	}
	client.Login(&invitationResponseWriter{ResponseWriter: w, token: token, key: h.identityFlowKey}, r)
}
func (h *Handler) identityCallback(w http.ResponseWriter, r *http.Request) {
	client := h.identityClient(w, r)
	if client == nil {
		return
	}
	http.SetCookie(w, invitationHTTP("", -1))
	var flow identityFlowContext
	if cookie, err := r.Cookie(invitationCookie); err == nil {
		ticket, err := decodeInvitation(cookie.Value, h.identityFlowKey)
		if err != nil || ticket.State != r.URL.Query().Get("state") || !validInvitationToken(ticket.Token) {
			flow.Invalid = true
		} else {
			flow.Invitation = ticket.Token
		}
	}
	client.Callback(w, r.WithContext(context.WithValue(r.Context(), invitationContextKey{}, flow)))
}
func (h *Handler) identityLogout(w http.ResponseWriter, r *http.Request) {
	client := h.identityClient(w, r)
	if client == nil {
		return
	}
	client.Logout(&invitationResponseWriter{ResponseWriter: w}, r)
}
func (h *Handler) resolveIdentity(ctx context.Context, claims idclient.Claims) (string, error) {
	flow, _ := ctx.Value(invitationContextKey{}).(identityFlowContext)
	if flow.Invalid {
		return "", errors.New("invalid invitation context")
	}
	var user identity.User
	var err error
	if flow.Invitation != "" {
		name := claims.Name
		if name == "" {
			name = claims.Email
		}
		user, _, err = h.services.Identity.RegisterIdentity(ctx, claims.Subject, claims.Email, name, flow.Invitation)
	} else {
		user, err = h.services.Identity.LinkIdentity(ctx, claims.Subject, claims.Email)
	}
	if err != nil {
		return "", err
	}
	if err = h.initializeIdentityAccount(ctx, user); err != nil {
		if h.services.Logger != nil {
			h.services.Logger.Error("initialize ID account", "user_id", user.ID, "error", err)
		}
		return "", err
	}
	return user.ID, nil
}
func (h *Handler) initializeIdentityAccount(ctx context.Context, user identity.User) error {
	return postgres.Workflow(ctx, func(ctx context.Context) error {
		current, err := h.services.Identity.User(ctx, user.ID)
		if err != nil {
			return err
		}
		if current.Disabled {
			return principal.ErrForbidden
		}
		pending, err := h.services.Identity.IdentityNeedsInitialization(ctx, user.ID)
		if err != nil || !pending {
			return err
		}
		if h.ledgerRepo == nil || h.services.Model == nil {
			return errors.New("account initialization unavailable")
		}
		p := identity.Principal(current)
		if err = h.ledgerRepo.Init(ctx, p); err != nil {
			return fmt.Errorf("initialize ID ledger: %w", err)
		}
		if err = h.services.Model.Initialize(ctx, p); err != nil {
			return fmt.Errorf("initialize ID model: %w", err)
		}
		return h.services.Identity.IdentityInitialized(ctx, user.ID)
	})
}

type identitySessions struct{ h *Handler }

func (s identitySessions) Create(w http.ResponseWriter, r *http.Request, userID string) error {
	flow, _ := r.Context().Value(invitationContextKey{}).(identityFlowContext)
	if flow.Invalid {
		return errors.New("invalid invitation context")
	}
	user, err := s.h.services.Identity.User(r.Context(), userID)
	if err != nil {
		return err
	}
	raw, _, err := s.h.services.Identity.StartIdentitySession(r.Context(), user)
	if err != nil {
		return err
	}
	s.h.writeSessionCookie(w, raw, 7*24*60*60)
	return nil
}
func (s identitySessions) Clear(w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie(s.h.sessionCookieName()); err == nil {
		if err := s.h.services.Identity.LogoutIdentity(r.Context(), cookie.Value); err != nil {
			return err
		}
	}
	s.h.writeSessionCookie(w, "", -1)
	return nil
}

func (h *Handler) sessionData(user identity.User, session identity.Session) map[string]any {
	data := map[string]any{"user": user, "csrf": session.CSRF, "version": h.cfg.Version, "mcp_url": "/api/mcp"}
	if h.cfg.IdentityMode {
		data["identity_enabled"] = true
		data["identity_account_url"] = h.identityAccountURL
	}
	return data
}
