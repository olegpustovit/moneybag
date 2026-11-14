// Legacy storage adapters are isolated here for the 30-day rollback window.
package main

import (
	"github.com/olegpustovit/moneybag/backend/internal/components/admin"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/classify"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/feedback"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/semantic"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	"time"
)

type legacyLedgerAccount struct {
	ledger.Account `bson:",inline"`
	UserID         string     `bson:"user_id"`
	Key            string     `bson:"_id"`
	ClosedAt       *time.Time `bson:"closed_at,omitempty"`
	DisplayName    string     `bson:"display_name,omitempty"`
}

func (v legacyLedgerAccount) value() ledger.Account {
	out := v.Account
	out.UserID = v.UserID
	out.Key = v.Key
	out.ClosedAt = v.ClosedAt
	out.DisplayName = v.DisplayName
	return out
}

type legacyLedgerEntry struct {
	ledger.Entry `bson:",inline"`
	UserID       string                `bson:"user_id"`
	ID           string                `bson:"_id"`
	Postings     []legacyLedgerPosting `bson:"postings"`
	CreatedAt    time.Time             `bson:"created_at"`
}

func (v legacyLedgerEntry) value() ledger.Entry {
	out := v.Entry
	out.UserID = v.UserID
	out.ID = v.ID
	if v.Postings != nil {
		out.Postings = make([]ledger.Posting, len(v.Postings))
		for i, x := range v.Postings {
			out.Postings[i] = x.value()
		}
	}
	out.CreatedAt = v.CreatedAt
	return out
}

type legacyLedgerPosting struct {
	ledger.Posting `bson:",inline"`
	Amount         legacyAmount  `bson:"amount"`
	Cost           *legacyAmount `bson:"cost,omitempty"`
	Price          *legacyAmount `bson:"price,omitempty"`
}

func (v legacyLedgerPosting) value() ledger.Posting {
	out := v.Posting
	out.Amount = v.Amount.value()
	if v.Cost != nil {
		x := v.Cost.value()
		out.Cost = &x
	}
	if v.Price != nil {
		x := v.Price.value()
		out.Price = &x
	}
	return out
}

type legacyLedgerEntryInput struct {
	ledger.EntryInput `bson:",inline"`
	Postings          []legacyLedgerPosting `bson:""`
}

func (v legacyLedgerEntryInput) value() ledger.EntryInput {
	out := v.EntryInput
	if v.Postings != nil {
		out.Postings = make([]ledger.Posting, len(v.Postings))
		for i, x := range v.Postings {
			out.Postings[i] = x.value()
		}
	}
	return out
}

type legacyBudgetLine struct {
	budget.Line `bson:",inline"`
	Key         string           `bson:"_id"`
	UserID      string           `bson:"user_id"`
	GroupRef    *budget.GroupRef `bson:"group_ref,omitempty"`
}

func (v legacyBudgetLine) value() budget.Line {
	out := v.Line
	out.Key = v.Key
	out.UserID = v.UserID
	out.GroupRef = v.GroupRef
	return out
}

type legacyClassifyRule struct {
	classify.Rule `bson:",inline"`
	Key           string `bson:"_id"`
	UserID        string `bson:"user_id"`
}

func (v legacyClassifyRule) value() classify.Rule {
	out := v.Rule
	out.Key = v.Key
	out.UserID = v.UserID
	return out
}

type legacyIdentityUser struct {
	identity.User `bson:",inline"`
	ID            string    `bson:"_id"`
	Admin         bool      `bson:"is_admin"`
	CreatedAt     time.Time `bson:"created_at"`
}

func (v legacyIdentityUser) value() identity.User {
	out := v.User
	out.ID = v.ID
	out.Admin = v.Admin
	out.CreatedAt = v.CreatedAt
	return out
}

type legacyIdentityCredential struct {
	ID   string `bson:"_id"`
	Hash []byte `bson:"hash"`
	Salt []byte `bson:"salt"`
}
type legacyIdentitySession struct {
	identity.Session `bson:",inline"`
	Hash             string    `bson:"_id"`
	UserID           string    `bson:"user_id"`
	ExpiresAt        time.Time `bson:"expires_at"`
}

func (v legacyIdentitySession) value() identity.Session {
	out := v.Session
	out.Hash = v.Hash
	out.UserID = v.UserID
	out.ExpiresAt = v.ExpiresAt
	return out
}

type legacyIdentityInvite struct {
	identity.Invite `bson:",inline"`
	Hash            string     `bson:"_id"`
	CreatedBy       string     `bson:"created_by"`
	ExpiresAt       time.Time  `bson:"expires_at"`
	UsedAt          *time.Time `bson:"used_at,omitempty"`
}

