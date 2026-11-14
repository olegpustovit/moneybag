package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/safefetch"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

var ErrInvalidRequest = errors.New("invalid_request")
var ErrInvalidClient = errors.New("invalid_client")
var ErrInvalidGrant = errors.New("invalid_grant")
var ErrInvalidScope = errors.New("invalid_scope")
var ErrInvalidTarget = errors.New("invalid_target")

// ErrAgentsSuspended is the owner's emergency stop, reported as such so an agent knows to stop retrying and ask a person.
var ErrAgentsSuspended = errors.New("the owner has suspended all agents; ask them to re-enable agents in Settings")
var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
var challengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

const AccessTTL = 15 * time.Minute
const RefreshTTL = 30 * 24 * time.Hour

type Client struct {
	ID           string    `json:"client_id" db:"id"`
	Name         string    `json:"client_name" db:"name"`
	RedirectURIs []string  `json:"redirect_uris" db:"redirect_uris"`
	AuthMethod   string    `json:"token_endpoint_auth_method" db:"auth_method"`
	SecretHash   string    `json:"-" db:"secret_hash"`
	FetchedUntil time.Time `json:"-" db:"fetched_until"`
}
type RegisterInput struct {
	Name          string   `json:"client_name"`
	RedirectURIs  []string `json:"redirect_uris"`
	AuthMethod    string   `json:"token_endpoint_auth_method"`
	GrantTypes    []string `json:"grant_types,omitempty"`
	ResponseTypes []string `json:"response_types,omitempty"`
}
type Registration struct {
	Client
	Secret        string   `json:"client_secret,omitempty"`
	IssuedAt      int64    `json:"client_id_issued_at"`
	SecretExpires int64    `json:"client_secret_expires_at"`
	GrantTypes    []string `json:"grant_types"`
	ResponseTypes []string `json:"response_types"`
}
type Authorization struct {
	ClientID        string `json:"client_id"`
	RedirectURI     string `json:"redirect_uri"`
	ResponseType    string `json:"response_type"`
	Scope           string `json:"scope"`
	State           string `json:"state"`
	Challenge       string `json:"code_challenge"`
	ChallengeMethod string `json:"code_challenge_method"`
	Resource        string `json:"resource"`
}
type code struct {
	Hash      string    `db:"id"`
	ClientID  string    `db:"client_id"`
	UserID    string    `db:"user_id"`
	Redirect  string    `db:"redirect_uri"`
	Challenge string    `db:"challenge"`
	Resource  string    `db:"resource"`
	Scopes    []string  `db:"scopes"`
	ExpiresAt time.Time `db:"expires_at"`
}
type Grant struct {
	ID         string    `json:"id" db:"id"`
	UserID     string    `json:"-" db:"user_id"`
	ClientID   string    `json:"client_id" db:"client_id"`
	ClientName string    `json:"client_name" db:"client_name"`
	Resource   string    `json:"resource" db:"resource"`
	Scopes     []string  `json:"scopes" db:"scopes"`
	Revoked    bool      `json:"revoked" db:"revoked"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	ExpiresAt  time.Time `json:"expires_at" db:"expires_at"`
}
type token struct {
	Hash            string    `db:"id"`
	RefreshHash     string    `db:"refresh_hash"`
	GrantID         string    `db:"grant_id"`
	AccessExpiresAt time.Time `db:"access_expires_at"`
	ExpiresAt       time.Time `db:"expires_at"`
	Used            bool      `db:"used"`
}
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}
type PostgresRepository struct {
	db *pgxpool.Pool
}
type Service struct {
	repo     *PostgresRepository
	identity *identity.Service
	Issuer   string
	http     *http.Client
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db}
}
func NewService(r *PostgresRepository, i *identity.Service, issuer string) *Service {
	return &Service{r, i, strings.TrimRight(issuer, "/"), safefetch.Client()}
}
func (s *Service) Init(context.Context) error { return nil }

// AllowedRedirect accepts any HTTPS callback, plain HTTP only on loopback, and native-app custom schemes
// (RFC 8252 §7.1, e.g. cursor://). The consent page shows the redirect target so users can spot a spoofed client.
func AllowedRedirect(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 2048 || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || !redirectScheme.MatchString(u.Scheme) {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch u.Scheme {
	case "https":
		return host != ""
	case "http":
		return host == "127.0.0.1" || host == "localhost" || host == "::1"
	case "javascript", "data", "file", "vbscript", "blob", "about", "ftp", "ws", "wss":
		return false
	}
	return u.Opaque == "" && (host != "" || u.Path != "")
}

var redirectScheme = regexp.MustCompile(`^[a-z][a-z0-9+.-]{1,63}$`)

// RedirectMatches ignores the port on loopback redirects, as RFC 8252 §7.3 requires for native clients.
func RedirectMatches(registered []string, raw string) bool {
	if slices.Contains(registered, raw) {
		return true
	}
	got, e := url.Parse(raw)
	if e != nil || got.Scheme != "http" {
		return false
	}
	host := strings.ToLower(got.Hostname())
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return false
	}
	for _, r := range registered {
		u, e := url.Parse(r)
		if e == nil && u.Scheme == "http" && strings.ToLower(u.Hostname()) == host && u.Path == got.Path && u.RawQuery == got.RawQuery {
			return true
		}
	}
	return false
}
func (s *Service) Register(ctx context.Context, in RegisterInput) (Registration, error) {
	if len(in.RedirectURIs) < 1 || len(in.RedirectURIs) > 10 {
		return Registration{}, fmt.Errorf("%w: 1..10 redirect_uris required", ErrInvalidRequest)
	}
	if r := []rune(in.Name); len(r) > 100 {
		in.Name = string(r[:100])
	}
	for _, r := range in.RedirectURIs {
		if !AllowedRedirect(r) {
			return Registration{}, fmt.Errorf("%w: redirect_uri %q must be https, loopback http or a native app scheme", ErrInvalidRequest, r)
		}
	}
	if len(in.GrantTypes) > 0 && !slices.Contains(in.GrantTypes, "authorization_code") {
		return Registration{}, fmt.Errorf("%w: grant_types must include authorization_code", ErrInvalidRequest)
	}
	for _, r := range in.ResponseTypes {
		if r != "code" {
			return Registration{}, fmt.Errorf("%w: only the code response type is supported", ErrInvalidRequest)
		}
	}
	if in.AuthMethod == "" {
		in.AuthMethod = "client_secret_basic"
	}
	if !slices.Contains([]string{"none", "client_secret_basic", "client_secret_post"}, in.AuthMethod) {
		return Registration{}, fmt.Errorf("%w: token_endpoint_auth_method must be none, client_secret_basic or client_secret_post", ErrInvalidClient)
	}
	if in.Name == "" {
		in.Name = "MCP agent"
	}
	c := Client{ID: "mb_client_" + identity.Random(), Name: in.Name, RedirectURIs: in.RedirectURIs, AuthMethod: in.AuthMethod}
	secret := ""
	if c.AuthMethod != "none" {
		secret = identity.Random()
		c.SecretHash = identity.Hash(secret)
	}
	const registerSQL = `INSERT INTO oauth_clients (id,name,redirect_uris,auth_method,secret_hash,fetched_until) VALUES (@id,@name,@redirects,@method,@secret,@until)`
	_, e := s.repo.db.Exec(ctx, registerSQL, pgx.NamedArgs{"id": c.ID, "name": c.Name, "redirects": c.RedirectURIs, "method": c.AuthMethod, "secret": c.SecretHash, "until": c.FetchedUntil.UTC().Truncate(time.Millisecond)})
	return Registration{c, secret, time.Now().Unix(), 0, []string{"authorization_code", "refresh_token"}, []string{"code"}}, e
}
func (s *Service) client(ctx context.Context, id string) (Client, error) {
	var c Client
	const clientSQL = `SELECT * FROM oauth_clients WHERE id=@id`
	rows, e := s.repo.db.Query(ctx, clientSQL, pgx.NamedArgs{"id": id})
	if e == nil {
		c, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[Client])
	}
	remote := strings.HasPrefix(id, "https://")
	if e == nil && (!remote || c.FetchedUntil.After(time.Now())) {
		return c, nil
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return c, e
	}
	if !remote || !AllowedRedirect(id) {
		return Client{}, ErrInvalidClient
	}
	b, h, e := safefetch.Read(ctx, s.http, id, 64*1024)
	if e != nil {
		return Client{}, ErrInvalidClient
	}
	if json.Unmarshal(b, &c) != nil || c.ID != id || c.AuthMethod != "none" || len(c.RedirectURIs) < 1 || len(c.RedirectURIs) > 10 {
		return Client{}, ErrInvalidClient
	}
	for _, r := range c.RedirectURIs {
		if !AllowedRedirect(r) {
			return Client{}, ErrInvalidClient
		}
	}
	ttl, noStore := clientCacheTTL(h, time.Now())
	c.SecretHash = ""
	c.FetchedUntil = time.Now().Add(ttl)
	if noStore {
		_, e = s.repo.db.Exec(ctx, `DELETE FROM oauth_clients WHERE id=@id`, pgx.NamedArgs{"id": id})
		return c, e
	}
	_, e = s.repo.db.Exec(ctx, `INSERT INTO oauth_clients (id,name,redirect_uris,auth_method,secret_hash,fetched_until) VALUES (@id,@name,@redirects,@method,@secret,@until)
 ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name,redirect_uris=EXCLUDED.redirect_uris,auth_method=EXCLUDED.auth_method,secret_hash=EXCLUDED.secret_hash,fetched_until=EXCLUDED.fetched_until`, pgx.NamedArgs{"id": c.ID, "name": c.Name, "redirects": c.RedirectURIs, "method": c.AuthMethod, "secret": c.SecretHash, "until": c.FetchedUntil.UTC().Truncate(time.Millisecond)})
	return c, e
}
func (s *Service) resource(raw string) bool {
	return raw == s.Issuer+"/mcp" || raw == s.Issuer+"/mcp/readonly"
}
func (s *Service) Validate(ctx context.Context, in Authorization) (Client, []string, error) {
	c, e := s.client(ctx, in.ClientID)
	if e != nil {
		return c, nil, e
	}
	if !RedirectMatches(c.RedirectURIs, in.RedirectURI) || in.ResponseType != "code" || in.ChallengeMethod != "S256" || !challengePattern.MatchString(in.Challenge) || len(in.State) > 2048 {
		return c, nil, ErrInvalidRequest
	}
	if !s.resource(in.Resource) {
		return c, nil, ErrInvalidTarget
	}
	scopes := strings.Fields(in.Scope)
	if len(scopes) == 0 {
		// No requested scope means the endpoint's full access; the consent page lists exactly what is granted.
		scopes = slices.Clone(principal.Scopes)
		if strings.HasSuffix(in.Resource, "/readonly") {
			scopes = []string{"ledger:read"}
		}
	}
	for _, scope := range scopes {
		if !slices.Contains(principal.Scopes, scope) || (strings.HasSuffix(in.Resource, "/readonly") && scope != "ledger:read") {
			return c, nil, ErrInvalidScope
		}
	}
	return c, scopes, nil
}
func (s *Service) Authorize(ctx context.Context, in Authorization, p principal.Principal) (string, error) {
	_, scopes, e := s.Validate(ctx, in)
	if e != nil {
		return "", e
	}
	if _, e = s.identity.User(ctx, p.UserID); e != nil {
		return "", e
	}
	raw := identity.Random()
	const codeSQL = `INSERT INTO oauth_codes (id,client_id,user_id,redirect_uri,challenge,resource,scopes,expires_at) VALUES (@id,@client,@user,@redirect,@challenge,@resource,@scopes,@expires)`
	_, e = s.repo.db.Exec(ctx, codeSQL, pgx.NamedArgs{"id": identity.Hash(raw), "client": in.ClientID, "user": p.UserID, "redirect": in.RedirectURI, "challenge": in.Challenge, "resource": in.Resource, "scopes": scopes, "expires": time.Now().UTC().Add(time.Minute).Truncate(time.Millisecond)})
	return raw, e
}
func (s *Service) AuthenticateClient(ctx context.Context, id, secret, method string) (Client, error) {
	c, e := s.client(ctx, id)
	if e != nil {
		return c, e
	}
	if c.AuthMethod != method || (method == "none" && secret != "") || (method != "none" && subtle.ConstantTimeCompare([]byte(c.SecretHash), []byte(identity.Hash(secret))) != 1) {
		return Client{}, ErrInvalidClient
	}
	return c, nil
}
func Challenge(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// Exchange and Refresh treat an omitted resource (optional in RFC 8707) as the one the grant was issued for.
func (s *Service) Exchange(ctx context.Context, c Client, raw, redirect, verifier, resource string) (TokenResponse, error) {
	if resource != "" && !s.resource(resource) {
		return TokenResponse{}, ErrInvalidTarget
	}
	if !verifierPattern.MatchString(verifier) {
		return TokenResponse{}, ErrInvalidGrant
	}
	if suspended, e := s.Suspended(ctx); e != nil {
		return TokenResponse{}, e
	} else if suspended {
		return TokenResponse{}, ErrAgentsSuspended
	}
	var result TokenResponse
	e := postgres.Workflow(ctx, func(tx context.Context) error {
		const exchangeSQL = `DELETE FROM oauth_codes WHERE id=@id AND client_id=@client AND redirect_uri=@redirect AND challenge=@challenge
 AND expires_at>@now AND (@resource='' OR resource=@resource) RETURNING *`
		rows, e := s.repo.db.Query(tx, exchangeSQL, pgx.NamedArgs{"id": identity.Hash(raw), "client": c.ID, "redirect": redirect, "challenge": Challenge(verifier), "now": time.Now().UTC().Truncate(time.Millisecond), "resource": resource})
		if e != nil {
			return e
		}
		auth, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[code])
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrInvalidGrant
		}
		if e != nil {
			return e
		}
		g := Grant{ID: identity.Random(), UserID: auth.UserID, ClientID: c.ID, ClientName: c.Name, Resource: auth.Resource, Scopes: auth.Scopes, CreatedAt: time.Now().UTC().Truncate(time.Millisecond), ExpiresAt: time.Now().UTC().Add(RefreshTTL).Truncate(time.Millisecond)}
		if _, e = s.repo.db.Exec(tx, `INSERT INTO oauth_grants (id,user_id,client_id,client_name,resource,scopes,revoked,created_at,expires_at) VALUES (@id,@user,@client,@name,@resource,@scopes,@revoked,@created,@expires)`, pgx.NamedArgs{"id": g.ID, "user": g.UserID, "client": g.ClientID, "name": g.ClientName, "resource": g.Resource, "scopes": g.Scopes, "revoked": g.Revoked, "created": g.CreatedAt, "expires": g.ExpiresAt}); e != nil {
			return e
		}
		result, e = s.issue(tx, g)
		return e
	})
	return result, e
}
func (s *Service) issue(ctx context.Context, g Grant) (TokenResponse, error) {
	access, refresh := identity.Random(), identity.Random()
	const issueSQL = `INSERT INTO oauth_tokens (id,refresh_hash,grant_id,access_expires_at,expires_at,used) VALUES (@id,@refresh,@grant,@access,@expires,false)`
	_, e := s.repo.db.Exec(ctx, issueSQL, pgx.NamedArgs{"id": identity.Hash(access), "refresh": identity.Hash(refresh), "grant": g.ID, "access": time.Now().UTC().Add(AccessTTL).Truncate(time.Millisecond), "expires": g.ExpiresAt})
	return TokenResponse{access, "Bearer", int64(AccessTTL.Seconds()), refresh, strings.Join(g.Scopes, " ")}, e
}
func (s *Service) Refresh(ctx context.Context, c Client, raw, resource, scope string) (TokenResponse, error) {
	if resource != "" && !s.resource(resource) {
		return TokenResponse{}, ErrInvalidTarget
	}
	if suspended, e := s.Suspended(ctx); e != nil {
		return TokenResponse{}, e
	} else if suspended {
		return TokenResponse{}, ErrAgentsSuspended
	}
	var result TokenResponse
	reused := false
	e := postgres.Workflow(ctx, func(tx context.Context) error {
		const refreshSQL = `SELECT * FROM oauth_tokens WHERE refresh_hash=@refresh AND expires_at>@now`
		rows, e := s.repo.db.Query(tx, refreshSQL, pgx.NamedArgs{"refresh": identity.Hash(raw), "now": time.Now().UTC().Truncate(time.Millisecond)})
		if e != nil {
			return e
		}
		tok, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[token])
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrInvalidGrant
		}
		if e != nil {
			return e
		}
		g, e := s.grant(tx, tok.GrantID)
		if e != nil || g.ClientID != c.ID || resource != "" && g.Resource != resource {
			return ErrInvalidGrant
		}
		if tok.Used {
			reused = true
			_, e = s.repo.db.Exec(tx, `UPDATE oauth_grants SET revoked=true WHERE id=@id`, pgx.NamedArgs{"id": g.ID})
			return e
		}
		if scope != "" {
			for _, x := range strings.Fields(scope) {
				if !slices.Contains(g.Scopes, x) {
					return ErrInvalidScope
				}
			}
			g.Scopes = strings.Fields(scope)
		}
		r, e := s.repo.db.Exec(tx, `UPDATE oauth_tokens SET used=true WHERE id=@id AND used=false`, pgx.NamedArgs{"id": tok.Hash})
		if e != nil {
			return e
		}
		if r.RowsAffected() != 1 {
			return ErrInvalidGrant
		}
		result, e = s.issue(tx, g)
		return e
	})
	if reused && e == nil {
		return TokenResponse{}, ErrInvalidGrant
	}
	return result, e
}
func (s *Service) grant(ctx context.Context, id string) (Grant, error) {
	const grantSQL = `SELECT * FROM oauth_grants WHERE id=@id AND revoked=false AND expires_at>@now`
	rows, e := s.repo.db.Query(ctx, grantSQL, pgx.NamedArgs{"id": id, "now": time.Now().UTC().Truncate(time.Millisecond)})
	if e != nil {
		return Grant{}, e
	}
	g, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[Grant])
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrInvalidGrant
	}
	return g, e
}
func (s *Service) Bearer(ctx context.Context, raw, resource string) (principal.Principal, error) {
	const bearerSQL = `SELECT g.id,g.user_id,g.client_id,g.scopes,g.resource,u.id,u.is_admin,u.disabled,
 EXISTS(SELECT 1 FROM settings WHERE id='agents_suspended')
 FROM oauth_tokens t JOIN oauth_grants g ON g.id=t.grant_id LEFT JOIN users u ON u.id=g.user_id
 WHERE t.id=@id AND t.access_expires_at>@now AND g.revoked=false AND g.expires_at>@now`
	var result struct{ Grant Grant }
	var userID *string
	var userAdmin, userDisabled *bool
	var suspended bool
	err := s.repo.db.QueryRow(ctx, bearerSQL, pgx.NamedArgs{"id": identity.Hash(raw), "now": time.Now().UTC().Truncate(time.Millisecond)}).Scan(
		&result.Grant.ID, &result.Grant.UserID, &result.Grant.ClientID, &result.Grant.Scopes, &result.Grant.Resource, &userID, &userAdmin, &userDisabled, &suspended)
	if err != nil || result.Grant.Resource != resource {
		return principal.Principal{}, principal.ErrForbidden
	}
	if userID == nil || *userID != result.Grant.UserID {
		return principal.Principal{}, principal.ErrNotFound
	}
	if *userDisabled {
		return principal.Principal{}, principal.ErrForbidden
	}
	if suspended {
		return principal.Principal{}, ErrAgentsSuspended
	}
	scopes := result.Grant.Scopes
	admin := slices.Contains(scopes, principal.ScopeAdminUsers)
	if admin && !*userAdmin {
		// A grant cannot outlive its user's admin rights.
		scopes = slices.DeleteFunc(slices.Clone(scopes), func(scope string) bool { return scope == principal.ScopeAdminUsers })
		admin = false
	}
	return principal.Principal{UserID: result.Grant.UserID, Scopes: scopes, Actor: principal.Actor{Kind: "agent", ID: result.Grant.ClientID}, Admin: admin, GrantID: result.Grant.ID}, nil
}

// Suspended reports the owner's emergency stop.
func (s *Service) Suspended(ctx context.Context) (bool, error) {
	var suspended bool
	e := s.repo.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM settings WHERE id='agents_suspended')`).Scan(&suspended)
	return suspended, e
}

