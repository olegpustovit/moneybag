package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/feedback"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

// notModified sets an ETag from the data revision and request and answers 304 when the browser already has it.
func (h *Handler) notModified(w http.ResponseWriter, r *http.Request, p principal.Principal) (int64, bool, error) {
	revision, e := h.services.Audit.Revision(r.Context(), p)
	if e != nil {
		return 0, false, e
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%d|%s|%s", p.UserID, revision, r.URL.Path, r.URL.RawQuery))
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return revision, true, nil
	}
	return revision, false, nil
}

func (h *Handler) apiCube(w http.ResponseWriter, r *http.Request) {
	p, _, e := h.session(r)
	if e != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	revision, done, e := h.notModified(w, r, p)
	if e != nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": "could not read the data revision"})
		return
	}
	if done {
		return
	}
	cube, e := h.services.Semantic.Cube(r.Context(), p, revision)
	if e != nil {
		h.services.Logger.ErrorContext(r.Context(), "build ledger cube", "error", e)
		w.Header().Del("ETag")
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not build the cube"})
		return
	}
	respond(w, http.StatusOK, cube)
}

// actorLabels names people and agents for the feed; an unknown id reads as a kind, never as a raw client URL.
func (h *Handler) actorLabels(r *http.Request, p principal.Principal) (func(principal.Actor) string, error) {
	agents, e := h.oauth.AgentNames(r.Context(), p)
	if e != nil {
		return nil, e
	}
	names := make(map[string]string, len(agents))
	for _, agent := range agents {
		names[agent.ClientID] = agent.ClientName
	}
	people := map[string]string{}
	return func(actor principal.Actor) string {
		switch actor.Kind {
		case "system":
			return "System"
		case "agent":
			if name := names[actor.ID]; name != "" {
				return name
			}
			return "An agent"
		}
		name, known := people[actor.ID]
		if !known {
			if u, e := h.services.Identity.User(r.Context(), actor.ID); e == nil {
				name = u.Name
			}
			people[actor.ID] = name
		}
		if name == "" {
			return "Someone"
		}
		return name
	}, nil
}

func (h *Handler) apiChanges(w http.ResponseWriter, r *http.Request) {
	p, _, e := h.session(r)
	if e != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	if _, done, e := h.notModified(w, r, p); e != nil {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": "could not read the data revision"})
		return
	} else if done {
		return
	}
	q := r.URL.Query()
	in := audit.ChangesInput{From: q.Get("from"), To: q.Get("to"), Q: q.Get("q"), Routine: q.Get("routine") == "true" || q.Get("routine") == "1"}
	if raw := q.Get("cursor"); raw != "" {
		if in.Cursor, e = strconv.ParseInt(raw, 10, 64); e != nil || in.Cursor < 0 {
			w.Header().Del("ETag")
			respond(w, http.StatusBadRequest, map[string]string{"error": "invalid cursor"})
			return
		}
	}
	labels, e := h.actorLabels(r, p)
	if e != nil {
		w.Header().Del("ETag")
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load the changes"})
		return
	}
	page, e := h.services.Audit.Changes(r.Context(), p, in, labels)
	if e != nil {
		w.Header().Del("ETag")
		respond(w, http.StatusBadRequest, map[string]string{"error": e.Error()})
		return
	}
	// Summaries quote the bank's payee text after " for "; show the alias instead when one matches.
	if name, e := h.services.Semantic.PayeeNamer(r.Context(), p); e == nil {
		alias := func(text string) string {
			if i := strings.LastIndex(text, " for "); i >= 0 {
				return text[:i+5] + name(text[i+5:])
			}
			return text
		}
		for i := range page.Groups {
			g := &page.Groups[i]
			g.Summary = alias(g.Summary)
			for j := range g.Events {
				g.Events[j].Summary = alias(g.Events[j].Summary)
			}
		}
	}
	respond(w, http.StatusOK, page)
}

func (h *Handler) apiFeedback(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in feedback.FlagInput
	if e := decode(r, &in); e != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid flag"})
		return
	}
	flag, e := h.services.Feedback.Flag(r.Context(), p, in)
	if e != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": e.Error()})
		return
	}
	respond(w, http.StatusCreated, flag)
}

func (h *Handler) apiUsage(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in feedback.UsageBeacon
	if e := decode(r, &in); e != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid usage beacon"})
		return
	}
	if e := h.services.Feedback.Record(r.Context(), p, in); e != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": e.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) apiSavedViews(w http.ResponseWriter, r *http.Request) {
	p, _, e := h.session(r)
	if e != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "sign in to continue"})
		return
	}
	views, e := h.services.Feedback.SavedViews(r.Context(), p)
	if e != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "could not load saved views"})
		return
	}
	respond(w, http.StatusOK, map[string]any{"views": views})
}

func (h *Handler) apiSavedViewPut(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var in feedback.SavedView
	if e := decode(r, &in); e != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid saved view"})
		return
	}
	view, e := h.services.Feedback.SaveView(r.Context(), p, in)
	if e != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": e.Error()})
		return
	}
	respond(w, http.StatusOK, view)
}

