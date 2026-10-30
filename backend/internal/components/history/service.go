package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/budget"
	"github.com/olegpustovit/moneybag/backend/internal/components/classify"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/components/tables"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"slices"
	"strings"
)

type Service struct {
	Reconcile *reconcile.Service
	Audit     *audit.Service
	Ledger    *ledger.Service
	Budget    *budget.Service
	Classify  *classify.Service
	Dashboard *dashboard.Service
	Imports   *imports.Service
	Model     *model.Service
	Rates     *rates.Service
	Sharing   *sharing.Service
	Tables    *tables.Service
}
type UndoInput struct {
	EventID string `json:"event_id"`
	Reason  string `json:"reason"`
}
type UndoResult struct {
	InverseEventIDs []string `json:"inverse_event_ids"`
}
type HistoryResult struct {
	Events     []audit.Event              `json:"events"`
	Versions   []dashboard.VersionSummary `json:"versions"`
	NextCursor int64                      `json:"next_cursor,omitempty"`
}

func (s *Service) Of(ctx context.Context, p principal.Principal, id string, includePayload bool) (HistoryResult, error) {
	v, e := s.Audit.List(ctx, p, audit.ListInput{Target: id})
	if e != nil {
		return HistoryResult{}, e
	}
	if !includePayload {
		for i := range v.Events {
			v.Events[i].Before, v.Events[i].After = nil, nil
		}
	}
	out := HistoryResult{Events: v.Events, Versions: []dashboard.VersionSummary{}, NextCursor: v.NextCursor}
	versions, e := s.Dashboard.Versions(ctx, p, id)
	if e == nil {
		out.Versions = versions
	} else if !errors.Is(e, principal.ErrNotFound) {
		return out, e
	}
	return out, nil
}

// UndoShared limits an inverse to the current editor's own write through this grant.
func (s *Service) UndoShared(ctx context.Context, p principal.Principal, access sharing.Access, in UndoInput) (UndoResult, error) {
	var result UndoResult
	err := s.Sharing.EditorWrite(ctx, p, access, func(tx context.Context, current sharing.Access) error {
		event, err := s.Audit.Get(tx, current.Principal, in.EventID)
		if err != nil {
			return principal.ErrNotFound
		}
		if event.GrantID != current.Grant.ID || event.GrantHash != sharing.PermissionHash(*current.Grant) || event.Actor != p.Actor || event.Target.Collection != "dashboards" || event.Target.ID != current.ID || (event.Tool != "dashboard_patch" && event.Tool != "dashboard_publish") {
			return principal.ErrForbidden
		}
		changed, err := s.Audit.HasLaterGrantChange(tx, current.Principal, current.Grant.ID, event.Seq)
		if err != nil {
			return err
		}
		if changed {
			return fmt.Errorf("dashboard grant changed after this event: %w", principal.ErrConflict)
		}
		scoped := *s
		scoped.Dashboard = current.Dashboard
		owner := current.Principal
		owner.Scopes = append(append([]string(nil), owner.Scopes...), "ledger:write")
		result, err = scoped.Undo(tx, owner, in)
		return err
	})
	return result, err
}

