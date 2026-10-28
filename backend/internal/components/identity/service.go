package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/mongodb"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/crypto/argon2"
	"net/mail"
	"strings"
	"time"
)

var ErrCredentials = errors.New("invalid email or password")

type User struct {
	ID        string    `json:"id" bson:"_id"`
	Email     string    `json:"email" bson:"email"`
	Name      string    `json:"name" bson:"name"`
	Admin     bool      `json:"is_admin" bson:"is_admin"`
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}
type credential struct {
	ID   string `bson:"_id"`
	Hash []byte `bson:"hash"`
	Salt []byte `bson:"salt"`
}
type Session struct {
	Hash      string    `json:"-" bson:"_id"`
	UserID    string    `json:"-" bson:"user_id"`
	CSRF      string    `json:"-" bson:"csrf"`
	ExpiresAt time.Time `json:"expires_at" bson:"expires_at"`
}
type Invite struct {
	Hash      string     `json:"-" bson:"_id"`
	Email     string     `json:"email" bson:"email"`
	CreatedBy string     `json:"created_by" bson:"created_by"`
	ExpiresAt time.Time  `json:"expires_at" bson:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty" bson:"used_at,omitempty"`
}
type MongoRepository struct {
	db     *mongo.Database
	client *mongo.Client
}
type Service struct{ repo *MongoRepository }

