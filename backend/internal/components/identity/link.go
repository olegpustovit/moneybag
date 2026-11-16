package identity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/postgres"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

var ErrIdentityInvalid = errors.New("invalid verified identity")
var ErrInvitationRequired = errors.New("Moneybag invitation required")

func verifiedIdentity(subject, email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	if subject == "" || len(subject) > 255 || strings.ContainsRune(subject, 0) || err != nil || address.Address != email || len(email) > 254 {
		return "", ErrIdentityInvalid
	}
	return email, nil
}

// LinkIdentity accepts only SDK-verified subject/email claims. It preserves the
// local profile, credentials, grants and financial ownership, and creates no user.
func (s *Service) LinkIdentity(ctx context.Context, subject, email string) (User, error) {
	email, err := verifiedIdentity(subject, email)
	if err != nil {
		return User{}, err
	}
	var user User
	err = pgx.BeginFunc(ctx, s.repo.db, func(tx pgx.Tx) error {
		var err error
		user, err = linkIdentityTx(ctx, tx, subject, email)
		return err
	})
	if err != nil {
		return User{}, fmt.Errorf("link Identity user: %w", identityError(err))
	}
	return user, nil
}

const identitySubjectSQL = `SELECT u.id,u.email,u.name,u.is_admin,u.created_at,u.disabled
 FROM user_identities i JOIN users u ON u.id=i.user_id WHERE i.subject=@subject FOR UPDATE OF u`
const identityEmailSQL = `SELECT id,email,name,is_admin,created_at,disabled FROM users WHERE email=@email FOR UPDATE`
const insertIdentitySQL = `INSERT INTO user_identities(subject,user_id) VALUES(@subject,@user)`

func identityUser(row pgx.Row) (User, error) {
	var user User
	err := row.Scan(&user.ID, &user.Email, &user.Name, &user.Admin, &user.CreatedAt, &user.Disabled)
	return user, err
}

func linkIdentityTx(ctx context.Context, tx pgx.Tx, subject, email string) (User, error) {
	args := pgx.NamedArgs{"subject": subject, "email": email}
	user, err := identityUser(tx.QueryRow(ctx, identitySubjectSQL, args))
	if err == nil {
		if user.Disabled {
			return User{}, principal.ErrForbidden
		}
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return User{}, err
	}
	user, err = identityUser(tx.QueryRow(ctx, identityEmailSQL, args))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvitationRequired
	}
	if err != nil {
		return User{}, err
	}
	if user.Disabled {
		return User{}, principal.ErrForbidden
	}
	// Unique subject and user keys serialize competing links without replacing either owner.
	const attachSQL = `INSERT INTO user_identities(subject,user_id) VALUES(@subject,@user) ON CONFLICT DO NOTHING`
	if _, err = tx.Exec(ctx, attachSQL, pgx.NamedArgs{"subject": subject, "user": user.ID}); err != nil {
		return User{}, err
	}
	linked, err := identityUser(tx.QueryRow(ctx, identitySubjectSQL, args))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, principal.ErrConflict
	}
	if err != nil {
		return User{}, err
	}
	if linked.Disabled {
		return User{}, principal.ErrForbidden
	}
	return linked, nil
}

// RegisterIdentity requires a live, matching local invite, even on an empty
// server. Created reports account creation; pending initialization is persisted for retry.
// Invite consumption, account creation and subject binding commit together.
func (s *Service) RegisterIdentity(ctx context.Context, subject, email, name, token string) (User, bool, error) {
	email, err := verifiedIdentity(subject, email)
	name = strings.TrimSpace(name)
	if err != nil || name == "" || len(name) > 100 || strings.ContainsRune(name, 0) || len(token) > 512 {
		return User{}, false, ErrIdentityInvalid
	}
	if token == "" {
		return User{}, false, ErrInvitationRequired
	}
	var user User
	created := false
	err = pgx.BeginFunc(ctx, s.repo.db, func(tx pgx.Tx) error {
		const inviteSQL = `SELECT email,expires_at,used_at FROM invites WHERE id=@id FOR UPDATE`
		var invitedEmail string
		var expires time.Time
		var used *time.Time
		err := tx.QueryRow(ctx, inviteSQL, pgx.NamedArgs{"id": Hash(token)}).Scan(&invitedEmail, &expires, &used)
		if errors.Is(err, pgx.ErrNoRows) {
			return principal.ErrForbidden
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if used != nil || !expires.After(now) || invitedEmail != email {
			return principal.ErrForbidden
		}
		user, err = linkIdentityTx(ctx, tx, subject, email)
		if errors.Is(err, ErrInvitationRequired) {
			user = User{ID: ulid.Make().String(), Email: email, Name: name, CreatedAt: now}
			if _, err = tx.Exec(ctx, insertUserSQL, pgx.NamedArgs{"id": user.ID, "email": user.Email, "name": user.Name, "admin": false, "at": now, "disabled": false}); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO user_identities(subject,user_id,needs_initialization) VALUES(@subject,@user,true)`, pgx.NamedArgs{"subject": subject, "user": user.ID}); err != nil {
				return err
			}
			created = true
		} else if err != nil {
			return err
		}
		if user.Email != email {
			return principal.ErrConflict
		}
		const useSQL = `UPDATE invites SET used_at=@now WHERE id=@id`
		_, err = tx.Exec(ctx, useSQL, pgx.NamedArgs{"now": now, "id": Hash(token)})
		return err
	})
	if err != nil {
		return User{}, false, fmt.Errorf("register Identity user: %w", identityError(err))
	}
	return user, created, nil
}

func identityError(err error) error {
	if postgres.IsUniqueViolation(err) {
		return principal.ErrConflict
	}
	return err
}
