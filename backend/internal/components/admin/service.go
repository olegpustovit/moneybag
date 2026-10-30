// Package admin lets agents with admin:users run people and connections, and keeps the owner's say over escalations and the emergency stop.
package admin

import (
	"context"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"net/url"
	"strings"
	"time"
)

const confirmationTTL = time.Hour

// Confirmation binds one owner decision to one exact action; the agent holds only the link.
type Confirmation struct {
	Hash        string          `json:"-" bson:"_id"`
	UserID      string          `json:"-" bson:"user_id"`
	Action      string          `json:"action" bson:"action"`
	Target      string          `json:"-" bson:"target"`
	TargetLabel string          `json:"target_label" bson:"target_label"`
	Reason      string          `json:"reason" bson:"reason"`
	RequestedBy principal.Actor `json:"requested_by" bson:"requested_by"`
	ExpiresAt   time.Time       `json:"expires_at" bson:"expires_at"`
	UsedAt      *time.Time      `json:"used_at,omitempty" bson:"used_at,omitempty"`
}

// Sentence is what the confirmation page asks the owner to approve.
func (c Confirmation) Sentence() string {
	if c.Action == "grant_admin_users" {
		return "Give " + c.TargetLabel + " permission to list, invite and disable people, and to disconnect other agents."
	}
	return "Disable " + c.TargetLabel + ", an admin of this Moneybag. They are signed out at once and cannot sign in until an admin re-enables them."
}

// Pending is what a tool returns when the owner must approve the action first.
type Pending struct {
	Status     string `json:"status"`
	ConfirmURL string `json:"confirm_url,omitempty"`
	Message    string `json:"message"`
}
type InviteInput struct {
	Email  string `json:"email"`
	Reason string `json:"reason" jsonschema:"why this person is being invited; recorded in the audit trail"`
}
type InviteResult struct {
	Email         string `json:"email"`
	InvitationURL string `json:"invitation_url" jsonschema:"send this to the person; they set their own password from it"`
}
type DisableInput struct {
	UserID string `json:"user_id"`
	Reason string `json:"reason" jsonschema:"recorded in the audit trail"`
}
type RevokeInput struct {
	ID     string `json:"id" jsonschema:"connection id from agent_list"`
	Reason string `json:"reason" jsonschema:"recorded in the audit trail"`
}
type RequestInput struct {
	Reason string `json:"reason" jsonschema:"shown to the owner on the confirmation page"`
}
type MongoRepository struct{ db *mongo.Database }
type Service struct {
	repo      *MongoRepository
	identity  *identity.Service
	oauth     *oauth.Service
	audit     *audit.Service
	publicURL string
}

func NewMongoRepository(db *mongo.Database) *MongoRepository { return &MongoRepository{db} }
func NewService(r *MongoRepository, i *identity.Service, o *oauth.Service, a *audit.Service, publicURL string) *Service {
	return &Service{r, i, o, a, strings.TrimRight(publicURL, "/")}
}
func (s *Service) Init(ctx context.Context) error {
	_, e := s.repo.db.Collection("admin_confirmations").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)})
	return e
}

func needReason(reason string) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 1000 {
		return fmt.Errorf("a reason of 1..1000 characters is required; it goes in the audit trail")
	}
	return nil
}

func (s *Service) Users(ctx context.Context, p principal.Principal) ([]identity.User, error) {
	if e := p.Require(principal.ScopeAdminUsers); e != nil {
		return nil, e
	}
	return s.identity.Users(ctx)
}

func (s *Service) Invite(ctx context.Context, p principal.Principal, in InviteInput) (InviteResult, error) {
	if e := p.Require(principal.ScopeAdminUsers); e != nil {
		return InviteResult{}, e
	}
	if e := needReason(in.Reason); e != nil {
		return InviteResult{}, e
	}
	var raw string
	after := map[string]any{"email": in.Email, "scopes": p.Scopes}
	_, e := s.audit.Write(audit.WithSummary(ctx, "Invited "+in.Email), p, "user_invite", audit.Target{Collection: "invites", ID: in.Email}, in.Reason, nil, after, func(tx context.Context) error {
		var e error
		raw, e = s.identity.Invite(tx, p, in.Email)
		return e
	})
	if e != nil {
		return InviteResult{}, e
	}
	return InviteResult{in.Email, s.publicURL + "/register?token=" + url.QueryEscape(raw)}, nil
}

