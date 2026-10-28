package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/safefetch"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
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
var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
var challengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

const AccessTTL = 15 * time.Minute
const RefreshTTL = 30 * 24 * time.Hour

type Client struct {
	ID           string    `json:"client_id" bson:"_id"`
	Name         string    `json:"client_name" bson:"name"`
	RedirectURIs []string  `json:"redirect_uris" bson:"redirect_uris"`
	AuthMethod   string    `json:"token_endpoint_auth_method" bson:"auth_method"`
	SecretHash   string    `json:"-" bson:"secret_hash,omitempty"`
	FetchedUntil time.Time `json:"-" bson:"fetched_until,omitempty"`
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
	Hash      string    `bson:"_id"`
	ClientID  string    `bson:"client_id"`
	UserID    string    `bson:"user_id"`
	Redirect  string    `bson:"redirect_uri"`
	Challenge string    `bson:"challenge"`
	Resource  string    `bson:"resource"`
	Scopes    []string  `bson:"scopes"`
	ExpiresAt time.Time `bson:"expires_at"`
}
type Grant struct {
	ID         string    `json:"id" bson:"_id"`
	UserID     string    `json:"-" bson:"user_id"`
	ClientID   string    `json:"client_id" bson:"client_id"`
	ClientName string    `json:"client_name" bson:"client_name"`
	Resource   string    `json:"resource" bson:"resource"`
	Scopes     []string  `json:"scopes" bson:"scopes"`
	Revoked    bool      `json:"revoked" bson:"revoked"`
	CreatedAt  time.Time `json:"created_at" bson:"created_at"`
	ExpiresAt  time.Time `json:"expires_at" bson:"expires_at"`
}
type token struct {
	Hash            string    `bson:"_id"`
	RefreshHash     string    `bson:"refresh_hash"`
	GrantID         string    `bson:"grant_id"`
	AccessExpiresAt time.Time `bson:"access_expires_at"`
	ExpiresAt       time.Time `bson:"expires_at"`
	Used            bool      `bson:"used"`
}
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}
type MongoRepository struct {
	db     *mongo.Database
	client *mongo.Client
}
type Service struct {
	repo     *MongoRepository
	identity *identity.Service
	Issuer   string
	http     *http.Client
}