// SetSuspended flips the emergency stop; grants themselves stay untouched, so resuming restores every connection.
func (s *Service) SetSuspended(ctx context.Context, by string, suspended bool) error {
	if !suspended {
		_, e := s.repo.db.Exec(ctx, `DELETE FROM settings WHERE id='agents_suspended'`)
		return e
	}
	_, e := s.repo.db.Exec(ctx, `INSERT INTO settings (id,"by",at) VALUES ('agents_suspended',@by,@at) ON CONFLICT (id) DO UPDATE SET "by"=EXCLUDED."by",at=EXCLUDED.at`, pgx.NamedArgs{"by": by, "at": time.Now().UTC().Truncate(time.Millisecond)})
	return e
}

// OwnedGrant reads one active connection of this user.
func (s *Service) OwnedGrant(ctx context.Context, p principal.Principal, id string) (Grant, error) {
	const ownedSQL = `SELECT * FROM oauth_grants WHERE id=@id AND user_id=@user AND revoked=false AND expires_at>@now`
	rows, e := s.repo.db.Query(ctx, ownedSQL, pgx.NamedArgs{"id": id, "user": p.UserID, "now": time.Now().UTC().Truncate(time.Millisecond)})
	if e != nil {
		return Grant{}, e
	}
	g, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[Grant])
	if errors.Is(e, pgx.ErrNoRows) {
		e = principal.ErrNotFound
	}
	return g, e
}

