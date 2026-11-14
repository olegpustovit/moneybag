package main

import (
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"slices"
	"strings"
)

var mappings = []mapping{{name: "accounts", columns: []string{"user_id", "storage_key", "id", "type", "name", "pot", "commodities", "location", "class", "groups", "closed_at", "display_name", "note", "estimated"}, sourceKeys: []string{"user_id", "_id", "id", "type", "name", "pot", "commodities", "location", "class", "groups", "closed_at", "display_name", "note", "estimated"}},
	{name: "journal", columns: []string{"user_id", "id", "seq", "date", "kind", "payee", "postings", "tags", "links", "status", "actor", "created_at"}, sourceKeys: []string{"user_id", "_id", "seq", "date", "kind", "payee", "postings", "tags", "links", "status", "actor", "created_at"}},
	{name: "budgets", columns: []string{"archived", "storage_key", "user_id", "id", "name", "revision", "limit", "accounts", "group_ref", "order", "limit_lexeme"}, sourceKeys: []string{"archived", "_id", "user_id", "id", "name", "revision", "limit", "accounts", "group_ref", "order"}},
	{name: "rules", columns: []string{"storage_key", "user_id", "id", "priority", "match", "set", "note", "enabled", "version"}, sourceKeys: []string{"_id", "user_id", "id", "priority", "match", "set", "note", "enabled", "version"}},
	{name: "users", columns: []string{"id", "email", "name", "is_admin", "created_at", "disabled"}, sourceKeys: []string{"_id", "email", "name", "is_admin", "created_at", "disabled"}},
	{name: "credentials", columns: []string{"id", "hash", "salt"}, sourceKeys: []string{"_id", "hash", "salt"}},
	{name: "sessions", columns: []string{"id", "user_id", "csrf", "expires_at"}, sourceKeys: []string{"_id", "user_id", "csrf", "expires_at"}},
	{name: "invites", columns: []string{"id", "email", "created_by", "expires_at", "used_at"}, sourceKeys: []string{"_id", "email", "created_by", "expires_at", "used_at"}},
	{name: "oauth_clients", columns: []string{"id", "name", "redirect_uris", "auth_method", "secret_hash", "fetched_until"}, sourceKeys: []string{"_id", "name", "redirect_uris", "auth_method", "secret_hash", "fetched_until"}},
	{name: "oauth_codes", columns: []string{"id", "client_id", "user_id", "redirect_uri", "challenge", "resource", "scopes", "expires_at"}, sourceKeys: []string{"_id", "client_id", "user_id", "redirect_uri", "challenge", "resource", "scopes", "expires_at"}},
	{name: "oauth_grants", columns: []string{"id", "user_id", "client_id", "client_name", "resource", "scopes", "revoked", "created_at", "expires_at"}, sourceKeys: []string{"_id", "user_id", "client_id", "client_name", "resource", "scopes", "revoked", "created_at", "expires_at"}},
	{name: "oauth_tokens", columns: []string{"id", "refresh_hash", "grant_id", "access_expires_at", "expires_at", "used"}, sourceKeys: []string{"_id", "refresh_hash", "grant_id", "access_expires_at", "expires_at", "used"}},
	{name: "admin_confirmations", columns: []string{"id", "user_id", "action", "target", "target_label", "reason", "requested_by", "expires_at", "used_at"}, sourceKeys: []string{"_id", "user_id", "action", "target", "target_label", "reason", "requested_by", "expires_at", "used_at"}},
	{name: "prices", columns: []string{"archived", "id", "user_id", "pair", "base", "quote", "at", "value", "source", "corrected"}, sourceKeys: []string{"archived", "_id", "user_id", "pair", "base", "quote", "at", "value", "source", "corrected"}},
	{name: "rate_sync", columns: []string{"id", "user_id", "provider", "status", "last_attempt", "last_success", "error", "consecutive_failures", "newest_at", "newest_value"}, sourceKeys: []string{"_id", "user_id", "provider", "status", "last_attempt", "last_success", "error", "consecutive_failures", "newest_at", "newest_value"}},
	{name: "events", columns: []string{"user_id", "id", "seq", "at", "actor", "tool", "target", "before", "after", "reason", "grant_id", "grant_hash", "summary", "batch_id", "routine", "prev_hash", "hash", "actor_id", "target_id"}, sourceKeys: []string{"user_id", "_id", "seq", "at", "actor", "tool", "target", "before", "after", "reason", "grant_id", "grant_hash", "summary", "batch_id", "routine", "prev_hash", "hash"}},
	{name: "audit_outcomes", columns: []string{"hash", "id", "user_id", "status", "before", "after", "at"}, sourceKeys: []string{"hash", "_id", "user_id", "status", "before", "after", "at"}},
	{name: "shares", columns: []string{"id", "dashboard_id", "owner_user", "grantee_user", "role", "mode", "sources", "row_filter", "refresh", "expires_at", "revoked_at", "created_at", "last_refresh"}, sourceKeys: []string{"_id", "dashboard_id", "owner_user", "grantee_user", "role", "mode", "sources", "row_filter", "refresh", "expires_at", "revoked_at", "created_at", "last_refresh"}},
	{name: "dashboard_snapshots", columns: []string{"grant_hash", "id", "owner_user", "dashboard_id", "version", "title", "rendered", "at"}, sourceKeys: []string{"grant_hash", "_id", "owner_user", "dashboard_id", "version", "title", "rendered", "at"}},
	{name: "feedback", columns: []string{"id", "user_id", "target", "note", "status", "resolution", "answer", "created_by", "created_at", "resolved_by", "resolved_at"}, sourceKeys: []string{"_id", "user_id", "target", "note", "status", "resolution", "answer", "created_by", "created_at", "resolved_by", "resolved_at"}},
	{name: "saved_views", columns: []string{"storage_key", "user_id", "id", "name", "page", "state"}, sourceKeys: []string{"_id", "user_id", "id", "name", "page", "state"}},
	{name: "scenarios", columns: []string{"archived", "storage_key", "user_id", "id", "name", "revision", "overrides", "events", "legacy"}, sourceKeys: []string{"archived", "_id", "user_id", "id", "name", "revision", "overrides", "events", "legacy"}},
	{name: "tables", columns: []string{"keys", "checks", "ledger", "description", "archived", "storage_key", "user_id", "id", "name", "columns"}, sourceKeys: []string{"keys", "checks", "ledger", "description", "archived", "_id", "user_id", "id", "name", "columns"}},
	{name: "rows", columns: []string{"key_hashes", "archived", "storage_key", "user_id", "table_id", "id", "cells", "version", "decimal_cells"}, sourceKeys: []string{"joined", "key_hashes", "archived", "_id", "user_id", "table_id", "id", "cells", "version"}},
	{name: "import_batches", columns: []string{"id", "user_id", "account", "format", "file_sha256", "status", "candidates", "from", "to", "closing_balance", "created_at"}, sourceKeys: []string{"_id", "user_id", "account", "format", "file_sha256", "status", "candidates", "from", "to", "closing_balance", "created_at"}},
	{name: "import_rows", columns: []string{"id", "user_id", "batch_id", "account", "dedupe_key", "raw", "proposed", "kind", "entry_id", "reversal_id", "proposed_id", "skip"}, sourceKeys: []string{"_id", "user_id", "batch_id", "account", "dedupe_key", "raw", "proposed", "kind", "entry_id", "reversal_id", "proposed_id", "skip"}},
	{name: "feeds", columns: []string{"storage_key", "user_id", "id", "url", "account", "format", "date_format", "token_encrypted"}, sourceKeys: []string{"_id", "user_id", "id", "url", "account", "format", "date_format", "token_encrypted"}},
	{name: "metrics", columns: []string{"storage_key", "user_id", "id", "label", "description", "unit", "kind", "additivity", "per", "estimates", "accounts", "exclude", "sign", "where", "dimensions"}, sourceKeys: []string{"_id", "user_id", "id", "label", "description", "unit", "kind", "additivity", "per", "estimates", "accounts", "exclude", "sign", "where", "dimensions"}},
	{name: "labels", columns: []string{"storage_key", "user_id", "kind", "key", "label"}, sourceKeys: []string{"_id", "user_id", "kind", "key", "label"}},
	{name: "payee_aliases", columns: []string{"storage_key", "user_id", "pattern", "name", "note"}, sourceKeys: []string{"_id", "user_id", "pattern", "name", "note"}},
	{name: "annotations", columns: []string{"storage_key", "user_id", "id", "label", "from", "to", "tag_rule", "kind"}, sourceKeys: []string{"_id", "user_id", "id", "label", "from", "to", "tag_rule", "kind"}},
	{name: "nav", columns: []string{"storage_key", "user_id", "items"}, sourceKeys: []string{"_id", "user_id", "items"}},
	{name: "dashboards", columns: []string{"published_by", "published_at", "publish_note", "archived", "storage_key", "user_id", "id", "title", "published_version", "draft_version", "revision", "home"}, sourceKeys: []string{"published_by", "published_at", "publish_note", "archived", "_id", "user_id", "id", "title", "published_version", "draft_version", "revision", "home"}},
	{name: "dashboard_versions", columns: []string{"storage_key", "user_id", "dashboard_id", "version", "spec", "author", "created_at", "note"}, sourceKeys: []string{"_id", "user_id", "dashboard_id", "version", "spec", "author", "created_at", "note"}},
	{name: "views", columns: []string{"storage_key", "user_id", "id", "query"}, sourceKeys: []string{"_id", "user_id", "id", "query"}},
	{name: "counters", columns: []string{"id", "user_id", "name", "n", "rates"}, sourceKeys: []string{"_id", "user_id", "name", "n", "rates"}},
	{name: "settings", columns: []string{"id", "by", "at"}, sourceKeys: []string{"_id", "by", "at"}},
	{name: "usage", columns: []string{"id", "user_id", "date", "path", "dashboard", "views", "last_at"}, sourceKeys: []string{"_id", "user_id", "date", "path", "dashboard", "views", "last_at"}},
	{name: "import_files", columns: []string{"id", "user_id", "content", "sha256"}, sourceKeys: []string{"_id", "user_id", "content", "sha256"}},
	{name: "income_models", columns: []string{"id", "user_id", "streams"}, sourceKeys: []string{"_id", "user_id", "streams"}},
	{name: "balances", columns: []string{"user_id", "dirty"}, sourceKeys: []string{"_id", "user_id", "dirty"}},
	{name: "entry_tags", columns: []string{"id", "user_id", "entry_id", "tags"}, sourceKeys: []string{"_id", "user_id", "entry_id", "tags"}},
	{name: "balance_assertions", columns: []string{"id", "user_id", "account", "date", "amount", "source", "ok", "diff", "archived"}, sourceKeys: []string{"_id", "user_id", "account", "date", "amount", "source", "ok", "diff", "archived"}},
	{name: "discrepancies", columns: []string{"id", "user_id", "assertion_id", "account", "diff", "status", "resolution", "reason", "adjustment_id", "archived"}, sourceKeys: []string{"_id", "user_id", "assertion_id", "account", "diff", "status", "resolution", "reason", "adjustment_id", "archived"}},
}