func (v legacyIdentityInvite) value() identity.Invite {
	out := v.Invite
	out.Hash = v.Hash
	out.CreatedBy = v.CreatedBy
	out.ExpiresAt = v.ExpiresAt
	out.UsedAt = v.UsedAt
	return out
}

type legacyOauthClient struct {
	oauth.Client `bson:",inline"`
	ID           string    `bson:"_id"`
	RedirectURIs []string  `bson:"redirect_uris"`
	AuthMethod   string    `bson:"auth_method"`
	SecretHash   string    `bson:"secret_hash,omitempty"`
	FetchedUntil time.Time `bson:"fetched_until,omitempty"`
}

func (v legacyOauthClient) value() oauth.Client {
	out := v.Client
	out.ID = v.ID
	out.RedirectURIs = v.RedirectURIs
	out.AuthMethod = v.AuthMethod
	out.SecretHash = v.SecretHash
	out.FetchedUntil = v.FetchedUntil
	return out
}

type legacyOauthCode struct {
	Hash      string    `bson:"_id"`
	ClientID  string    `bson:"client_id"`
	UserID    string    `bson:"user_id"`
	Redirect  string    `bson:"redirect_uri"`
	Challenge string    `bson:"challenge"`
	Resource  string    `bson:"resource"`
	Scopes    []string  `bson:"scopes"`
	ExpiresAt time.Time `bson:"expires_at"`
}
type legacyOauthGrant struct {
	oauth.Grant `bson:",inline"`
	ID          string    `bson:"_id"`
	UserID      string    `bson:"user_id"`
	ClientID    string    `bson:"client_id"`
	ClientName  string    `bson:"client_name"`
	CreatedAt   time.Time `bson:"created_at"`
	ExpiresAt   time.Time `bson:"expires_at"`
}

func (v legacyOauthGrant) value() oauth.Grant {
	out := v.Grant
	out.ID = v.ID
	out.UserID = v.UserID
	out.ClientID = v.ClientID
	out.ClientName = v.ClientName
	out.CreatedAt = v.CreatedAt
	out.ExpiresAt = v.ExpiresAt
	return out
}

type legacyOauthToken struct {
	Hash            string    `bson:"_id"`
	RefreshHash     string    `bson:"refresh_hash"`
	GrantID         string    `bson:"grant_id"`
	AccessExpiresAt time.Time `bson:"access_expires_at"`
	ExpiresAt       time.Time `bson:"expires_at"`
	Used            bool      `bson:"used"`
}
type legacyAdminConfirmation struct {
	admin.Confirmation `bson:",inline"`
	Hash               string          `bson:"_id"`
	UserID             string          `bson:"user_id"`
	TargetLabel        string          `bson:"target_label"`
	RequestedBy        principal.Actor `bson:"requested_by"`
	ExpiresAt          time.Time       `bson:"expires_at"`
	UsedAt             *time.Time      `bson:"used_at,omitempty"`
}

func (v legacyAdminConfirmation) value() admin.Confirmation {
	out := v.Confirmation
	out.Hash = v.Hash
	out.UserID = v.UserID
	out.TargetLabel = v.TargetLabel
	out.RequestedBy = v.RequestedBy
	out.ExpiresAt = v.ExpiresAt
	out.UsedAt = v.UsedAt
	return out
}

type legacyRatesQuote struct {
	rates.Quote `bson:",inline"`
	ID          string `bson:"_id"`
	UserID      string `bson:"user_id"`
}

func (v legacyRatesQuote) value() rates.Quote {
	out := v.Quote
	out.ID = v.ID
	out.UserID = v.UserID
	return out
}

type legacyRatesProviderState struct {
	rates.ProviderState `bson:",inline"`
	ID                  string `bson:"_id"`
	UserID              string `bson:"user_id"`
	LastAttempt         string `bson:"last_attempt,omitempty"`
	LastSuccess         string `bson:"last_success,omitempty"`
	ConsecutiveFailures int    `bson:"consecutive_failures"`
	NewestAt            string `bson:"newest_at,omitempty"`
	NewestValue         string `bson:"newest_value,omitempty"`
}

func (v legacyRatesProviderState) value() rates.ProviderState {
	out := v.ProviderState
	out.ID = v.ID
	out.UserID = v.UserID
	out.LastAttempt = v.LastAttempt
	out.LastSuccess = v.LastSuccess
	out.ConsecutiveFailures = v.ConsecutiveFailures
	out.NewestAt = v.NewestAt
	out.NewestValue = v.NewestValue
	return out
}