func NewMongoRepository(db *mongo.Database) *MongoRepository {
	return &MongoRepository{db, db.Client()}
}
func NewService(r *MongoRepository) *Service { return &Service{r} }
func Random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func passwordHash(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
}
func validateUser(email, name, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	a, e := mail.ParseAddress(email)
	if e != nil || a.Address != email || len(email) > 254 || strings.TrimSpace(name) == "" || len(name) > 100 || len(password) < 12 || len(password) > 256 {
		return "", fmt.Errorf("enter a valid email, name up to 100 characters and password of 12..256 characters")
	}
	return email, nil
}
func (s *Service) Init(ctx context.Context) error {
	for coll, models := range map[string][]mongo.IndexModel{
		"users":    {{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)}},
		"sessions": {{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)}},
		"invites":  {{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)}},
	} {
		if _, e := s.repo.db.Collection(coll).Indexes().CreateMany(ctx, models); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, e := s.repo.db.Collection("users").CountDocuments(ctx, bson.M{})
	return n == 0, e
}
func (s *Service) Setup(ctx context.Context, email, name, password string) (User, error) {
	email, e := validateUser(email, name, password)
	if e != nil {
		return User{}, e
	}
	u, cred, e := newUser(email, name, password, true)
	if e != nil {
		return User{}, e
	}
	e = mongodb.Workflow(ctx, func(tx context.Context) error {
		needed, e := s.NeedsSetup(tx)
		if e != nil {
			return e
		}
		if !needed {
			return principal.ErrForbidden
		}
		if _, e = s.repo.db.Collection("settings").InsertOne(tx, bson.M{"_id": "owner_initialized"}); mongo.IsDuplicateKeyError(e) {
			return principal.ErrForbidden
		} else if e != nil {
			return e
		}
		if _, e = s.repo.db.Collection("users").InsertOne(tx, u); e != nil {
			return e
		}
		_, e = s.repo.db.Collection("credentials").InsertOne(tx, cred)
		return e
	})
	return u, e
}
func newUser(email, name, password string, admin bool) (User, credential, error) {
	u := User{ID: ulid.Make().String(), Email: email, Name: strings.TrimSpace(name), Admin: admin, CreatedAt: time.Now().UTC()}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return User{}, credential{}, e
	}
	return u, credential{u.ID, passwordHash(password, salt), salt}, nil
}
func (s *Service) User(ctx context.Context, id string) (User, error) {
	var u User
	e := s.repo.db.Collection("users").FindOne(ctx, bson.M{"_id": id}).Decode(&u)
	if errors.Is(e, mongo.ErrNoDocuments) {
		e = principal.ErrNotFound
	}
	return u, e
}
func (s *Service) ByEmail(ctx context.Context, email string) (User, error) {
	var u User
	e := s.repo.db.Collection("users").FindOne(ctx, bson.M{"email": strings.ToLower(strings.TrimSpace(email))}).Decode(&u)
	if errors.Is(e, mongo.ErrNoDocuments) {
		e = principal.ErrNotFound
	}
	return u, e
}
func (s *Service) Login(ctx context.Context, email, password string) (User, error) {
	if len(password) > 256 {
		return User{}, ErrCredentials
	}
	u, e := s.ByEmail(ctx, email)
	var cred credential
	if e == nil {
		e = s.repo.db.Collection("credentials").FindOne(ctx, bson.M{"_id": u.ID}).Decode(&cred)
	}
	if e != nil {
		if !errors.Is(e, principal.ErrNotFound) && !errors.Is(e, mongo.ErrNoDocuments) {
			return User{}, e
		}
		_ = passwordHash(password, make([]byte, 16))
		return User{}, ErrCredentials
	}
	if subtle.ConstantTimeCompare(passwordHash(password, cred.Salt), cred.Hash) != 1 {
		return User{}, ErrCredentials
	}
	return u, nil
}
func (s *Service) StartSession(ctx context.Context, u User) (string, Session, error) {
	raw := Random()
	session := Session{Hash: Hash(raw), UserID: u.ID, CSRF: Random(), ExpiresAt: time.Now().Add(7 * 24 * time.Hour)}
	_, e := s.repo.db.Collection("sessions").InsertOne(ctx, session)
	return raw, session, e
}
func (s *Service) Session(ctx context.Context, raw string) (principal.Principal, Session, error) {
	var session Session
	e := s.repo.db.Collection("sessions").FindOne(ctx, bson.M{"_id": Hash(raw), "expires_at": bson.M{"$gt": time.Now()}}).Decode(&session)
	if e != nil {
		if errors.Is(e, mongo.ErrNoDocuments) {
			e = principal.ErrForbidden
		}
		return principal.Principal{}, session, e
	}
	u, e := s.User(ctx, session.UserID)
	return Principal(u), session, e
}
func Principal(u User) principal.Principal {
	return principal.Principal{UserID: u.ID, Scopes: append([]string{}, principal.Scopes...), Actor: principal.Actor{Kind: "human", ID: u.ID}, Admin: u.Admin}
}
func (s *Service) Logout(ctx context.Context, raw string) error {
	_, e := s.repo.db.Collection("sessions").DeleteOne(ctx, bson.M{"_id": Hash(raw)})
	return e
}
func (s *Service) Invite(ctx context.Context, p principal.Principal, email string) (string, error) {
	if !p.Admin {
		return "", principal.ErrForbidden
	}
	a, e := mail.ParseAddress(email)
	if e != nil || a.Address != email {
		return "", fmt.Errorf("valid email required")
	}
	email = strings.ToLower(email)
	if _, e = s.ByEmail(ctx, email); e == nil {
		return "", fmt.Errorf("user already exists")
	} else if !errors.Is(e, principal.ErrNotFound) {
		return "", e
	}
	raw := Random()
	_, e = s.repo.db.Collection("invites").InsertOne(ctx, Invite{Hash: Hash(raw), Email: email, CreatedBy: p.UserID, ExpiresAt: time.Now().Add(72 * time.Hour)})
	return raw, e
}
func (s *Service) Register(ctx context.Context, token, name, password string) (User, error) {
	var invite Invite
	e := s.repo.db.Collection("invites").FindOne(ctx, bson.M{"_id": Hash(token), "used_at": bson.M{"$exists": false}, "expires_at": bson.M{"$gt": time.Now()}}).Decode(&invite)
	if e != nil {
		return User{}, fmt.Errorf("invite is invalid or expired")
	}
	if _, e = validateUser(invite.Email, name, password); e != nil {
		return User{}, e
	}
	u, cred, e := newUser(invite.Email, name, password, false)
	if e != nil {
		return User{}, e
	}
	e = mongodb.Workflow(ctx, func(tx context.Context) error {
		r, e := s.repo.db.Collection("invites").UpdateOne(tx, bson.M{"_id": invite.Hash, "used_at": bson.M{"$exists": false}, "expires_at": bson.M{"$gt": time.Now()}}, bson.M{"$set": bson.M{"used_at": time.Now()}})
		if e != nil {
			return e
		}
		if r.ModifiedCount != 1 {
			return principal.ErrForbidden
		}
		if _, e = s.repo.db.Collection("users").InsertOne(tx, u); e != nil {
			return e
		}
		_, e = s.repo.db.Collection("credentials").InsertOne(tx, cred)
		return e
	})
	return u, e
}
func (s *Service) ChangePassword(ctx context.Context, p principal.Principal, current, next string) error {
	u, e := s.User(ctx, p.UserID)
	if e != nil {
		return e
	}
	if _, e = s.Login(ctx, u.Email, current); e != nil {
		return e
	}
	if len(next) < 12 || len(next) > 256 {
		return fmt.Errorf("password must be 12..256 characters")
	}
	_, cred, e := newUser(u.Email, u.Name, next, false)
	if e != nil {
		return e
	}
	cred.ID = u.ID
	return mongodb.Workflow(ctx, func(tx context.Context) error {
		if _, e := s.repo.db.Collection("credentials").ReplaceOne(tx, bson.M{"_id": u.ID}, cred); e != nil {
			return e
		}
		_, e := s.repo.db.Collection("sessions").DeleteMany(tx, bson.M{"user_id": u.ID})
		return e
	})
}

// EachUser is for operator maintenance; it is never exposed by an HTTP or MCP handler.
func (s *Service) EachUser(ctx context.Context, fn func(principal.Principal) error) error {
	cur, e := s.repo.db.Collection("users").Find(ctx, bson.M{}, options.Find().SetProjection(bson.M{"_id": 1, "admin": 1}).SetBatchSize(100))
	if e != nil {
		return e
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var u User
		if e = cur.Decode(&u); e != nil {
			return e
		}
		p := Principal(u)
		p.Actor = principal.Actor{Kind: "system", ID: "maintenance"}
		if e = fn(p); e != nil {
			return e
		}
	}
	return cur.Err()
}