func NewMongoRepository(db *mongo.Database) *MongoRepository {
	return &MongoRepository{db, db.Client()}
}
func NewService(r *MongoRepository, i *identity.Service, issuer string) *Service {
	return &Service{r, i, strings.TrimRight(issuer, "/"), safefetch.Client()}
}
func (s *Service) Init(ctx context.Context) error {
	for c, models := range map[string][]mongo.IndexModel{
		"oauth_codes":  {{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)}},
		"oauth_tokens": {{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)}, {Keys: bson.D{{Key: "refresh_hash", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"oauth_grants": {{Keys: bson.D{{Key: "user_id", Value: 1}}}},
	} {
		if _, e := s.repo.db.Collection(c).Indexes().CreateMany(ctx, models); e != nil {
			return e
		}
	}
	return nil
}
func AllowedRedirect(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 2048 || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if (host == "127.0.0.1" || host == "localhost" || host == "::1") && (u.Scheme == "http" || u.Scheme == "https") {
		return true
	}
	if u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	for _, base := range []string{"chatgpt.com", "openai.com", "grok.com", "x.ai", "claude.ai", "claude.com"} {
		if host == base || strings.HasSuffix(host, "."+base) {
			return true
		}
	}
	return false
}
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
	if len(in.RedirectURIs) < 1 || len(in.RedirectURIs) > 10 || len(in.Name) > 100 {
		return Registration{}, ErrInvalidRequest
	}
	for _, r := range in.RedirectURIs {
		if !AllowedRedirect(r) {
			return Registration{}, ErrInvalidRequest
		}
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return Registration{}, ErrInvalidRequest
		}
	}
	for _, r := range in.ResponseTypes {
		if r != "code" {
			return Registration{}, ErrInvalidRequest
		}
	}
	if in.AuthMethod == "" {
		in.AuthMethod = "client_secret_basic"
	}
	if !slices.Contains([]string{"none", "client_secret_basic", "client_secret_post"}, in.AuthMethod) {
		return Registration{}, ErrInvalidClient
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
	_, e := s.repo.db.Collection("oauth_clients").InsertOne(ctx, c)
	return Registration{c, secret, time.Now().Unix(), 0, []string{"authorization_code", "refresh_token"}, []string{"code"}}, e
}
func (s *Service) client(ctx context.Context, id string) (Client, error) {
	var c Client
	e := s.repo.db.Collection("oauth_clients").FindOne(ctx, bson.M{"_id": id}).Decode(&c)
	remote := strings.HasPrefix(id, "https://")
	if e == nil && (!remote || c.FetchedUntil.After(time.Now())) {
		return c, nil
	}
	if e != nil && !errors.Is(e, mongo.ErrNoDocuments) {
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
		_, e = s.repo.db.Collection("oauth_clients").DeleteOne(ctx, bson.M{"_id": id})
		return c, e
	}
	_, e = s.repo.db.Collection("oauth_clients").ReplaceOne(ctx, bson.M{"_id": id}, c, options.Replace().SetUpsert(true))
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
		scopes = []string{"ledger:read"}
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
	_, e = s.repo.db.Collection("oauth_codes").InsertOne(ctx, code{identity.Hash(raw), in.ClientID, p.UserID, in.RedirectURI, in.Challenge, in.Resource, scopes, time.Now().Add(time.Minute)})
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
func (s *Service) Exchange(ctx context.Context, c Client, raw, redirect, verifier, resource string) (TokenResponse, error) {
	if !s.resource(resource) {
		return TokenResponse{}, ErrInvalidTarget
	}
	if !verifierPattern.MatchString(verifier) {
		return TokenResponse{}, ErrInvalidGrant
	}
	var result TokenResponse
	e := mongodb.Workflow(ctx, func(tx context.Context) error {
		var auth code
		e := s.repo.db.Collection("oauth_codes").FindOneAndDelete(tx, bson.M{"_id": identity.Hash(raw), "client_id": c.ID, "redirect_uri": redirect, "challenge": Challenge(verifier), "resource": resource, "expires_at": bson.M{"$gt": time.Now()}}).Decode(&auth)
		if errors.Is(e, mongo.ErrNoDocuments) {
			return ErrInvalidGrant
		}
		if e != nil {
			return e
		}
		g := Grant{ID: identity.Random(), UserID: auth.UserID, ClientID: c.ID, ClientName: c.Name, Resource: resource, Scopes: auth.Scopes, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(RefreshTTL)}
		if _, e = s.repo.db.Collection("oauth_grants").InsertOne(tx, g); e != nil {
			return e
		}
		result, e = s.issue(tx, g)
		return e
	})
	return result, e
}
func (s *Service) issue(ctx context.Context, g Grant) (TokenResponse, error) {
	access, refresh := identity.Random(), identity.Random()
	_, e := s.repo.db.Collection("oauth_tokens").InsertOne(ctx, token{identity.Hash(access), identity.Hash(refresh), g.ID, time.Now().Add(AccessTTL), g.ExpiresAt, false})
	return TokenResponse{access, "Bearer", int64(AccessTTL.Seconds()), refresh, strings.Join(g.Scopes, " ")}, e
}
func (s *Service) Refresh(ctx context.Context, c Client, raw, resource, scope string) (TokenResponse, error) {
	if !s.resource(resource) {
		return TokenResponse{}, ErrInvalidTarget
	}
	var result TokenResponse
	reused := false
	e := mongodb.Workflow(ctx, func(tx context.Context) error {
		var tok token
		e := s.repo.db.Collection("oauth_tokens").FindOne(tx, bson.M{"refresh_hash": identity.Hash(raw), "expires_at": bson.M{"$gt": time.Now()}}).Decode(&tok)
		if errors.Is(e, mongo.ErrNoDocuments) {
			return ErrInvalidGrant
		}
		if e != nil {
			return e
		}
		g, e := s.grant(tx, tok.GrantID)
		if e != nil || g.ClientID != c.ID || g.Resource != resource {
			return ErrInvalidGrant
		}
		if tok.Used {
			reused = true
			_, e = s.repo.db.Collection("oauth_grants").UpdateOne(tx, bson.M{"_id": g.ID}, bson.M{"$set": bson.M{"revoked": true}})
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
		r, e := s.repo.db.Collection("oauth_tokens").UpdateOne(tx, bson.M{"_id": tok.Hash, "used": false}, bson.M{"$set": bson.M{"used": true}})
		if e != nil {
			return e
		}
		if r.ModifiedCount != 1 {
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
	var g Grant
	e := s.repo.db.Collection("oauth_grants").FindOne(ctx, bson.M{"_id": id, "revoked": false, "expires_at": bson.M{"$gt": time.Now()}}).Decode(&g)
	if errors.Is(e, mongo.ErrNoDocuments) {
		e = ErrInvalidGrant
	}
	return g, e
}
func (s *Service) Bearer(ctx context.Context, raw, resource string) (principal.Principal, error) {
	var tok token
	e := s.repo.db.Collection("oauth_tokens").FindOne(ctx, bson.M{"_id": identity.Hash(raw), "access_expires_at": bson.M{"$gt": time.Now()}}).Decode(&tok)
	if e != nil {
		return principal.Principal{}, principal.ErrForbidden
	}
	g, e := s.grant(ctx, tok.GrantID)
	if e != nil || g.Resource != resource {
		return principal.Principal{}, principal.ErrForbidden
	}
	u, e := s.identity.User(ctx, g.UserID)
	if e != nil {
		return principal.Principal{}, e
	}
	return principal.Principal{UserID: u.ID, Scopes: g.Scopes, Actor: principal.Actor{Kind: "agent", ID: g.ClientID}}, nil
}
func (s *Service) Revoke(ctx context.Context, c Client, raw string) error {
	var tok token
	e := s.repo.db.Collection("oauth_tokens").FindOne(ctx, bson.M{"$or": bson.A{bson.M{"_id": identity.Hash(raw)}, bson.M{"refresh_hash": identity.Hash(raw)}}}).Decode(&tok)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return nil
	}
	if e != nil {
		return e
	}
	_, e = s.repo.db.Collection("oauth_grants").UpdateOne(ctx, bson.M{"_id": tok.GrantID, "client_id": c.ID}, bson.M{"$set": bson.M{"revoked": true}})
	return e
}
func (s *Service) Grants(ctx context.Context, p principal.Principal) ([]Grant, error) {
	cur, e := s.repo.db.Collection("oauth_grants").Find(ctx, bson.M{"user_id": p.UserID, "revoked": false, "expires_at": bson.M{"$gt": time.Now()}}, options.Find().SetLimit(200).SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if e != nil {
		return nil, e
	}
	defer cur.Close(ctx)
	out := []Grant{}
	e = cur.All(ctx, &out)
	return out, e
}
func (s *Service) RevokeGrant(ctx context.Context, p principal.Principal, id string) error {
	r, e := s.repo.db.Collection("oauth_grants").UpdateOne(ctx, bson.M{"_id": id, "user_id": p.UserID}, bson.M{"$set": bson.M{"revoked": true}})
	if e == nil && r.MatchedCount != 1 {
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