func (s *Service) Undo(ctx context.Context, p principal.Principal, in UndoInput) (UndoResult, error) {
	if e := p.Require("ledger:write"); e != nil {
		return UndoResult{}, e
	}
	if strings.TrimSpace(in.Reason) == "" {
		return UndoResult{}, fmt.Errorf("reason is required")
	}
	result := UndoResult{InverseEventIDs: []string{}}
	e := s.Audit.Workflow(ctx, func(tx context.Context) error {
		event, e := s.Audit.Get(tx, p, in.EventID)
		if e != nil {
			return e
		}
		if event.Outcome == nil || event.Outcome.Status != "completed" {
			return fmt.Errorf("only completed mutations can be undone; review failed or interrupted workflows first")
		}
		switch event.Tool {
		case "dashboard_publish", "dashboard_unpublish", "dashboard_add_component", "dashboard_update_component", "dashboard_remove_component", "dashboard_compose", "dashboard_patch":
			later, err := s.Audit.HasLaterTarget(tx, p, event.Target, event.Seq)
			if err != nil {
				return err
			}
			if later {
				return fmt.Errorf("dashboard changed after this event: %w", principal.ErrConflict)
			}
		}
		latest, e := s.Audit.List(tx, p, audit.ListInput{Limit: 1})
		if e != nil {
			return e
		}
		lastSeq := int64(0)
		if len(latest.Events) > 0 {
			lastSeq = latest.Events[0].Seq
		}
		_, e = s.Audit.Write(tx, p, "undo", audit.Target{Collection: "events", ID: event.ID}, in.Reason, event, &result, func(tx context.Context) error {
			if e := s.inverse(tx, p, event, in.Reason); e != nil {
				return e
			}
			page, e := s.Audit.List(tx, p, audit.ListInput{Limit: 200})
			if e != nil {
				return e
			}
			for {
				stop := false
				for _, next := range page.Events {
					if next.Seq <= lastSeq {
						stop = true
						break
					}
					if next.Tool != "undo" {
						result.InverseEventIDs = append(result.InverseEventIDs, next.ID)
					}
				}
				if stop || page.NextCursor == 0 {
					break
				}
				page, e = s.Audit.List(tx, p, audit.ListInput{Limit: 200, Cursor: page.NextCursor})
				if e != nil {
					return e
				}
			}
			slices.Reverse(result.InverseEventIDs)
			return nil
		})
		return e
	})
	return result, e
}
func decodeBefore[T any](event audit.Event) (T, error) {
	var out T
	b := event.Before
	if event.Outcome != nil {
		b = event.Outcome.Before
	}
	e := json.Unmarshal(b, &out)
	return out, e
}
func decodeAfter[T any](event audit.Event) (T, error) {
	var out T
	b := event.After
	if event.Outcome != nil {
		b = event.Outcome.After
	}
	e := json.Unmarshal(b, &out)
	return out, e
}
func (s *Service) inverse(ctx context.Context, p principal.Principal, event audit.Event, reason string) error {
	id := event.Target.ID
	switch event.Tool {
	case "entry_post", "entry_reverse", "transfer_record", "reclassify":
		if event.Target.Collection != "journal" {
			return fmt.Errorf("undo the linked journal adjustment event to reverse this resolution")
		}
		entry, e := s.Ledger.Get(ctx, p, id)
		if e != nil {
			return e
		}
		if entry.Status == "draft" {
			return s.Ledger.SetDraftStatus(ctx, p, id, "cancelled", reason)
		}
		_, e = s.Ledger.Reverse(ctx, p, ledger.ReverseInput{ID: id, Reason: reason})
		return e
	case "entry_update":
		old, e := decodeBefore[ledger.Entry](event)
		if e != nil {
			return e
		}
		_, e = s.Ledger.UpdateDraft(ctx, p, ledger.EntryInput{ID: id, Date: old.Date, Payee: old.Payee, Postings: old.Postings, Tags: old.Tags})
		return e
	case "entry_draft_status":
		old, e := decodeBefore[ledger.Entry](event)
		if e != nil {
			return e
		}
		return s.Ledger.SetDraftStatus(ctx, p, id, old.Status, reason)
	case "entry_commit":
		returnError := fmt.Errorf("posted entries stay immutable; undo the entry with entry_reverse")
		entry, e := s.Ledger.Get(ctx, p, id)
		if e != nil {
			return e
		}
		if entry.Status == "posted" {
			_, e = s.Ledger.Reverse(ctx, p, ledger.ReverseInput{ID: id, Reason: reason})
			return e
		}
		return returnError
	case "reconcile_resolve":
		if event.Target.Collection == "journal" {
			_, e := s.Ledger.Reverse(ctx, p, ledger.ReverseInput{ID: id, Reason: reason})
			return e
		}
		return s.Reconcile.Reopen(ctx, p, id, reason)
	case "balance_assert":
		old, e := decodeBefore[reconcile.Assertion](event)
		if e != nil {
			return e
		}
		return s.Reconcile.RestoreAssertion(ctx, p, id, old, reason)
	case "assertion_archive":
		a, e := s.Reconcile.Assertion(ctx, p, id)
		if e != nil {
			return e
		}
		return s.Reconcile.RestoreAssertion(ctx, p, id, a, reason)
	case "import_start":
		return s.Imports.SetStagedStatus(ctx, p, id, "cancelled", reason)
	case "import_status":
		old, e := decodeBefore[imports.Batch](event)
		if e != nil {
			return e
		}
		return s.Imports.SetStagedStatus(ctx, p, id, old.Status, reason)
	case "import_undo":
		_, e := s.Imports.Redo(ctx, p, id, reason)
		return e
	case "import_redo":
		_, e := s.Imports.Undo(ctx, p, id, reason)
		return e
	case "import_commit":
		if event.Target.Collection == "journal" {
			_, e := s.Ledger.Reverse(ctx, importWriter(p), ledger.ReverseInput{ID: id, Reason: reason})
			return e
		}
		_, e := s.Imports.Undo(ctx, p, id, reason)
		return e
	case "account_create":
		closed := true
		_, e := s.Ledger.UpdateAccount(ctx, p, ledger.AccountUpdate{ID: id, Close: &closed})
		return e
	case "account_update":
		old, e := decodeBefore[ledger.Account](event)
		if e != nil {
			return e
		}
		closed := old.ClosedAt != nil
		_, e = s.Ledger.UpdateAccount(ctx, p, ledger.AccountUpdate{ID: id, Name: &old.Name, Pot: &old.Pot, Location: &old.Location, Class: &old.Class, Groups: &old.Groups, Close: &closed})
		return e
	case "tags_set":
		olds, e := decodeBefore[map[string][]string](event)
		if e != nil { // old single-entry format: a plain tag list
			old, e := decodeBefore[[]string](event)
			if e != nil {
				return e
			}
			olds = map[string][]string{id: old}
		}
		for eid, old := range olds {
			current, e := s.Ledger.Get(ctx, p, eid)
			if e != nil {
				return e
			}
			if _, e = s.Ledger.SetTags(ctx, p, ledger.TagInput{EntryIDs: []string{eid}, Add: old, Remove: current.Tags}); e != nil {
				return e
			}
		}
		return nil
	case "budget_set_line":
		old, e := decodeBefore[budget.Line](event)
		if e != nil {
			return e
		}
		if old.ID == "" {
			return s.Budget.Archive(ctx, p, id, true, reason)
		}
		group := old.GroupRef
		if group == nil {
			group = &budget.GroupRef{}
		}
		_, e = s.Budget.Set(ctx, p, budget.SetInput{ID: id, Name: &old.Name, Limit: &old.Limit, Accounts: &old.Accounts, GroupRef: group, Order: &old.Order})
		if e == nil && old.Archived {
			e = s.Budget.Archive(ctx, p, id, true, reason)
		}
		return e
	case "budget_archive":
		old, e := decodeBefore[struct {
			Archived bool `json:"archived"`
		}](event)
		if e != nil {
			return e
		}
		return s.Budget.Archive(ctx, p, id, old.Archived, reason)
	case "rule_upsert":
		old, e := decodeBefore[classify.Rule](event)
		if e != nil {
			return e
		}
		if old.ID == "" {
			old, e = decodeAfter[classify.Rule](event)
			if e != nil {
				return e
			}
			old.Enabled = false
		}
		_, e = s.Classify.Upsert(ctx, p, classify.RuleInput{ID: id, Priority: old.Priority, Match: old.Match, Set: old.Set, Note: old.Note, Enabled: &old.Enabled})
		return e
	case "rate_set":
		old, e := decodeBefore[rates.Quote](event)
		if e != nil {
			return e
		}
		if old.ID == "" {
			return s.Rates.Archive(ctx, p, id, true, reason)
		}
		_, e = s.Rates.Set(ctx, p, rates.SetInput{Pair: old.Pair, At: old.At, Value: old.Value, Source: old.Source, Provider: !old.Corrected})
		if e == nil && old.Archived {
			e = s.Rates.Archive(ctx, p, id, true, reason)
		}
		return e
	case "rate_archive":
		old, e := decodeBefore[struct {
			Archived bool `json:"archived"`
		}](event)
		if e != nil {
			return e
		}
		return s.Rates.Archive(ctx, p, id, old.Archived, reason)
	case "db_create":
		return s.Tables.Archive(ctx, p, id, true, reason)
	case "db_archive":
		old, e := decodeBefore[struct {
			Archived bool `json:"archived"`
		}](event)
		if e != nil {
			return e
		}
		return s.Tables.Archive(ctx, p, id, old.Archived, reason)
	case "db_alter":
		old, e := decodeBefore[tables.ColumnsState](event)
		if e != nil {
			return e
		}
		return s.Tables.SetColumns(ctx, p, id, old.Columns, reason)
	case "db_update_cells":
		old, e := decodeBefore[[]tables.Row](event)
		if e != nil {
			return e
		}
		return s.Tables.RestoreRows(ctx, p, id, old, reason)
	case "scenario_upsert":
		old, e := decodeBefore[model.Scenario](event)
		if e != nil {
			return e
		}
		if old.ID == "" {
			return s.Model.Archive(ctx, p, id, true, reason)
		}
		_, e = s.Model.Upsert(ctx, p, old)
		if e == nil && old.Archived {
			e = s.Model.Archive(ctx, p, id, true, reason)
		}
		return e
	case "scenario_archive":
		old, e := decodeBefore[struct {
			Archived bool `json:"archived"`
		}](event)
		if e != nil {
			return e
		}
		return s.Model.Archive(ctx, p, id, old.Archived, reason)
	case "income_model_set":
		old, e := decodeBefore[[]model.Income](event)
		if e != nil {
			return e
		}
		if old == nil {
			old = []model.Income{}
		}
		_, e = s.Model.SetIncome(ctx, p, old)
		return e
	case "dashboard_create":
		grants, e := s.Sharing.ListOwned(ctx, p, id)
		if e != nil {
			return e
		}
		for _, g := range grants {
			if g.RevokedAt == nil {
				if e = s.Sharing.Unshare(ctx, p, g.ID); e != nil {
					return e
				}
			}
		}
		return s.Dashboard.Archive(ctx, p, id, true, reason)
	case "dashboard_archive":
		old, e := decodeBefore[struct {
			Archived bool `json:"archived"`
		}](event)
		if e != nil {
			return e
		}
		return s.Dashboard.Archive(ctx, p, id, old.Archived, reason)
	case "dashboard_publish", "dashboard_unpublish":
		old, e := decodeBefore[dashboard.Head](event)
		if e != nil {
			return e
		}
		if e = s.Dashboard.RestorePublication(ctx, p, id, old, reason); e != nil {
			return e
		}
		return s.Sharing.RefreshDashboard(ctx, p, id)
	case "dashboard_add_component", "dashboard_update_component", "dashboard_remove_component", "dashboard_compose", "dashboard_patch":
		old, e := decodeBefore[dashboard.GetResult](event)
		if e != nil {
			return e
		}
		ops, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "", "value": old.Spec}})
		_, e = s.Dashboard.Patch(ctx, p, id, ops)
		return e
	case "dashboard_share":
		old, e := decodeBefore[sharing.Grant](event)
		if e != nil {
			return e
		}
		if old.ID == "" || old.RevokedAt != nil {
			return s.Sharing.Unshare(ctx, p, id)
		}
		return s.restoreGrant(ctx, p, old)
	case "dashboard_unshare":
		old, e := decodeBefore[sharing.Grant](event)
		if e != nil {
			return e
		}
		return s.restoreGrant(ctx, p, old)
	case "import_edit":
		old, e := decodeBefore[imports.ImportRow](event)
		if e != nil {
			return e
		}
		in := imports.EditInput{BatchID: old.BatchID, RowID: old.ID}
		if len(old.Proposed.Postings) < 2 {
			return fmt.Errorf("invalid historical import row")
		}
		in.Set.Account = &old.Proposed.Postings[1].Account
		in.Set.Tags = &old.Proposed.Tags
		in.Set.Skip = &old.Skip
		_, e = s.Imports.Edit(ctx, p, in)
		return e
	default:
		return fmt.Errorf("this event has no direct inverse; use the linked domain events in its history")
	}
}
func (s *Service) restoreGrant(ctx context.Context, p principal.Principal, g sharing.Grant) error {
	expires := ""
	if g.ExpiresAt != nil {
		expires = g.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	}
	_, e := s.Sharing.Share(ctx, p, sharing.ShareInput{DashboardID: g.DashboardID, Grantee: g.GranteeUser, Role: g.Role, Mode: g.Mode, Sources: g.Sources, RowFilter: g.RowFilter, Refresh: g.Refresh, ExpiresAt: expires})
	return e
}
func importWriter(p principal.Principal) principal.Principal {
	if p.Require("import:write") != nil {
		return p
	}
	p.Scopes = append(append([]string{}, p.Scopes...), "ledger:write")
	return p
}
