package handlers

import (
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"net/http"
	"net/url"
	"strings"
)

func settingsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func settingsError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if settingsJSON(r) {
		respond(w, status, map[string]string{"error": message})
	} else {
		http.Error(w, message, status)
	}
}

func (h *Handler) settingsPrincipal(w http.ResponseWriter, r *http.Request) (principal.Principal, bool) {
	p, session, err := h.session(r)
	if err != nil {
		status := http.StatusForbidden
		if settingsJSON(r) {
			status = http.StatusUnauthorized
		}
		settingsError(w, r, status, "reload and retry")
		return p, false
	}
	if !h.csrf(r, session) {
		settingsError(w, r, http.StatusForbidden, "reload and retry")
		return p, false
	}
	return p, true
}

func (h *Handler) settingsInvite(w http.ResponseWriter, r *http.Request) {
	p, ok := h.settingsPrincipal(w, r)
	if !ok {
		return
	}
	raw, e := h.services.Identity.Invite(r.Context(), p, r.FormValue("email"))
	if e != nil {
		settingsError(w, r, 400, e.Error())
		return
	}
	if settingsJSON(r) {
		respond(w, 200, map[string]string{"invitation_url": h.cfg.PublicURL + "/register?token=" + url.QueryEscape(raw)})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if e = h.templates.ExecuteTemplate(w, "invitation.html", h.cfg.PublicURL+"/register?token="+url.QueryEscape(raw)); e != nil {
		h.services.Logger.Error("render invitation", "error", e)
	}
}
func (h *Handler) settingsRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := h.settingsPrincipal(w, r)
	if !ok {
		return
	}
	if e := h.oauth.RevokeGrant(r.Context(), p, r.FormValue("id")); e != nil {
		settingsError(w, r, 400, "could not revoke this connection; retry")
		return
	}
	if settingsJSON(r) {
		respond(w, 200, map[string]bool{"ok": true})
		return
	}
	http.Redirect(w, r, "/settings", 303)
}
func (h *Handler) settingsPassword(w http.ResponseWriter, r *http.Request) {
	p, ok := h.settingsPrincipal(w, r)
	if !ok {
		return
	}
	if e := h.services.Identity.ChangePassword(r.Context(), p, r.FormValue("current"), r.FormValue("next")); e != nil {
		settingsError(w, r, 400, e.Error())
		return
	}
	if settingsJSON(r) {
		respond(w, 200, map[string]any{"ok": true, "redirect": "/login"})
		return
	}
	http.Redirect(w, r, "/login", 303)
}

func (h *Handler) settingsFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := h.settingsPrincipal(w, r)
	if !ok {
		return
	}
	_, e := h.services.Imports.ConfigureFeed(r.Context(), p, imports.FeedInput{ID: r.FormValue("id"), URL: r.FormValue("url"), Account: r.FormValue("account"), Format: r.FormValue("format"), DateFormat: r.FormValue("date_format"), Token: r.FormValue("token")})
	if e != nil {
		settingsError(w, r, 400, e.Error())
		return
	}
	if settingsJSON(r) {
		respond(w, 200, map[string]bool{"ok": true})
		return
	}
	http.Redirect(w, r, "/settings", 303)
}