func decodeRecord(table string, raw bson.Raw) ([]any, error) {
	switch table {
	case "accounts":
		var src legacyLedgerAccount
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.UserID, v.Key, v.ID, v.Type, v.Name, v.Pot, v.Commodities, v.Location, v.Class, v.Groups, v.ClosedAt, v.DisplayName, v.Note, v.Estimated}, nil
	case "journal":
		var src legacyLedgerEntry
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.UserID, v.ID, v.Seq, v.Date, v.Kind, v.Payee, v.Postings, v.Tags, v.Links, v.Status, v.Actor, v.CreatedAt}, nil
	case "budgets":
		var src legacyBudgetLine
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		var lexeme *string
		if v.Limit.Rat().Sign() == 0 && strings.HasPrefix(v.Limit.String(), "-") {
			s := v.Limit.String()
			lexeme = &s
		}
		return []any{v.Archived, v.Key, v.UserID, v.ID, v.Name, v.Revision, v.Limit, v.Accounts, v.GroupRef, v.Order, lexeme}, nil
	case "rules":
		var src legacyClassifyRule
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Key, v.UserID, v.ID, v.Priority, v.Match, v.Set, v.Note, v.Enabled, v.Version}, nil
	case "users":
		var src legacyIdentityUser
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.ID, v.Email, v.Name, v.Admin, v.CreatedAt, v.Disabled}, nil
	case "credentials":
		var src legacyIdentityCredential
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src
		return []any{v.ID, v.Hash, v.Salt}, nil
	case "sessions":
		var src legacyIdentitySession
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Hash, v.UserID, v.CSRF, v.ExpiresAt}, nil
	case "invites":
		var src legacyIdentityInvite
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Hash, v.Email, v.CreatedBy, v.ExpiresAt, v.UsedAt}, nil
	case "oauth_clients":
		var src legacyOauthClient
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.ID, v.Name, v.RedirectURIs, v.AuthMethod, v.SecretHash, v.FetchedUntil}, nil
	case "oauth_codes":
		var src legacyOauthCode
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src
		return []any{v.Hash, v.ClientID, v.UserID, v.Redirect, v.Challenge, v.Resource, v.Scopes, v.ExpiresAt}, nil
	case "oauth_grants":
		var src legacyOauthGrant
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.ID, v.UserID, v.ClientID, v.ClientName, v.Resource, v.Scopes, v.Revoked, v.CreatedAt, v.ExpiresAt}, nil
	case "oauth_tokens":
		var src legacyOauthToken
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src
		return []any{v.Hash, v.RefreshHash, v.GrantID, v.AccessExpiresAt, v.ExpiresAt, v.Used}, nil
	case "admin_confirmations":
		var src legacyAdminConfirmation
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Hash, v.UserID, v.Action, v.Target, v.TargetLabel, v.Reason, v.RequestedBy, v.ExpiresAt, v.UsedAt}, nil
	case "prices":
		var src legacyRatesQuote
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Archived, v.ID, v.UserID, v.Pair, v.Base, v.Quote, v.At, v.Value, v.Source, v.Corrected}, nil
	case "rate_sync":
		var src legacyRatesProviderState
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.ID, v.UserID, v.Provider, v.Status, v.LastAttempt, v.LastSuccess, v.Error, v.ConsecutiveFailures, v.NewestAt, v.NewestValue}, nil
	case "events":
		var src legacyAuditEvent
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.UserID, v.ID, v.Seq, v.At, v.Actor, v.Tool, v.Target, v.Before, v.After, v.Reason, v.GrantID, v.GrantHash, v.Summary, v.BatchID, v.Routine, v.PrevHash, v.Hash, v.Actor.ID, v.Target.ID}, nil
	case "audit_outcomes":
		var src legacyAuditOutcome
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Hash, v.ID, v.UserID, v.Status, v.Before, v.After, v.At}, nil
	case "shares":
		var src legacySharingGrant
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.ID, v.DashboardID, v.OwnerUser, v.GranteeUser, v.Role, v.Mode, v.Sources, v.RowFilter, v.Refresh, v.ExpiresAt, v.RevokedAt, v.CreatedAt, v.LastRefresh}, nil
	case "dashboard_snapshots":
		var src legacySharingSnapshot
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		rendering, err := snapshotRendering(src.Rendered)
		if err != nil {
			return nil, err
		}
		return []any{v.GrantHash, v.ID, v.OwnerUser, v.DashboardID, v.Version, v.Title, rendering, v.At}, nil
	case "feedback":
		var src legacyFeedbackFlag
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.ID, v.UserID, v.Target, v.Note, v.Status, v.Resolution, v.Answer, v.CreatedBy, v.CreatedAt, v.ResolvedBy, v.ResolvedAt}, nil
	case "saved_views":
		var src legacyFeedbackSavedView
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Key, v.UserID, v.ID, v.Name, v.Page, v.State}, nil
	case "scenarios":
		var src legacyModelScenario
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Archived, v.Key, v.UserID, v.ID, v.Name, v.Revision, v.Overrides, v.Events, v.Legacy}, nil
	case "tables":
		var src legacyTablesTable
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Keys, v.Checks, v.Ledger, v.Description, v.Archived, v.Key, v.UserID, v.ID, v.Name, v.Columns}, nil
	case "rows":
		var src legacyTablesRow
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		decimalCells := []string{}
		for id, value := range v.Cells {
			if decimal, ok := value.(bson.Decimal128); ok {
				number, err := decimalFromLegacy(decimal)
				if err != nil {
					return nil, err
				}
				v.Cells[id] = number
				decimalCells = append(decimalCells, id)
			}
		}
		slices.Sort(decimalCells)
		return []any{v.KeyHashes, v.Archived, v.Key, v.UserID, v.TableID, v.ID, v.Cells, v.Version, decimalCells}, nil
	case "import_batches":
		var src legacyImportsBatch
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.ID, v.UserID, v.Account, v.Format, v.FileSHA256, v.Status, v.Candidates, v.From, v.To, v.ClosingBalance, v.CreatedAt}, nil
	case "import_rows":
		var src legacyImportsImportRow
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.ID, v.UserID, v.BatchID, v.Account, v.DedupeKey, v.Raw, v.Proposed, v.Kind, v.EntryID, v.ReversalID, v.ProposedID, v.Skip}, nil
	case "feeds":
		var src legacyImportsFeed
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Key, v.UserID, v.ID, v.URL, v.Account, v.Format, v.DateFormat, v.TokenEncrypted}, nil
	case "metrics":
		var src legacySemanticMetric
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Key, v.UserID, v.ID, v.Label, v.Description, v.Unit, v.Kind, v.Additivity, v.Per, v.Estimates, v.Accounts, v.Exclude, v.Sign, v.Where, v.Dimensions}, nil
	case "labels":
		var src legacySemanticLabel
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Doc, v.User, v.Kind, v.Key, v.Label}, nil
	case "payee_aliases":
		var src legacySemanticPayeeAlias
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Key, v.UserID, v.Pattern, v.Name, v.Note}, nil
	case "annotations":
		var src legacySemanticAnnotation
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Key, v.UserID, v.ID, v.Label, v.From, v.To, v.TagRule, v.Kind}, nil
	case "nav":
		var src legacySemanticNavDoc
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src
		return []any{v.Key, v.UserID, v.Items}, nil
	case "dashboards":
		var src legacyDashboardHead
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.PublishedBy, v.PublishedAt, v.PublishNote, v.Archived, v.Key, v.UserID, v.ID, v.Title, v.PublishedVersion, v.DraftVersion, v.Revision, v.Home}, nil
	case "dashboard_versions":
		var src legacyDashboardVersion
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Key, v.UserID, v.DashboardID, v.Version, v.Spec, v.Author, v.CreatedAt, v.Note}, nil
	case "views":
		var src legacyDashboardView
		if err := decodeLegacy(raw, &src); err != nil {
			return nil, err
		}
		v := src.value()
		return []any{v.Key, v.UserID, v.ID, v.Query}, nil
	case "counters":
		var v counterRecord
		if err := decodeLegacy(raw, &v); err != nil {
			return nil, err
		}
		return []any{v.ID, v.UserID, v.Name, v.N, v.Rates}, nil
	case "settings":
		var v settingRecord
		if err := decodeLegacy(raw, &v); err != nil {
			return nil, err
		}
		return []any{v.ID, v.By, v.At}, nil
	case "usage":
		var v usageRecord
		if err := decodeLegacy(raw, &v); err != nil {
			return nil, err
		}
		return []any{v.ID, v.UserID, v.Date, v.Path, v.Dashboard, v.Views, v.LastAt}, nil
	case "import_files":
		var v fileRecord
		if err := decodeLegacy(raw, &v); err != nil {
			return nil, err
		}
		return []any{v.ID, v.UserID, v.Content, v.SHA256}, nil
	case "income_models":
		var v incomeRecord
		if err := decodeLegacy(raw, &v); err != nil {
			return nil, err
		}
		return []any{v.ID, v.UserID, v.streams()}, nil
	case "balances":
		var v balanceRecord
		if err := decodeLegacy(raw, &v); err != nil {
			return nil, err
		}
		return []any{v.UserID, v.Dirty}, nil
	case "entry_tags":
		var v tagRecord
		if err := decodeLegacy(raw, &v); err != nil {
			return nil, err
		}
		return []any{v.ID, v.UserID, v.EntryID, v.Tags}, nil
	case "balance_assertions":
		var v assertionRecord
		if err := decodeLegacy(raw, &v); err != nil {
			return nil, err
		}
		return []any{v.ID, v.UserID, v.Account, v.Date, v.amount(), v.Source, v.OK, v.Diff, v.Archived}, nil
	case "discrepancies":
		var v discrepancyRecord
		if err := decodeLegacy(raw, &v); err != nil {
			return nil, err
		}
		return []any{v.ID, v.UserID, v.AssertionID, v.Account, v.diff(), v.Status, v.Resolution, v.Reason, v.AdjustmentID, v.Archived}, nil
	}
	return nil, fmt.Errorf("unmapped collection %q", table)
}