// AddScope widens one connection. Only the admin service's owner confirmation reaches it.
func (s *Service) AddScope(ctx context.Context, p principal.Principal, id, scope string) error {
	r, e := s.repo.db.Exec(ctx, `UPDATE oauth_grants SET scopes=CASE WHEN @scope=ANY(scopes) THEN scopes ELSE array_append(scopes,@scope) END WHERE id=@id AND user_id=@user AND revoked=false`, pgx.NamedArgs{"scope": scope, "id": id, "user": p.UserID})
	if e == nil && r.RowsAffected() != 1 {
		return principal.ErrNotFound
	}
	return e
}
func (s *Service) Revoke(ctx context.Context, c Client, raw string) error {
	const revokeSQL = `SELECT * FROM oauth_tokens WHERE id=@hash OR refresh_hash=@hash LIMIT 1`
	rows, e := s.repo.db.Query(ctx, revokeSQL, pgx.NamedArgs{"hash": identity.Hash(raw)})
	if e != nil {
		return e
	}
	tok, e := pgx.CollectOneRow(rows, pgx.RowToStructByName[token])
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	_, e = s.repo.db.Exec(ctx, `UPDATE oauth_grants SET revoked=true WHERE id=@id AND client_id=@client`, pgx.NamedArgs{"id": tok.GrantID, "client": c.ID})
	return e
}
func (s *Service) Grants(ctx context.Context, p principal.Principal) ([]Grant, error) {
	const grantsSQL = `SELECT * FROM oauth_grants WHERE user_id=@user AND revoked=false AND expires_at>@now ORDER BY created_at DESC LIMIT 200`
	rows, e := s.repo.db.Query(ctx, grantsSQL, pgx.NamedArgs{"user": p.UserID, "now": time.Now().UTC().Truncate(time.Millisecond)})
	if e != nil {
		return nil, e
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[Grant])
}