// DisableUser acts at once on ordinary users; an admin, which covers the owner and the last admin, needs the owner's confirmation page.
func (s *Service) DisableUser(ctx context.Context, p principal.Principal, in DisableInput) (Pending, error) {
	if e := p.Require(principal.ScopeAdminUsers); e != nil {
		return Pending{}, e
	}
	if e := needReason(in.Reason); e != nil {
		return Pending{}, e
	}
	target, e := s.identity.User(ctx, in.UserID)
	if e != nil {
		return Pending{}, e
	}
	if target.Disabled {
		return Pending{Status: "disabled", Message: target.Name + " is already disabled."}, nil
	}
	if target.Admin {
		link, e := s.request(ctx, p, "disable_user", target.ID, target.Name, in.Reason)
		if e != nil {
			return Pending{}, e
		}
		return Pending{Status: "needs_confirmation", ConfirmURL: link, Message: "Disabling an admin needs the owner. Ask them to open this link; it covers only this action and expires in an hour."}, nil
	}
	if e = s.disable(ctx, p, target, in.Reason); e != nil {
		return Pending{}, e
	}
	return Pending{Status: "disabled", Message: target.Name + " is disabled and signed out."}, nil
}

func (s *Service) disable(ctx context.Context, p principal.Principal, target identity.User, reason string) error {
	after := map[string]any{"user": target.ID, "name": target.Name, "disabled": true, "scopes": p.Scopes}
	_, e := s.audit.Write(audit.WithSummary(ctx, "Disabled "+target.Name), p, "user_disable", audit.Target{Collection: "users", ID: target.ID}, reason, map[string]any{"disabled": false}, after, func(tx context.Context) error {
		return s.identity.SetDisabled(tx, target.ID, true)
	})
	return e
}

// EnableUser is the inverse of disabling, and only ever restores access.
func (s *Service) EnableUser(ctx context.Context, p principal.Principal, id, reason string) error {
	if e := p.Require(principal.ScopeAdminUsers); e != nil {
		return e
	}
	target, e := s.identity.User(ctx, id)
	if e != nil {
		return e
	}
	_, e = s.audit.Write(audit.WithSummary(ctx, "Re-enabled "+target.Name), p, "user_enable", audit.Target{Collection: "users", ID: id}, reason, map[string]any{"disabled": true}, map[string]any{"disabled": false, "scopes": p.Scopes}, func(tx context.Context) error {
		return s.identity.SetDisabled(tx, id, false)
	})
	return e
}

func (s *Service) Agents(ctx context.Context, p principal.Principal) ([]oauth.Grant, error) {
	if e := p.Require(principal.ScopeAdminUsers); e != nil {
		return nil, e
	}
	return s.oauth.Grants(ctx, p)
}

func (s *Service) RevokeAgent(ctx context.Context, p principal.Principal, in RevokeInput) error {
	if e := p.Require(principal.ScopeAdminUsers); e != nil {
		return e
	}
	if e := needReason(in.Reason); e != nil {
		return e
	}
	grant, e := s.oauth.OwnedGrant(ctx, p, in.ID)
	if e != nil {
		return e
	}
	after := map[string]any{"client": grant.ClientName, "scopes": grant.Scopes, "revoked_by_scopes": p.Scopes}
	_, e = s.audit.Write(audit.WithSummary(ctx, "Disconnected "+grant.ClientName), p, "agent_revoke", audit.Target{Collection: "oauth_grants", ID: grant.ClientID}, in.Reason, nil, after, func(tx context.Context) error {
		return s.oauth.RevokeGrant(tx, p, in.ID)
	})
	return e
}

// RequestAdmin returns the owner's page for adding admin:users to the calling connection.
func (s *Service) RequestAdmin(ctx context.Context, p principal.Principal, in RequestInput) (Pending, error) {
	if e := p.Require("ledger:read"); e != nil {
		return Pending{}, e
	}
	if e := needReason(in.Reason); e != nil {
		return Pending{}, e
	}
	if p.GrantID == "" {
		return Pending{}, fmt.Errorf("only an agent connection can ask for admin:users")
	}
	if p.Require(principal.ScopeAdminUsers) == nil {
		return Pending{Status: "granted", Message: "This connection already has admin:users."}, nil
	}
	grant, e := s.oauth.OwnedGrant(ctx, p, p.GrantID)
	if e != nil {
		return Pending{}, e
	}
	link, e := s.request(ctx, p, "grant_admin_users", grant.ID, grant.ClientName, in.Reason)
	if e != nil {
		return Pending{}, e
	}
	return Pending{Status: "needs_confirmation", ConfirmURL: link, Message: "Ask the owner to open this link; it covers only this connection and expires in an hour. Your tools update as soon as they approve."}, nil
}

