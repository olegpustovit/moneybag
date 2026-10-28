package handlers

import (
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"net/http"
	"net/url"
)

func (h *Handler) settingsInvite(w http.ResponseWriter, r *http.Request) {
	p, session, e := h.session(r)
	if e != nil || !h.csrf(r, session) {
		http.Error(w, "reload and retry", 403)
		return
	}
	raw, e := h.services.Identity.Invite(r.Context(), p, r.FormValue("email"))
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if e = h.templates.ExecuteTemplate(w, "invitation.html", h.cfg.PublicURL+"/register?token="+url.QueryEscape(raw)); e != nil {
		h.services.Logger.Error("render invitation", "error", e)
	}
}
func (h *Handler) settingsRevoke(w http.ResponseWriter, r *http.Request) {
	p, session, e := h.session(r)
	if e != nil || !h.csrf(r, session) {
		http.Error(w, "reload and retry", 403)
		return
	}
	if e = h.oauth.RevokeGrant(r.Context(), p, r.FormValue("id")); e != nil {
		http.Error(w, "could not revoke this connection; retry", 400)
		return
	}
	http.Redirect(w, r, "/settings", 303)
}
func (h *Handler) settingsPassword(w http.ResponseWriter, r *http.Request) {
	p, session, e := h.session(r)
	if e != nil || !h.csrf(r, session) {
		http.Error(w, "reload and retry", 403)
		return
	}
	if e = h.services.Identity.ChangePassword(r.Context(), p, r.FormValue("current"), r.FormValue("next")); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/login", 303)
}

func (h *Handler) settingsFeed(w http.ResponseWriter, r *http.Request) {
	p, session, e := h.session(r)
	if e != nil || !h.csrf(r, session) {
		http.Error(w, "reload and retry", 403)
		return
	}
	_, e = h.services.Imports.ConfigureFeed(r.Context(), p, imports.FeedInput{ID: r.FormValue("id"), URL: r.FormValue("url"), Account: r.FormValue("account"), Format: r.FormValue("format"), DateFormat: r.FormValue("date_format"), Token: r.FormValue("token")})
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	http.Redirect(w, r, "/settings", 303)
}