type legacyAuditEvent struct {
	audit.Event `bson:",inline"`
	UserID      string            `bson:"user_id"`
	ID          string            `bson:"_id"`
	Target      legacyAuditTarget `bson:"target"`
	GrantID     string            `bson:"grant_id,omitempty"`
	GrantHash   string            `bson:"grant_hash,omitempty"`
	BatchID     string            `bson:"batch_id,omitempty"`
	PrevHash    string            `bson:"prev_hash"`
}

func (v legacyAuditEvent) value() audit.Event {
	out := v.Event
	out.UserID = v.UserID
	out.ID = v.ID
	out.Target = audit.Target{Collection: v.Target.Collection, ID: v.Target.ID}
	out.GrantID = v.GrantID
	out.GrantHash = v.GrantHash
	out.BatchID = v.BatchID
	out.PrevHash = v.PrevHash
	return out
}

type legacyAuditOutcome struct {
	audit.Outcome `bson:",inline"`
	ID            string `bson:"_id"`
	UserID        string `bson:"user_id"`
}

func (v legacyAuditOutcome) value() audit.Outcome {
	out := v.Outcome
	out.ID = v.ID
	out.UserID = v.UserID
	return out
}

type legacySharingGrant struct {
	sharing.Grant `bson:",inline"`
	ID            string     `bson:"_id"`
	DashboardID   string     `bson:"dashboard_id"`
	OwnerUser     string     `bson:"owner_user"`
	GranteeUser   string     `bson:"grantee_user"`
	RowFilter     string     `bson:"row_filter,omitempty"`
	ExpiresAt     *time.Time `bson:"expires_at,omitempty"`
	RevokedAt     *time.Time `bson:"revoked_at,omitempty"`
	CreatedAt     time.Time  `bson:"created_at"`
	LastRefresh   time.Time  `bson:"last_refresh"`
}

func (v legacySharingGrant) value() sharing.Grant {
	out := v.Grant
	out.ID = v.ID
	out.DashboardID = v.DashboardID
	out.OwnerUser = v.OwnerUser
	out.GranteeUser = v.GranteeUser
	out.RowFilter = v.RowFilter
	out.ExpiresAt = v.ExpiresAt
	out.RevokedAt = v.RevokedAt
	out.CreatedAt = v.CreatedAt
	out.LastRefresh = v.LastRefresh
	return out
}

type legacySharingSnapshot struct {
	sharing.Snapshot `bson:",inline"`
	GrantHash        string `bson:"grant_hash"`
	ID               string `bson:"_id"`
	OwnerUser        string `bson:"owner_user"`
	DashboardID      string `bson:"dashboard_id"`
	Rendered         bson.M `bson:"rendered"`
}

func (v legacySharingSnapshot) value() sharing.Snapshot {
	out := v.Snapshot
	out.GrantHash = v.GrantHash
	out.ID = v.ID
	out.OwnerUser = v.OwnerUser
	out.DashboardID = v.DashboardID
	return out
}

type legacyFeedbackFlag struct {
	feedback.Flag `bson:",inline"`
	ID            string          `bson:"_id"`
	UserID        string          `bson:"user_id"`
	CreatedBy     principal.Actor `bson:"created_by"`
	CreatedAt     time.Time       `bson:"created_at"`
	ResolvedBy    principal.Actor `bson:"resolved_by,omitempty"`
	ResolvedAt    *time.Time      `bson:"resolved_at,omitempty"`
}

func (v legacyFeedbackFlag) value() feedback.Flag {
	out := v.Flag
	out.ID = v.ID
	out.UserID = v.UserID
	out.CreatedBy = v.CreatedBy
	out.CreatedAt = v.CreatedAt
	out.ResolvedBy = v.ResolvedBy
	out.ResolvedAt = v.ResolvedAt
	return out
}

type legacyFeedbackSavedView struct {
	feedback.SavedView `bson:",inline"`
	Key                string `bson:"_id"`
	UserID             string `bson:"user_id"`
}

func (v legacyFeedbackSavedView) value() feedback.SavedView {
	out := v.SavedView
	out.Key = v.Key
	out.UserID = v.UserID
	return out
}

type legacyModelScenario struct {
	model.Scenario `bson:",inline"`
	Key            string                   `bson:"_id"`
	UserID         string                   `bson:"user_id"`
	Legacy         *legacyModelLegacyInputs `bson:"legacy,omitempty"`
}

