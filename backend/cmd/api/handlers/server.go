package handlers

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed web/*
var webFS embed.FS

var assetPaths = func() map[string]string {
	data, err := webFS.ReadFile("web/preview-auth.css")
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(data)
	legacy := fmt.Sprintf("/assets/style.%x.css", sum[:12])
	auth, err := authStylesheet()
	if err != nil {
		auth = legacy
	}
	return map[string]string{"style.css": legacy, "auth.css": auth}
}()

func parseTemplates() (*template.Template, error) {
	return template.New("").Funcs(template.FuncMap{"asset": func(name string) string { return assetPaths[name] }}).ParseFS(webFS, "web/*.html")
}

type Config struct {
	PublicURL, SetupToken, Version string
	Toolsets                       map[string]bool
}
type Handler struct {
	authLimit      authLimiter
	cfg            Config
	services       mcpserver.Services
	oauth          *oauth.Service
	ledgerRepo     *ledger.MongoRepository
	client         *mongo.Client
	templates      *template.Template
	full, readonly *mcp.Server
}

func New(cfg Config, s mcpserver.Services, oa *oauth.Service, repo *ledger.MongoRepository, client *mongo.Client) *Handler {
	h := &Handler{cfg: cfg, services: s, oauth: oa, ledgerRepo: repo, client: client, templates: template.Must(parseTemplates())}
	h.full = mcpserver.New(s, mcpserver.Options{Version: cfg.Version, Toolsets: cfg.Toolsets})
	h.readonly = mcpserver.New(s, mcpserver.Options{Version: cfg.Version, ReadOnly: true, Toolsets: cfg.Toolsets})
	return h
}
func (h *Handler) RemoteMCP(readOnly bool) http.Handler {
	server := h.full
	path := "/mcp"
	if readOnly {
		server = h.readonly
		path += "/readonly"
	}
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 12 * 1024 * 1024})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.security(w)
		w.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); origin != "" && !h.allowedOrigin(origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(raw, "Bearer ")) == "" {
			h.unauthorized(w, path)
			return
		}
		p, e := h.oauth.Bearer(r.Context(), strings.TrimPrefix(raw, "Bearer "), h.cfg.PublicURL+path)
		if e != nil {
			h.unauthorized(w, path)
			return
		}
		transport.ServeHTTP(w, r.WithContext(principal.With(r.Context(), p)))
	})
}
func (h *Handler) unauthorized(w http.ResponseWriter, path string) {
	// Connectors request exactly the challenged scopes, so the full endpoint must advertise all of them.
	scope := strings.Join(principal.Scopes, " ")
	if strings.HasSuffix(path, "/readonly") {
		scope = "ledger:read"
	}
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+h.cfg.PublicURL+"/.well-known/oauth-protected-resource"+path+`", scope="`+scope+`"`)
	respond(w, 401, map[string]string{"error": "invalid_token"})
}
func (h *Handler) allowedOrigin(origin string) bool {
	if origin == h.cfg.PublicURL {
		return true
	}
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Path != "" || u.User != nil {
		return false
	}
	switch u.Host {
	case "chatgpt.com", "chat.openai.com", "grok.com", "claude.ai", "claude.com":
		return true
	}
	return false
}
func (h *Handler) security(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
}
func (h *Handler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /previews/{file}", h.preview)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if h.client.Ping(ctx, nil) != nil {
			respond(w, 503, map[string]string{"status": "unhealthy"})
			return
		}
		respond(w, 200, map[string]string{"status": "ok", "version": h.cfg.Version})
	})
	mux.HandleFunc("GET /assets/{file}", func(w http.ResponseWriter, r *http.Request) {
		if serveUIAsset(w, r) {
			return
		}
		requested := r.PathValue("file")
		if requested != "style.css" && "/assets/"+requested != assetPaths["style.css"] {
			http.NotFound(w, r)
			return
		}
		b, e := webFS.ReadFile("web/preview-auth.css")
		if e != nil {
			http.NotFound(w, r)
			return
		}
		if requested != "style.css" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(b)
	})
	metadata := func(w http.ResponseWriter, _ *http.Request) { respond(w, 200, h.oauth.Metadata()) }
	// RFC 8414 and RFC 9728 clients probe both the root and the resource-suffixed forms.
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", metadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/mcp", metadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/mcp/readonly", metadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) { respond(w, 200, h.oauth.ProtectedResource("/mcp")) })
	// Discovery clients treat a login redirect as a broken server; unknown documents are a plain JSON 404.
	mux.HandleFunc("GET /.well-known/", func(w http.ResponseWriter, _ *http.Request) { respond(w, 404, map[string]string{"error": "not_found"}) })
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) { respond(w, 200, h.oauth.ProtectedResource("/mcp")) })
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp/readonly", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 200, h.oauth.ProtectedResource("/mcp/readonly"))
	})
	mux.HandleFunc("POST /oauth/register", h.registerClient)
	mux.HandleFunc("GET /oauth/authorize", h.authorizePage)
	mux.HandleFunc("POST /oauth/authorize", h.authorize)
	mux.HandleFunc("POST /oauth/token", h.token)
	mux.HandleFunc("POST /oauth/revoke", h.revoke)
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.login)
	mux.HandleFunc("GET /setup", h.setupPage)
	mux.HandleFunc("POST /setup", h.setup)
	mux.HandleFunc("GET /register", h.registerPage)
	mux.HandleFunc("POST /register", h.registerUser)
	mux.HandleFunc("POST /logout", h.logout)
	mux.HandleFunc("POST /settings/invite", h.settingsInvite)
	mux.HandleFunc("POST /settings/revoke", h.settingsRevoke)
	mux.HandleFunc("POST /settings/password", h.settingsPassword)
	mux.HandleFunc("POST /settings/feed", h.settingsFeed)
	browserTransport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return h.full }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 12 * 1024 * 1024})
	mux.Handle("POST /api/mcp", h.browser(browserTransport))
	mux.Handle("POST /api/expr/check", h.browser(http.HandlerFunc(h.apiExpressionCheck)))
	mux.Handle("POST /api/dashboards/{id}/validate", h.browser(http.HandlerFunc(h.apiDashboardValidate)))
	mux.HandleFunc("GET /api/expr/catalog", h.apiExpressionCatalog)
	mux.HandleFunc("GET /api/session", h.apiSession)
	mux.HandleFunc("GET /api/imports", h.apiImports)
	mux.HandleFunc("GET /api/imports/count", h.apiImportCount)
	mux.HandleFunc("GET /api/reconcile/count", h.apiReconcileCount)
	mux.HandleFunc("GET /api/settings", h.apiSettings)
	mux.HandleFunc("GET /api/activity/agents", h.apiActivityAgents)
	mux.HandleFunc("GET /api/shared/{id}", h.apiShared)
	mux.HandleFunc("GET /api/shared/{id}/draft", h.apiSharedDraft)
	mux.HandleFunc("GET /api/shared/{id}/catalog", h.apiSharedCatalog)
	mux.Handle("POST /api/shared/{id}/patch", h.browser(http.HandlerFunc(h.apiSharedPatch)))
	mux.Handle("POST /api/shared/{id}/publish", h.browser(http.HandlerFunc(h.apiSharedPublish)))
	mux.Handle("POST /api/shared/{id}/undo", h.browser(http.HandlerFunc(h.apiSharedUndo)))
	mux.Handle("POST /api/shared/{id}/evaluate", h.browser(http.HandlerFunc(h.apiSharedEvaluate)))
	mux.Handle("POST /api/shared/{id}/validate", h.browser(http.HandlerFunc(h.apiSharedValidate)))
	mux.HandleFunc("GET /api/consistency", h.consistency)
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		mux.HandleFunc(method+" /api/", func(w http.ResponseWriter, _ *http.Request) {
			respond(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		})
	}
	for _, prefix := range []string{"GET /mcp", "GET /mcp/", "GET /oauth/", "GET /previews/"} {
		mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	}
	mux.HandleFunc("GET /", serveUI)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.security(w)
		w.Header().Set("Cache-Control", "no-store")
		if h.limited(w, r) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 12*1024*1024)
		mux.ServeHTTP(w, r)
	})
}
func (h *Handler) session(r *http.Request) (principal.Principal, identity.Session, error) {
	c, e := r.Cookie("moneybag_session")
	if e != nil {
		return principal.Principal{}, identity.Session{}, principal.ErrForbidden
	}
	return h.services.Identity.Session(r.Context(), c.Value)
}
func (h *Handler) csrf(r *http.Request, session identity.Session) bool {
	if origin := r.Header.Get("Origin"); origin != "" && origin != h.cfg.PublicURL {
		return false
	}
	token := r.Header.Get("X-CSRF-Token")
	if token == "" {
		token = r.FormValue("csrf")
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(session.CSRF)) == 1 && token != ""
}
func (h *Handler) browser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, session, e := h.session(r)
		if e != nil {
			respond(w, 401, map[string]string{"error": "sign in to continue"})
			return
		}
		if r.Method != "POST" || !h.csrf(r, session) {
			respond(w, 403, map[string]string{"error": "reload this page and retry"})
			return
		}
		next.ServeHTTP(w, r.WithContext(principal.With(r.Context(), p)))
	})
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 12*1024*1024))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("one JSON object required")
	}
	return nil
}

func Stylesheet() string { b, _ := webFS.ReadFile("web/preview-auth.css"); return string(b) }

// consistency is a read-only GET for the signed-in session, so no CSRF token is needed.
func (h *Handler) consistency(w http.ResponseWriter, r *http.Request) {
	p, _, e := h.session(r)
	if e != nil {
		respond(w, 401, map[string]string{"error": "sign in to continue"})
		return
	}
	res, e := h.services.Consistency.Run(r.Context(), p)
	if e != nil {
		respond(w, 500, map[string]string{"error": e.Error()})
		return
	}
	respond(w, 200, res)
}