func (h *Handler) apiSavedViewDelete(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if e := h.services.Feedback.DeleteView(r.Context(), p, r.URL.Query().Get("id")); e != nil {
		respond(w, http.StatusNotFound, map[string]string{"error": "view not found"})
		return
	}
	respond(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) settingsAgentsStop(w http.ResponseWriter, r *http.Request) {
	h.agentsSwitch(w, r, true)
}
func (h *Handler) settingsAgentsResume(w http.ResponseWriter, r *http.Request) {
	h.agentsSwitch(w, r, false)
}

func (h *Handler) agentsSwitch(w http.ResponseWriter, r *http.Request, suspended bool) {
	p, ok := h.settingsPrincipal(w, r)
	if !ok {
		return
	}
	if e := h.services.Admin.SetAgentsSuspended(r.Context(), p, suspended); e != nil {
		status := http.StatusInternalServerError
		if errors.Is(e, principal.ErrForbidden) {
			status = http.StatusForbidden
		}
		settingsError(w, r, status, "only the owner can do this")
		return
	}
	if settingsJSON(r) {
		respond(w, http.StatusOK, map[string]bool{"agents_suspended": suspended})
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

type confirmData struct {
	Title, Sentence, Reason, Requester, CSRF, Error, Action string
	Done                                                    bool
}

func (h *Handler) confirmRender(w http.ResponseWriter, status int, data confirmData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if e := h.templates.ExecuteTemplate(w, "confirm.html", data); e != nil {
		h.services.Logger.Error("render confirmation", "error", e)
	}
}

// confirmPage is the owner's view of one exact action. It reads the request from the server, so an agent cannot reword what is being approved.
func (h *Handler) confirmPage(w http.ResponseWriter, r *http.Request) {
	p, session, e := h.session(r)
	if e != nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	c, e := h.services.Admin.Confirmation(r.Context(), p, r.PathValue("token"))
	if e != nil {
		h.confirmRender(w, http.StatusNotFound, confirmData{Title: "This link has ended", Error: "It was used, has expired, or belongs to another account. Ask the agent for a new one.", Done: true})
		return
	}
	labels, e := h.actorLabels(r, p)
	if e != nil {
		http.Error(w, "could not load this request; try again", http.StatusInternalServerError)
		return
	}
	h.confirmRender(w, http.StatusOK, confirmData{Title: "Approve this action?", Sentence: c.Sentence(), Reason: c.Reason, Requester: labels(c.RequestedBy), CSRF: session.CSRF, Action: "/confirm/" + url.PathEscape(r.PathValue("token"))})
}

func (h *Handler) confirmDecide(w http.ResponseWriter, r *http.Request) {
	p, session, e := h.session(r)
	if e != nil || !h.csrf(r, session) {
		http.Error(w, "reload and retry", http.StatusForbidden)
		return
	}
	decision := r.FormValue("decision")
	if decision != "allow" && decision != "deny" {
		http.Error(w, "choose allow or deny", http.StatusBadRequest)
		return
	}
	c, e := h.services.Admin.Decide(r.Context(), p, r.PathValue("token"), decision == "allow")
	if e != nil {
		status := http.StatusInternalServerError
		if errors.Is(e, principal.ErrNotFound) || errors.Is(e, principal.ErrForbidden) {
			status = http.StatusNotFound
		}
		h.services.Logger.Warn("confirmation failed", "error", e)
		h.confirmRender(w, status, confirmData{Title: "This did not go through", Error: "The link was used, has expired, or the action could not finish. Check Changes, then ask the agent for a new link.", Done: true})
		return
	}
	title := "Declined"
	if decision == "allow" {
		title = "Done"
	}
	h.confirmRender(w, http.StatusOK, confirmData{Title: title, Sentence: c.Sentence(), Done: true})
}

// feedSecretPage is the only place a bank credential is entered; the agent that set up the feed only ever holds this link.
func (h *Handler) feedSecretPage(w http.ResponseWriter, r *http.Request) {
	_, session, e := h.session(r)
	if e != nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	h.feedSecretRender(w, http.StatusOK, r.PathValue("id"), session.CSRF, "")
}

func (h *Handler) feedSecretRender(w http.ResponseWriter, status int, id, csrf, problem string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if e := h.templates.ExecuteTemplate(w, "feed_secret.html", map[string]string{"Action": "/secure/feed/" + url.PathEscape(id), "CSRF": csrf, "Error": problem}); e != nil {
		h.services.Logger.Error("render feed secret page", "error", e)
	}
}

func (h *Handler) feedSecretSave(w http.ResponseWriter, r *http.Request) {
	p, session, e := h.session(r)
	if e != nil || !h.csrf(r, session) {
		http.Error(w, "reload and retry", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if e = h.services.Imports.SetFeedSecret(r.Context(), p, id, r.FormValue("token")); e != nil {
		h.feedSecretRender(w, http.StatusBadRequest, id, session.CSRF, "That could not be saved. Check that the agent has set up this feed, then try again.")
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
