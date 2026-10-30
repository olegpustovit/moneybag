package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

// Exchange and Refresh treat an omitted resource (optional in RFC 8707) as the one the grant was issued for.
func (s *Service) Exchange(ctx context.Context, c Client, raw, redirect, verifier, resource string) (TokenResponse, error) {
	if resource != "" && !s.resource(resource) {
		return TokenResponse{}, ErrInvalidTarget
	}
	if !verifierPattern.MatchString(verifier) {
		return TokenResponse{}, ErrInvalidGrant
	}
	var result TokenResponse
	e := mongodb.Workflow(ctx, func(tx context.Context) error {
		var auth code
		f := bson.M{"_id": identity.Hash(raw), "client_id": c.ID, "redirect_uri": redirect, "challenge": Challenge(verifier), "expires_at": bson.M{"$gt": time.Now()}}
		if resource != "" {
			f["resource"] = resource
		}
		e := s.repo.db.Collection("oauth_codes").FindOneAndDelete(tx, f).Decode(&auth)
		if errors.Is(e, mongo.ErrNoDocuments) {
			return ErrInvalidGrant
		}
		if e != nil {
			return e
		}
		g := Grant{ID: identity.Random(), UserID: auth.UserID, ClientID: c.ID, ClientName: c.Name, Resource: auth.Resource, Scopes: auth.Scopes, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(RefreshTTL)}
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
	if resource != "" && !s.resource(resource) {
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
		if e != nil || g.ClientID != c.ID || resource != "" && g.Resource != resource {
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
	now := time.Now()
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"_id": identity.Hash(raw), "access_expires_at": bson.M{"$gt": now}, "$expr": bson.M{"$and": bson.A{bson.M{"$eq": bson.A{bson.M{"$type": "$grant_id"}, "string"}}, bson.M{"$eq": bson.A{bson.M{"$type": "$access_expires_at"}, "date"}}}}}}},
		{{Key: "$set", Value: bson.M{"token": "$$ROOT"}}},
		{{Key: "$lookup", Value: bson.M{"from": "oauth_grants", "localField": "grant_id", "foreignField": "_id", "as": "grant"}}},
		{{Key: "$unwind", Value: "$grant"}},
		{{Key: "$match", Value: bson.M{"grant.revoked": false, "grant.expires_at": bson.M{"$gt": now}}}},
		// Authentication needs the current user existence check as well as the current grant.
		{{Key: "$lookup", Value: bson.M{"from": "users", "localField": "grant.user_id", "foreignField": "_id", "pipeline": mongo.Pipeline{{{Key: "$project", Value: bson.M{"_id": 1}}}}, "as": "users"}}},
		{{Key: "$project", Value: bson.M{"token": 1, "grant": 1, "users": 1}}},
	}
	// The unique token lookup returns at most one row; a two-row batch exhausts the cursor.
	cursor, err := s.repo.db.Collection("oauth_tokens").Aggregate(ctx, pipeline, options.Aggregate().SetBatchSize(2))
	if err != nil {
		return principal.Principal{}, principal.ErrForbidden
	}
	defer cursor.Close(ctx)
	var result struct {
		Token token `bson:"token"`
		Grant Grant `bson:"grant"`
		Users []struct {
			ID string `bson:"_id"`
		} `bson:"users"`
	}
	if !cursor.Next(ctx) || cursor.Decode(&result) != nil || result.Grant.Resource != resource {
		return principal.Principal{}, principal.ErrForbidden
	}
	if len(result.Users) != 1 || result.Users[0].ID != result.Grant.UserID {
		return principal.Principal{}, principal.ErrNotFound
	}
	return principal.Principal{UserID: result.Grant.UserID, Scopes: result.Grant.Scopes, Actor: principal.Actor{Kind: "agent", ID: result.Grant.ClientID}}, nil
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

type AgentName struct {
	ClientID   string `json:"client_id"`
	ClientName string `json:"client_name"`
}

// AgentNames includes expired and revoked grants so past audit events remain identifiable.
func (s *Service) AgentNames(ctx context.Context, p principal.Principal) ([]AgentName, error) {
	if err := p.Require("ledger:read"); err != nil {
		return nil, err
	}
	cur, err := s.repo.db.Collection("oauth_grants").Find(ctx, bson.M{"user_id": p.UserID}, options.Find().SetProjection(bson.M{"client_id": 1, "client_name": 1, "created_at": 1}))
	if err != nil {
		return nil, fmt.Errorf("find activity agents: %w", err)
	}
	defer cur.Close(ctx)
	latest := map[string]Grant{}
	for cur.Next(ctx) {
		var grant Grant
		if err := cur.Decode(&grant); err != nil {
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
	if err := cur.Err(); err != nil {
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
