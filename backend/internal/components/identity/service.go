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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"golang.org/x/crypto/argon2"
	"net/mail"
	"strings"
	"time"
)

var ErrCredentials = errors.New("invalid email or password")

type User struct {
	ID        string    `json:"id" db:"id"`
	Email     string    `json:"email" db:"email"`
	Name      string    `json:"name" db:"name"`
	Admin     bool      `json:"is_admin" db:"is_admin"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	Disabled  bool      `json:"disabled,omitempty" db:"disabled"`
}
type credential struct {
	ID   string `db:"id"`
	Hash []byte `db:"hash"`
	Salt []byte `db:"salt"`
}
type Session struct {
	Hash      string    `json:"-" db:"id"`
	UserID    string    `json:"-" db:"user_id"`
	CSRF      string    `json:"-" db:"csrf"`
	ExpiresAt time.Time `json:"expires_at" db:"expires_at"`
}
type Invite struct {
	Hash      string     `json:"-" db:"id"`
	Email     string     `json:"email" db:"email"`
	CreatedBy string     `json:"created_by" db:"created_by"`
	ExpiresAt time.Time  `json:"expires_at" db:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty" db:"used_at"`
}
type PostgresRepository struct {
	db *pgxpool.Pool
}
type Service struct{ repo *PostgresRepository }

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db}
}
func NewService(r *PostgresRepository) *Service { return &Service{r} }
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
func (s *Service) Init(context.Context) error { return nil }
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	const setupSQL = `SELECT NOT EXISTS (SELECT 1 FROM users)`
	var needed bool
	e := s.repo.db.QueryRow(ctx, setupSQL).Scan(&needed)
	return needed, e
}

const insertUserSQL = `INSERT INTO users (id,email,name,is_admin,created_at,disabled) VALUES (@id,@email,@name,@admin,@at,@disabled)`
const insertCredentialSQL = `INSERT INTO credentials (id,hash,salt) VALUES (@id,@hash,@salt)`