func (s *Service) request(ctx context.Context, p principal.Principal, action, target, label, reason string) (string, error) {
	raw := identity.Random()
	c := Confirmation{Hash: identity.Hash(raw), UserID: p.UserID, Action: action, Target: target, TargetLabel: label, Reason: reason, RequestedBy: p.Actor, ExpiresAt: time.Now().UTC().Add(confirmationTTL)}
	if _, e := s.repo.db.Collection("admin_confirmations").InsertOne(ctx, c); e != nil {
		return "", e
	}
	return s.publicURL + "/confirm/" + url.PathEscape(raw), nil
}

// Confirmation reads a pending request for the page. Only the owner it belongs to may see it.
func (s *Service) Confirmation(ctx context.Context, p principal.Principal, raw string) (Confirmation, error) {
	if !p.Admin || p.Actor.Kind != "human" {
		return Confirmation{}, principal.ErrForbidden
	}
	var c Confirmation
	e := s.repo.db.Collection("admin_confirmations").FindOne(ctx, bson.M{"_id": identity.Hash(raw), "user_id": p.UserID, "used_at": bson.M{"$exists": false}, "expires_at": bson.M{"$gt": time.Now()}}).Decode(&c)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return Confirmation{}, principal.ErrNotFound
	}
	return c, e
}

// Decide consumes the link exactly once, so a decision cannot be replayed, and only a signed-in owner reaches it.
func (s *Service) Decide(ctx context.Context, p principal.Principal, raw string, allow bool) (Confirmation, error) {
	if !p.Admin || p.Actor.Kind != "human" {
		return Confirmation{}, principal.ErrForbidden
	}
	var c Confirmation
	e := s.audit.Workflow(ctx, func(tx context.Context) error {
		e := s.repo.db.Collection("admin_confirmations").FindOneAndUpdate(tx, bson.M{"_id": identity.Hash(raw), "user_id": p.UserID, "used_at": bson.M{"$exists": false}, "expires_at": bson.M{"$gt": time.Now()}}, bson.M{"$set": bson.M{"used_at": time.Now().UTC()}}).Decode(&c)
		if errors.Is(e, mongo.ErrNoDocuments) {
			return principal.ErrNotFound
		}
		if e != nil || !allow {
			return e
		}
		switch c.Action {
		case "grant_admin_users":
			_, e = s.audit.Write(audit.WithSummary(tx, "Gave "+c.TargetLabel+" permission to manage people"), p, "agent_grant_admin", audit.Target{Collection: "oauth_grants", ID: c.RequestedBy.ID}, c.Reason, nil, map[string]any{"client": c.TargetLabel, "scope": principal.ScopeAdminUsers, "requested_by": c.RequestedBy}, func(tx context.Context) error {
				return s.oauth.AddScope(tx, p, c.Target, principal.ScopeAdminUsers)
			})
			return e
		case "disable_user":
			target, e := s.identity.User(tx, c.Target)
			if e != nil {
				return e
			}
			return s.disable(tx, p, target, c.Reason)
		}
		return fmt.Errorf("unknown confirmation action")
	})
	return c, e
}

// SetAgentsSuspended is the owner's emergency stop: every agent grant on this server stops at once, and resuming restores them unchanged.
func (s *Service) SetAgentsSuspended(ctx context.Context, p principal.Principal, suspended bool) error {
	if !p.Admin || p.Actor.Kind != "human" {
		return principal.ErrForbidden
	}
	summary := "Re-enabled all agents"
	if suspended {
		summary = "Suspended all agents"
	}
	_, e := s.audit.Write(audit.WithSummary(ctx, summary), p, "agents_suspend", audit.Target{Collection: "settings", ID: "agents"}, "", map[string]bool{"suspended": !suspended}, map[string]bool{"suspended": suspended}, func(tx context.Context) error {
		return s.oauth.SetSuspended(tx, p.UserID, suspended)
	})
	return e
}