func (v legacyModelScenario) value() model.Scenario {
	out := v.Scenario
	out.Key = v.Key
	out.UserID = v.UserID
	if v.Legacy != nil {
		x := v.Legacy.value()
		out.Legacy = &x
	}
	return out
}

type legacyModelLegacyInputs struct {
	model.LegacyInputs       `bson:",inline"`
	SalaryBiweeklyUSD        float64 `bson:"salary_biweekly_usd"`
	ReimbursementBiweeklyUSD float64 `bson:"reimbursement_biweekly_usd"`
	SideIncomeMonthlyUSD     float64 `bson:"side_income_monthly_usd"`
	KyivExtraDrainMonthlyUSD float64 `bson:"kyiv_extra_drain_monthly_usd"`
	BufferUSD                float64 `bson:"buffer_usd"`
	YieldPct                 float64 `bson:"yield_pct"`
}

func (v legacyModelLegacyInputs) value() model.LegacyInputs {
	out := v.LegacyInputs
	out.SalaryBiweeklyUSD = v.SalaryBiweeklyUSD
	out.ReimbursementBiweeklyUSD = v.ReimbursementBiweeklyUSD
	out.SideIncomeMonthlyUSD = v.SideIncomeMonthlyUSD
	out.KyivExtraDrainMonthlyUSD = v.KyivExtraDrainMonthlyUSD
	out.BufferUSD = v.BufferUSD
	out.YieldPct = v.YieldPct
	return out
}

type legacyModelIncome struct {
	model.Income `bson:",inline"`
	GrowthPct    float64 `bson:"growth_pct,omitempty"`
	PayerMatch   string  `bson:"payer_match,omitempty"`
	LandsIn      string  `bson:"lands_in,omitempty"`
	GrossOrNet   string  `bson:"gross_or_net,omitempty"`
}

func (v legacyModelIncome) value() model.Income {
	out := v.Income
	out.GrowthPct = v.GrowthPct
	out.PayerMatch = v.PayerMatch
	out.LandsIn = v.LandsIn
	out.GrossOrNet = v.GrossOrNet
	return out
}

type legacyTablesTable struct {
	tables.Table `bson:",inline"`
	Key          string `bson:"_id"`
	UserID       string `bson:"user_id"`
}

func (v legacyTablesTable) value() tables.Table {
	out := v.Table
	out.Key = v.Key
	out.UserID = v.UserID
	return out
}

type legacyTablesRow struct {
	tables.Row `bson:",inline"`
	KeyHashes  []string `bson:"key_hashes,omitempty"`
	Key        string   `bson:"_id"`
	UserID     string   `bson:"user_id"`
	TableID    string   `bson:"table_id"`
}

func (v legacyTablesRow) value() tables.Row {
	out := v.Row
	out.KeyHashes = v.KeyHashes
	out.Key = v.Key
	out.UserID = v.UserID
	out.TableID = v.TableID
	return out
}

type legacyImportsBatch struct {
	imports.Batch  `bson:",inline"`
	ID             string                   `bson:"_id"`
	UserID         string                   `bson:"user_id"`
	FileSHA256     string                   `bson:"file_sha256"`
	Candidates     []legacyImportsCandidate `bson:"candidates"`
	ClosingBalance *legacyAmount            `bson:"closing_balance,omitempty"`
	CreatedAt      time.Time                `bson:"created_at"`
}

func (v legacyImportsBatch) value() imports.Batch {
	out := v.Batch
	out.ID = v.ID
	out.UserID = v.UserID
	out.FileSHA256 = v.FileSHA256
	if v.Candidates != nil {
		out.Candidates = make([]imports.Candidate, len(v.Candidates))
		for i, x := range v.Candidates {
			out.Candidates[i] = x.value()
		}
	}
	if v.ClosingBalance != nil {
		x := v.ClosingBalance.value()
		out.ClosingBalance = &x
	}
	out.CreatedAt = v.CreatedAt
	return out
}

type legacyImportsImportRow struct {
	imports.ImportRow `bson:",inline"`
	ID                string                 `bson:"_id"`
	UserID            string                 `bson:"user_id"`
	BatchID           string                 `bson:"batch_id"`
	DedupeKey         string                 `bson:"dedupe_key"`
	Proposed          legacyLedgerEntryInput `bson:"proposed"`
	EntryID           string                 `bson:"entry_id,omitempty"`
	ReversalID        string                 `bson:"reversal_id,omitempty"`
	ProposedID        string                 `bson:"proposed_id"`
}