func (s *Service) Setup(ctx context.Context, email, name, password string) (User, error) {
	email, e := validateUser(email, name, password)
	if e != nil {
		return User{}, e
	}
	u, cred, e := newUser(email, name, password, true)
	if e != nil {
		return User{}, e
	}
	e = postgres.Workflow(ctx, func(tx context.Context) error {
		needed, e := s.NeedsSetup(tx)
		if e != nil {
			return e
		}
		if !needed {
			return principal.ErrForbidden
		}
		if _, e = s.repo.db.Exec(tx, `INSERT INTO settings (id) VALUES ('owner_initialized')`); postgres.IsUniqueViolation(e) {
			return principal.ErrForbidden
		} else if e != nil {
			return e
		}
		if _, e = s.repo.db.Exec(tx, insertUserSQL, pgx.NamedArgs{"id": u.ID, "email": u.Email, "name": u.Name, "admin": u.Admin, "at": u.CreatedAt, "disabled": u.Disabled}); e != nil {
			return e
		}
		_, e = s.repo.db.Exec(tx, insertCredentialSQL, pgx.NamedArgs{"id": cred.ID, "hash": cred.Hash, "salt": cred.Salt})
		return e
	})
	return u, e
}
func newUser(email, name, password string, admin bool) (User, credential, error) {
	u := User{ID: ulid.Make().String(), Email: email, Name: strings.TrimSpace(name), Admin: admin, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return User{}, credential{}, e
	}
	return u, credential{u.ID, passwordHash(password, salt), salt}, nil
}
func (s *Service) User(ctx context.Context, id string) (User, error) {
	var u User
	const userSQL = `SELECT * FROM users WHERE id=@id`
	rows, e := s.repo.db.Query(ctx, userSQL, pgx.NamedArgs{"id": id})
	if e != nil {
		return u, e
	}
	u, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(e, pgx.ErrNoRows) {
		e = principal.ErrNotFound
	}
	return u, e
}
func (s *Service) ByEmail(ctx context.Context, email string) (User, error) {
	var u User
	const emailSQL = `SELECT * FROM users WHERE email=@email`
	rows, e := s.repo.db.Query(ctx, emailSQL, pgx.NamedArgs{"email": strings.ToLower(strings.TrimSpace(email))})
	if e != nil {
		return u, e
	}
	u, e = pgx.CollectOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(e, pgx.ErrNoRows) {
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
		const credentialSQL = `SELECT id,hash,salt FROM credentials WHERE id=@id`
		e = s.repo.db.QueryRow(ctx, credentialSQL, pgx.NamedArgs{"id": u.ID}).Scan(&cred.ID, &cred.Hash, &cred.Salt)
	}
	if e != nil {
		if !errors.Is(e, principal.ErrNotFound) && !errors.Is(e, pgx.ErrNoRows) {
			return User{}, e
		}
		_ = passwordHash(password, make([]byte, 16))
		return User{}, ErrCredentials
	}
	if subtle.ConstantTimeCompare(passwordHash(password, cred.Salt), cred.Hash) != 1 || u.Disabled {
		return User{}, ErrCredentials
	}
	return u, nil
}
func (s *Service) StartSession(ctx context.Context, u User) (string, Session, error) {
	return s.startSession(ctx, u, false)
}
func (s *Service) startSession(ctx context.Context, u User, identityMode bool) (string, Session, error) {
	raw := Random()
	hash := Hash(raw)
	if identityMode {
		hash = identitySessionHash(raw)
	}
	session := Session{Hash: hash, UserID: u.ID, CSRF: Random(), ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Millisecond)}
	const sessionSQL = `INSERT INTO sessions (id,user_id,csrf,expires_at) VALUES (@id,@user,@csrf,@expires)`
	_, e := s.repo.db.Exec(ctx, sessionSQL, pgx.NamedArgs{"id": session.Hash, "user": session.UserID, "csrf": session.CSRF, "expires": session.ExpiresAt})
	return raw, session, e
}
func (s *Service) Session(ctx context.Context, raw string) (principal.Principal, Session, error) {
	return s.session(ctx, Hash(raw))
}
func (s *Service) session(ctx context.Context, hash string) (principal.Principal, Session, error) {
	var session Session
	const sessionSQL = `SELECT id,user_id,csrf,expires_at FROM sessions WHERE id=@id AND expires_at>@now`
	e := s.repo.db.QueryRow(ctx, sessionSQL, pgx.NamedArgs{"id": hash, "now": time.Now().UTC().Truncate(time.Millisecond)}).Scan(&session.Hash, &session.UserID, &session.CSRF, &session.ExpiresAt)
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			e = principal.ErrForbidden
		}
		return principal.Principal{}, session, e
	}
	u, e := s.User(ctx, session.UserID)
	if e == nil && u.Disabled {
		e = principal.ErrForbidden
	}
	return Principal(u), session, e
}
func Principal(u User) principal.Principal {
	scopes := append([]string{}, principal.Scopes...)
	if u.Admin {
		scopes = append(scopes, principal.ScopeAdminUsers)
	}
	return principal.Principal{UserID: u.ID, Scopes: scopes, Actor: principal.Actor{Kind: "human", ID: u.ID}, Admin: u.Admin}
}
func (s *Service) Logout(ctx context.Context, raw string) error {
	return s.logout(ctx, Hash(raw))
}
func (s *Service) logout(ctx context.Context, hash string) error {
	_, e := s.repo.db.Exec(ctx, `DELETE FROM sessions WHERE id=@id`, pgx.NamedArgs{"id": hash})
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
	const inviteSQL = `INSERT INTO invites (id,email,created_by,expires_at) VALUES (@id,@email,@by,@expires)`
	_, e = s.repo.db.Exec(ctx, inviteSQL, pgx.NamedArgs{"id": Hash(raw), "email": email, "by": p.UserID, "expires": time.Now().UTC().Add(72 * time.Hour).Truncate(time.Millisecond)})
	return raw, e
}
func (s *Service) Register(ctx context.Context, token, name, password string) (User, error) {
	var invite Invite
	const inviteSQL = `SELECT id,email,created_by,expires_at,used_at FROM invites WHERE id=@id AND used_at IS NULL AND expires_at>@now`
	e := s.repo.db.QueryRow(ctx, inviteSQL, pgx.NamedArgs{"id": Hash(token), "now": time.Now().UTC().Truncate(time.Millisecond)}).Scan(&invite.Hash, &invite.Email, &invite.CreatedBy, &invite.ExpiresAt, &invite.UsedAt)
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
	e = postgres.Workflow(ctx, func(tx context.Context) error {
		const useSQL = `UPDATE invites SET used_at=@now WHERE id=@id AND used_at IS NULL AND expires_at>@now`
		r, e := s.repo.db.Exec(tx, useSQL, pgx.NamedArgs{"id": invite.Hash, "now": time.Now().UTC().Truncate(time.Millisecond)})
		if e != nil {
			return e
		}
		if r.RowsAffected() != 1 {
			return principal.ErrForbidden
		}
		if _, e = s.repo.db.Exec(tx, insertUserSQL, pgx.NamedArgs{"id": u.ID, "email": u.Email, "name": u.Name, "admin": u.Admin, "at": u.CreatedAt, "disabled": u.Disabled}); e != nil {
			return e
		}
		_, e = s.repo.db.Exec(tx, insertCredentialSQL, pgx.NamedArgs{"id": cred.ID, "hash": cred.Hash, "salt": cred.Salt})
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
	return postgres.Workflow(ctx, func(tx context.Context) error {
		if _, e := s.repo.db.Exec(tx, `UPDATE credentials SET hash=@hash,salt=@salt WHERE id=@id`, pgx.NamedArgs{"id": u.ID, "hash": cred.Hash, "salt": cred.Salt}); e != nil {
			return e
		}
		_, e := s.repo.db.Exec(tx, `DELETE FROM sessions WHERE user_id=@user`, pgx.NamedArgs{"user": u.ID})
		return e
	})
}

// Users lists everyone on this server; callers enforce admin:users. Password material lives in another collection and never reaches it.
func (s *Service) Users(ctx context.Context) ([]User, error) {
	const usersSQL = `SELECT * FROM users ORDER BY created_at LIMIT 500`
	rows, e := s.repo.db.Query(ctx, usersSQL)
	if e != nil {
		return nil, e
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[User])
}

// SetDisabled blocks or restores a user; disabling also ends their sessions, and their agents stop at the next token check.
func (s *Service) SetDisabled(ctx context.Context, id string, disabled bool) error {
	const disableSQL = `UPDATE users SET disabled=@disabled WHERE id=@id`
	r, e := s.repo.db.Exec(ctx, disableSQL, pgx.NamedArgs{"id": id, "disabled": disabled})
	if e == nil && r.RowsAffected() != 1 {
		return principal.ErrNotFound
	}
	if e != nil || !disabled {
		return e
	}
	_, e = s.repo.db.Exec(ctx, `DELETE FROM sessions WHERE user_id=@user`, pgx.NamedArgs{"user": id})
	return e
}

// EachUser is for operator maintenance; it is never exposed by an HTTP or MCP handler.
func (s *Service) EachUser(ctx context.Context, fn func(principal.Principal) error) error {
	// The original projection omitted is_admin, so maintenance principals stay non-admin.
	const eachSQL = `SELECT id FROM users`
	rows, e := s.repo.db.Query(ctx, eachSQL)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var u User
		if e = rows.Scan(&u.ID); e != nil {
			return e
		}
		p := Principal(u)
		p.Actor = principal.Actor{Kind: "system", ID: "maintenance"}
		if e = fn(p); e != nil {
			return e
		}
	}
	return rows.Err()
}