var columnKinds = map[string]map[string]columnKind{
	"audit_outcomes":      {"hash": kindText, "id": kindText, "user_id": kindText, "status": kindText, "before": kindBytes, "after": kindBytes, "at": kindTime},
	"rate_sync":           {"id": kindText, "user_id": kindText, "provider": kindText, "status": kindText, "last_attempt": kindText, "last_success": kindText, "error": kindText, "consecutive_failures": kindInteger, "newest_at": kindText, "newest_value": kindText},
	"balance_assertions":  {"id": kindText, "user_id": kindText, "account": kindText, "date": kindText, "amount": kindJSON, "source": kindText, "ok": kindBool, "diff": kindNumeric, "archived": kindBool},
	"labels":              {"storage_key": kindText, "user_id": kindText, "kind": kindText, "key": kindText, "label": kindText},
	"oauth_grants":        {"id": kindText, "user_id": kindText, "client_id": kindText, "client_name": kindText, "resource": kindText, "scopes": kindArray, "revoked": kindBool, "created_at": kindTime, "expires_at": kindTime},
	"admin_confirmations": {"id": kindText, "user_id": kindText, "action": kindText, "target": kindText, "target_label": kindText, "reason": kindText, "requested_by": kindJSON, "expires_at": kindTime, "used_at": kindTime},
	"shares":              {"id": kindText, "dashboard_id": kindText, "owner_user": kindText, "grantee_user": kindText, "role": kindText, "mode": kindText, "sources": kindArray, "row_filter": kindText, "refresh": kindText, "expires_at": kindTime, "revoked_at": kindTime, "created_at": kindTime, "last_refresh": kindTime},
	"rules":               {"storage_key": kindText, "user_id": kindText, "id": kindText, "priority": kindInteger, "match": kindText, "set": kindJSON, "note": kindText, "enabled": kindBool, "version": kindInteger},
	"income_models":       {"id": kindText, "user_id": kindText, "streams": kindJSON},
	"views":               {"storage_key": kindText, "user_id": kindText, "id": kindText, "query": kindJSON},
	"metrics":             {"storage_key": kindText, "user_id": kindText, "id": kindText, "label": kindText, "description": kindText, "unit": kindText, "kind": kindText, "additivity": kindText, "per": kindText, "estimates": kindText, "accounts": kindArray, "exclude": kindArray, "sign": kindInteger, "where": kindText, "dimensions": kindArray},
	"nav":                 {"storage_key": kindText, "user_id": kindText, "items": kindJSON},
	"oauth_codes":         {"id": kindText, "client_id": kindText, "user_id": kindText, "redirect_uri": kindText, "challenge": kindText, "resource": kindText, "scopes": kindArray, "expires_at": kindTime},
	"entry_tags":          {"id": kindText, "user_id": kindText, "entry_id": kindText, "tags": kindArray},
	"import_batches":      {"id": kindText, "user_id": kindText, "account": kindText, "format": kindText, "file_sha256": kindText, "status": kindText, "candidates": kindJSON, "from": kindText, "to": kindText, "closing_balance": kindJSON, "created_at": kindTime},
	"dashboards":          {"storage_key": kindText, "user_id": kindText, "id": kindText, "title": kindText, "published_version": kindInteger, "draft_version": kindInteger, "revision": kindInteger, "home": kindBool, "archived": kindBool, "published_by": kindJSON, "published_at": kindTime, "publish_note": kindText},
	"dashboard_versions":  {"storage_key": kindText, "user_id": kindText, "dashboard_id": kindText, "version": kindInteger, "spec": kindJSON, "author": kindJSON, "created_at": kindTime, "note": kindText},
	"settings":            {"id": kindText, "by": kindText, "at": kindTime},
	"oauth_clients":       {"id": kindText, "name": kindText, "redirect_uris": kindArray, "auth_method": kindText, "secret_hash": kindText, "fetched_until": kindTime},
	"accounts":            {"user_id": kindText, "storage_key": kindText, "id": kindText, "type": kindText, "name": kindText, "pot": kindText, "commodities": kindArray, "location": kindText, "class": kindText, "groups": kindJSON, "closed_at": kindTime, "display_name": kindText, "note": kindText, "estimated": kindBool},
	"users":               {"id": kindText, "email": kindText, "name": kindText, "is_admin": kindBool, "created_at": kindTime, "disabled": kindBool},
	"credentials":         {"id": kindText, "hash": kindBytes, "salt": kindBytes},
	"invites":             {"id": kindText, "email": kindText, "created_by": kindText, "expires_at": kindTime, "used_at": kindTime},
	"balances":            {"pk": kindInteger, "user_id": kindText, "dirty": kindBool},
	"rows":                {"storage_key": kindText, "user_id": kindText, "table_id": kindText, "id": kindText, "cells": kindJSON, "version": kindInteger, "key_hashes": kindArray, "decimal_cells": kindArray, "archived": kindBool},
	"annotations":         {"storage_key": kindText, "user_id": kindText, "id": kindText, "label": kindText, "from": kindText, "to": kindText, "tag_rule": kindText, "kind": kindText},
	"counters":            {"id": kindText, "user_id": kindText, "name": kindText, "n": kindInteger, "rates": kindInteger},
	"discrepancies":       {"id": kindText, "user_id": kindText, "assertion_id": kindText, "account": kindText, "diff": kindJSON, "status": kindText, "resolution": kindText, "reason": kindText, "adjustment_id": kindText, "archived": kindBool},
	"scenarios":           {"storage_key": kindText, "user_id": kindText, "id": kindText, "name": kindText, "revision": kindInteger, "overrides": kindJSON, "events": kindJSON, "legacy": kindJSON, "archived": kindBool},
	"import_rows":         {"id": kindText, "user_id": kindText, "batch_id": kindText, "account": kindText, "dedupe_key": kindText, "raw": kindJSON, "proposed": kindJSON, "kind": kindText, "entry_id": kindText, "reversal_id": kindText, "proposed_id": kindText, "skip": kindBool},
	"import_files":        {"id": kindText, "user_id": kindText, "content": kindBytes, "sha256": kindText},
	"feeds":               {"storage_key": kindText, "user_id": kindText, "id": kindText, "url": kindText, "account": kindText, "format": kindText, "date_format": kindText, "token_encrypted": kindText},
	"saved_views":         {"storage_key": kindText, "user_id": kindText, "id": kindText, "name": kindText, "page": kindText, "state": kindJSON},
	"usage":               {"id": kindText, "user_id": kindText, "date": kindText, "path": kindText, "dashboard": kindText, "views": kindInteger, "last_at": kindText},
	"journal":             {"user_id": kindText, "id": kindText, "seq": kindInteger, "date": kindText, "kind": kindText, "payee": kindText, "postings": kindJSON, "tags": kindArray, "links": kindJSON, "status": kindText, "actor": kindJSON, "created_at": kindTime},
	"budgets":             {"storage_key": kindText, "user_id": kindText, "id": kindText, "name": kindText, "revision": kindInteger, "limit": kindNumeric, "limit_lexeme": kindText, "accounts": kindArray, "group_ref": kindJSON, "order": kindInteger, "archived": kindBool},
	"tables":              {"storage_key": kindText, "user_id": kindText, "id": kindText, "name": kindText, "columns": kindJSON, "keys": kindJSON, "checks": kindJSON, "ledger": kindJSON, "description": kindText, "archived": kindBool},
	"payee_aliases":       {"storage_key": kindText, "user_id": kindText, "pattern": kindText, "name": kindText, "note": kindText},
	"prices":              {"id": kindText, "user_id": kindText, "pair": kindText, "base": kindText, "quote": kindText, "at": kindTime, "value": kindNumeric, "source": kindText, "archived": kindBool, "corrected": kindBool},
	"feedback":            {"id": kindText, "user_id": kindText, "target": kindJSON, "note": kindText, "status": kindText, "resolution": kindText, "answer": kindText, "created_by": kindJSON, "created_at": kindTime, "resolved_by": kindJSON, "resolved_at": kindTime},
	"sessions":            {"id": kindText, "user_id": kindText, "csrf": kindText, "expires_at": kindTime},
	"oauth_tokens":        {"id": kindText, "refresh_hash": kindText, "grant_id": kindText, "access_expires_at": kindTime, "expires_at": kindTime, "used": kindBool},
	"dashboard_snapshots": {"id": kindText, "grant_hash": kindText, "owner_user": kindText, "dashboard_id": kindText, "version": kindInteger, "title": kindText, "rendered": kindJSON, "at": kindTime},
	"events":              {"user_id": kindText, "id": kindText, "seq": kindInteger, "at": kindTime, "actor": kindJSON, "actor_id": kindText, "tool": kindText, "target": kindJSON, "target_id": kindText, "before": kindBytes, "after": kindBytes, "reason": kindText, "grant_id": kindText, "grant_hash": kindText, "summary": kindText, "batch_id": kindText, "routine": kindBool, "prev_hash": kindText, "hash": kindText},
}