func (v legacyImportsImportRow) value() imports.ImportRow {
	out := v.ImportRow
	out.ID = v.ID
	out.UserID = v.UserID
	out.BatchID = v.BatchID
	out.DedupeKey = v.DedupeKey
	out.Proposed = v.Proposed.value()
	out.EntryID = v.EntryID
	out.ReversalID = v.ReversalID
	out.ProposedID = v.ProposedID
	return out
}

type legacyImportsFeed struct {
	imports.Feed   `bson:",inline"`
	Key            string `bson:"_id"`
	UserID         string `bson:"user_id"`
	DateFormat     string `bson:"date_format,omitempty"`
	TokenEncrypted string `bson:"token_encrypted"`
}

func (v legacyImportsFeed) value() imports.Feed {
	out := v.Feed
	out.Key = v.Key
	out.UserID = v.UserID
	out.DateFormat = v.DateFormat
	out.TokenEncrypted = v.TokenEncrypted
	return out
}

type legacyImportsCandidate struct {
	imports.Candidate `bson:",inline"`
	RowID             string `bson:"row_id"`
}

func (v legacyImportsCandidate) value() imports.Candidate {
	out := v.Candidate
	out.RowID = v.RowID
	return out
}

type legacySemanticMetric struct {
	semantic.Metric `bson:",inline"`
	Key             string `bson:"_id"`
	UserID          string `bson:"user_id"`
}

func (v legacySemanticMetric) value() semantic.Metric {
	out := v.Metric
	out.Key = v.Key
	out.UserID = v.UserID
	return out
}

type legacySemanticLabel struct {
	semantic.Label `bson:",inline"`
	Doc            string `bson:"_id"`
	User           string `bson:"user_id"`
}

func (v legacySemanticLabel) value() semantic.Label {
	out := v.Label
	out.Doc = v.Doc
	out.User = v.User
	return out
}

type legacySemanticPayeeAlias struct {
	semantic.PayeeAlias `bson:",inline"`
	Key                 string `bson:"_id"`
	UserID              string `bson:"user_id"`
}

func (v legacySemanticPayeeAlias) value() semantic.PayeeAlias {
	out := v.PayeeAlias
	out.Key = v.Key
	out.UserID = v.UserID
	return out
}

type legacySemanticAnnotation struct {
	semantic.Annotation `bson:",inline"`
	Key                 string `bson:"_id"`
	UserID              string `bson:"user_id"`
	TagRule             string `bson:"tag_rule,omitempty"`
}

func (v legacySemanticAnnotation) value() semantic.Annotation {
	out := v.Annotation
	out.Key = v.Key
	out.UserID = v.UserID
	out.TagRule = v.TagRule
	return out
}

type legacySemanticNavDoc struct {
	Key    string             `bson:"_id"`
	UserID string             `bson:"user_id"`
	Items  []semantic.NavItem `bson:"items"`
}
type legacyDashboardHead struct {
	dashboard.Head   `bson:",inline"`
	PublishedBy      principal.Actor `bson:"published_by,omitempty"`
	PublishedAt      *time.Time      `bson:"published_at,omitempty"`
	PublishNote      string          `bson:"publish_note,omitempty"`
	Key              string          `bson:"_id"`
	UserID           string          `bson:"user_id"`
	PublishedVersion int             `bson:"published_version"`
	DraftVersion     int             `bson:"draft_version"`
}

func (v legacyDashboardHead) value() dashboard.Head {
	out := v.Head
	out.PublishedBy = v.PublishedBy
	out.PublishedAt = v.PublishedAt
	out.PublishNote = v.PublishNote
	out.Key = v.Key
	out.UserID = v.UserID
	out.PublishedVersion = v.PublishedVersion
	out.DraftVersion = v.DraftVersion
	return out
}

type legacyDashboardVersion struct {
	dashboard.Version `bson:",inline"`
	Key               string    `bson:"_id"`
	UserID            string    `bson:"user_id"`
	DashboardID       string    `bson:"dashboard_id"`
	CreatedAt         time.Time `bson:"created_at"`
}

func (v legacyDashboardVersion) value() dashboard.Version {
	out := v.Version
	out.Key = v.Key
	out.UserID = v.UserID
	out.DashboardID = v.DashboardID
	out.CreatedAt = v.CreatedAt
	return out
}

type legacyDashboardView struct {
	dashboard.View `bson:",inline"`
	Key            string `bson:"_id"`
	UserID         string `bson:"user_id"`
}

func (v legacyDashboardView) value() dashboard.View {
	out := v.View
	out.Key = v.Key
	out.UserID = v.UserID
	return out
}
