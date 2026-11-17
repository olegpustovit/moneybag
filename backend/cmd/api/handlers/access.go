package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func accessFailure(w http.ResponseWriter, err error) bool {
	if errors.Is(err, identity.ErrAccessDenied) {
		respond(w, 403, map[string]string{"error": "access_denied"})
		return true
	}
	if errors.Is(err, identity.ErrAccessUnavailable) {
		respond(w, 503, map[string]string{"error": "access_unavailable"})
		return true
	}
	return false
}

// Mounted after ServeMux routing so render tokens come from the matched route.
// The original viewer is checked before sharing projects an owner principal.
func (h *Handler) accessRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.services.Identity.AccessEnforced() {
			next.ServeHTTP(w, r)
			return
		}
		var p principal.Principal
		if (strings.HasPrefix(r.URL.Path, "/render/") || strings.HasPrefix(r.URL.Path, "/api/render/")) && h.services.Previews != nil {
			session, err := h.services.Previews.Resolve(r.PathValue("token"))
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			p = session.Principal
		} else if _, err := r.Cookie(h.sessionCookieName()); err != nil && r.URL.Path == "/api/mcp" {
			_, session, ok := h.renderCookie(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			p = session.Principal
		} else {
			current, _, err := h.session(r)
			if err != nil {
				if !accessFailure(w, err) {
					next.ServeHTTP(w, r)
				}
				return
			}
			p = current
		}
		h.serveAccess(w, r, p, next)
	})
}

func (h *Handler) serveAccess(w http.ResponseWriter, r *http.Request, p principal.Principal, next http.Handler) {
	ctx, release, err := h.services.Identity.WatchAccess(r.Context(), p.UserID)
	if err != nil {
		if !accessFailure(w, err) {
			respond(w, 503, map[string]string{"error": "access_unavailable"})
		}
		return
	}
	defer release()
	next.ServeHTTP(w, r.WithContext(ctx))
}

func (h *Handler) accessHandler(next http.HandlerFunc) http.HandlerFunc {
	return h.accessRequest(next).ServeHTTP
}