type AgentName struct {
	ClientID   string `json:"client_id"`
	ClientName string `json:"client_name"`
}

// AgentNames includes expired and revoked grants so past audit events remain identifiable.
func (s *Service) AgentNames(ctx context.Context, p principal.Principal) ([]AgentName, error) {
	if err := p.Require("ledger:read"); err != nil {
		return nil, err
	}
	const namesSQL = `SELECT id,client_id,client_name,created_at FROM oauth_grants WHERE user_id=@user`
	rows, err := s.repo.db.Query(ctx, namesSQL, pgx.NamedArgs{"user": p.UserID})
	if err != nil {
		return nil, fmt.Errorf("find activity agents: %w", err)
	}
	defer rows.Close()
	latest := map[string]Grant{}
	for rows.Next() {
		var grant Grant
		if err := rows.Scan(&grant.ID, &grant.ClientID, &grant.ClientName, &grant.CreatedAt); err != nil {
			return nil, fmt.Errorf("decode activity agent: %w", err)
		}
		if grant.ClientID == "" || grant.ClientName == "" {
			continue
		}
		old, found := latest[grant.ClientID]
		if !found || grant.CreatedAt.After(old.CreatedAt) || grant.CreatedAt.Equal(old.CreatedAt) && grant.ID > old.ID {
			latest[grant.ClientID] = grant
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read activity agents: %w", err)
	}
	out := make([]AgentName, 0, len(latest))
	for _, grant := range latest {
		out = append(out, AgentName{ClientID: grant.ClientID, ClientName: grant.ClientName})
	}
	slices.SortFunc(out, func(a, b AgentName) int { return strings.Compare(a.ClientID, b.ClientID) })
	return out, nil
}
func (s *Service) RevokeGrant(ctx context.Context, p principal.Principal, id string) error {
	r, e := s.repo.db.Exec(ctx, `UPDATE oauth_grants SET revoked=true WHERE id=@id AND user_id=@user`, pgx.NamedArgs{"id": id, "user": p.UserID})
	if e == nil && r.RowsAffected() != 1 {
		return principal.ErrNotFound
	}
	return e
}
func (s *Service) Metadata() map[string]any {
	return map[string]any{"issuer": s.Issuer, "authorization_endpoint": s.Issuer + "/oauth/authorize", "token_endpoint": s.Issuer + "/oauth/token", "registration_endpoint": s.Issuer + "/oauth/register", "revocation_endpoint": s.Issuer + "/oauth/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"}, "scopes_supported": principal.Scopes, "client_id_metadata_document_supported": true, "authorization_response_iss_parameter_supported": true}
}
func (s *Service) ProtectedResource(path string) map[string]any {
	return map[string]any{"resource": s.Issuer + path, "authorization_servers": []string{s.Issuer}, "scopes_supported": principal.Scopes, "bearer_methods_supported": []string{"header"}}
}
