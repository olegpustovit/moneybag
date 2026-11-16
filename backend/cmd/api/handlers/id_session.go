package handlers

import (
	"net/http"
	"strings"
)

func (h *Handler) sessionCookieName() string {
	if h.cfg.IdentityMode {
		return "__Host-moneybag_session"
	}
	return "moneybag_session"
}
func (h *Handler) writeSessionCookie(w http.ResponseWriter, raw string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: h.sessionCookieName(), Value: raw, Path: "/", HttpOnly: true, Secure: h.cfg.IdentityMode || strings.HasPrefix(h.cfg.PublicURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (h *Handler) localCredentialsUnavailable(w http.ResponseWriter) bool {
	if !h.cfg.IdentityMode {
		return false
	}
	http.Error(w, "Local credentials are unavailable while Identity sign-in is enabled.", http.StatusForbidden)
	return true
}

func (h *Handler) identityOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	if len(h.identityOrigins) > 0 {
		return h.identityOrigins[origin]
	}
	return origin == h.cfg.PublicURL
}
