package mcpserver

import (
	"context"
	"github.com/olegpustovit/moneybag/backend/internal/components/classify"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/model"
	"github.com/olegpustovit/moneybag/backend/internal/components/reconcile"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
)

func registerWorkflows(r registration, s Services) {
	add(r, "feeds_sync", "import", "import:write", "Use this when refreshing a feed configured for your own user in Settings. Downloads its statement over public HTTPS using encrypted saved credentials, filters optional since (YYYY-MM-DD/RFC3339), and stages an idempotent import for preview.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		Feed  string `json:"feed"`
		Since string `json:"since,omitempty"`
	}) (imports.StartResult, error) {
		return s.Imports.Sync(ctx, p, in.Feed, in.Since)
	})
	add(r, "categories_list", "classify", "ledger:read", "Use this when choosing a spending category. Categories are your expense accounts; page through accounts_list for catalogs above 500 accounts.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Items []ledger.Account `json:"items"`
	}, error) {
		v, e := s.Ledger.Accounts(ctx, p, ledger.AccountsInput{Type: "expense", Limit: 500})
		return struct {
			Items []ledger.Account `json:"items"`
		}{v.Items}, e
	})
	add(r, "reclassify", "classify", "ledger:write", "Use this when moving spend to another expense category. Appends balanced reclassification entries with the original date and a reason; defaults to draft. Original entries remain unchanged.", false, false, s.Classify.Reclassify)
	add(r, "tags_set", "classify", "ledger:write", "Use this when adding or removing tags (namespaced like trip:x is fine) on 1..100 entry_ids, or on up to 10000 entries selected by where (CEL over the entry row); dry_run with where returns the match count only. A separate metadata overlay preserves the immutable original journal document.", false, false, s.Ledger.SetTags)
	add(r, "rules_list", "classify", "ledger:read", "Use this when inspecting auto-classification rules in priority order. The first enabled matching rule wins.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Items []classify.Rule `json:"items"`
	}, error) {
		v, e := s.Classify.Rules(ctx, p)
		return struct {
			Items []classify.Rule `json:"items"`
		}{v}, e
	})
	add(r, "rule_upsert", "classify", "ledger:write", "Use this when a payee should be classified consistently. match is boolean CEL over row; priority sorts ascending. Omit id to create. enabled=false disables a rule.", false, false, s.Classify.Upsert)
	add(r, "rule_preview", "classify", "ledger:read", "Use this before rule_apply. Supply an inline rule or owned rule_id and optional dates. Evaluates current effective categories without changing the journal.", true, false, s.Classify.Preview)
	add(r, "rule_apply", "classify", "ledger:write", "Use this when applying an owned rule retroactively. Writes source-linked reclassification drafts unless commit=true; tags are stored separately from journal history.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		RuleID string `json:"rule_id"`
		From   string `json:"from,omitempty"`
		To     string `json:"to,omitempty"`
		Commit bool   `json:"commit,omitempty"`
	}) (classify.ReclassResult, error) {
		return s.Classify.Apply(ctx, p, classify.PreviewInput{RuleID: in.RuleID, From: in.From, To: in.To}, in.Commit)
	})
	add(r, "import_start", "import", "import:write", "Use this when you receive a bank statement. Supply owned account, privatbank_xlsx/wise_csv/generic_csv/plaid, and exactly one public HTTPS file_url or content_b64. generic_csv mapping names columns; date_format is iso, dmy or mdy. Statements are staged with provenance and dedupe keys; no money is posted.", false, false, s.Imports.Start)
	add(r, "import_preview", "import", "ledger:read", "Use this to inspect staged proposals and duplicate candidates. Pages contain at most 100 rows; pass next_cursor until omitted.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		BatchID string `json:"batch_id"`
		Cursor  int    `json:"cursor,omitempty"`
	}) (imports.PreviewResult, error) {
		return s.Imports.Preview(ctx, p, in.BatchID, in.Cursor)
	})
	add(r, "import_edit", "import", "import:write", "Use this when correcting a staged row's counterpart account or tags, or skip flag. Only unposted rows belonging to the selected batch can change.", false, false, s.Imports.Edit)
	add(r, "import_commit", "import", "import:write", "Use this when a statement preview is correct. Posts rows idempotently using stable entry IDs; retries resume interrupted batches. Optional closing balance assertion requires a balance in the statement.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		BatchID              string `json:"batch_id"`
		AssertClosingBalance bool   `json:"assert_closing_balance,omitempty"`
	}) (imports.CommitResult, error) {
		return s.Imports.Commit(ctx, p, in.BatchID, in.AssertClosingBalance)
	})
	add(r, "import_undo", "import", "import:write", "Use this when a committed import was wrong. A reason is required; appends reversals for its own posted rows, retaining raw source data and history.", false, true, func(ctx context.Context, p principal.Principal, in struct {
		BatchID string `json:"batch_id"`
		Reason  string `json:"reason"`
	}) (struct {
		Reversals []ledger.Entry `json:"reversals"`
	}, error) {
		v, e := s.Imports.Undo(ctx, p, in.BatchID, in.Reason)
		return struct {
			Reversals []ledger.Entry `json:"reversals"`
		}{v}, e
	})
	add(r, "balance_assert", "reconcile", "ledger:write", "Use this when you know a real account balance from a statement or cash count. Compares exact commodity amounts on YYYY-MM-DD and opens a discrepancy when they differ.", false, false, s.Reconcile.Assert)
	add(r, "reconcile_status", "reconcile", "ledger:read", "Use this when checking whether the journal matches known balances. Returns last successful assertion and open discrepancies, optionally for one account. Read-only: it reports the last verification, which runs nightly; after backdated entries, reversals or import undos call reconcile_verify first.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		Account string `json:"account,omitempty"`
	}) (struct {
		Accounts []reconcile.AccountStatus `json:"accounts"`
	}, error) {
		v, e := s.Reconcile.Status(ctx, p, in.Account)
		return struct {
			Accounts []reconcile.AccountStatus `json:"accounts"`
		}{v}, e
	})
	add(r, "reconcile_verify", "reconcile", "ledger:write", "Use this after backdated entries, reversals or import undos. Rechecks known balances against the journal now, for one account or all, updates discrepancies and returns the same status as reconcile_status.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		Account string `json:"account,omitempty"`
	}) (struct {
		Accounts []reconcile.AccountStatus `json:"accounts"`
	}, error) {
		v, e := s.Reconcile.Recheck(ctx, p, in.Account)
		return struct {
			Accounts []reconcile.AccountStatus `json:"accounts"`
		}{v}, e
	})
	add(r, "reconcile_suggest", "reconcile", "ledger:read", "Use this when investigating a discrepancy. Returns actual nearby entries whose posting magnitude matches it, labelled as timing or possible duplicate evidence; no inferred transactions are created.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		DiscrepancyID string `json:"discrepancy_id"`
	}) (struct {
		Candidates []reconcile.Candidate `json:"candidates"`
	}, error) {
		v, e := s.Reconcile.Suggest(ctx, p, in.DiscrepancyID)
		return struct {
			Candidates []reconcile.Candidate `json:"candidates"`
		}{v}, e
	})
	add(r, "reconcile_resolve", "reconcile", "ledger:write", "Use this when settling a discrepancy with evidence and a reason. match requires a matching computed balance; adjust posts a balanced equity adjustment; ignore records the exception.", false, false, s.Reconcile.Resolve)
	add(r, "duplicates_find", "reconcile", "ledger:read", "Use this when suspecting double-counted entries. Groups actual posted entries by date, payee and canonical postings. Narrow dates for ledgers over 5000 entries; candidates require review.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		From    string `json:"from,omitempty"`
		To      string `json:"to,omitempty"`
		Account string `json:"account,omitempty"`
	}) (struct {
		Groups [][]ledger.Entry `json:"groups"`
	}, error) {
		v, e := s.Reconcile.Duplicates(ctx, p, ledger.EntriesInput{From: in.From, To: in.To, Account: in.Account})
		return struct {
			Groups [][]ledger.Entry `json:"groups"`
		}{v}, e
	})
	add(r, "scenario_compare", "model", "ledger:read", "Use this when comparing scenarios over the next 12 months. Valid metrics: spendUsd, endNetUsd, runway, cash, gold, wise, kyiv (cash, gold, wise, kyiv are month-12 values), savings_usd_mo, kyiv_empty_month, runway_ex_gold. Default spendUsd, endNetUsd, runway. Starting state comes from the live ledger as of as_of (see forecast_run).", true, false, s.Model.Compare)
	add(r, "whatif_asset_shock", "model", "ledger:read", "Use this when stress-testing current holdings. Returns USD net-worth change by pot for commodity percentage shocks. Optional scenario_id also reports its 12-month net after the immediate valuation shock, with future cash flows unchanged.", true, false, func(ctx context.Context, p principal.Principal, in model.ShockInput) (model.ShockResult, error) {
		return s.Model.Shock(ctx, p, in, s.Ledger, s.Rates)
	})
	add(r, "dashboard_share", "dashboard", "dash:share", "Use this when granting an existing user viewer or editor access to an owned published dashboard. Snapshot is default; live reads enforce declared sources and boolean row_filter. Only owners can reshare; grants can expire.", false, false, s.Sharing.Share)
	add(r, "dashboard_unshare", "dashboard", "dash:share", "Use this when another user's access should end. Only the owner can revoke; the grantee's snapshot is deleted and future reads are denied.", false, true, func(ctx context.Context, p principal.Principal, in struct {
		ShareID string `json:"share_id"`
	}) (struct {
		Revoked bool `json:"revoked"`
	}, error) {
		e := s.Sharing.Unshare(ctx, p, in.ShareID)
		return struct {
			Revoked bool `json:"revoked"`
		}{e == nil}, e
	})
	add(r, "dashboard_shares_list", "dashboard", "ledger:read", "Use this when checking who can see dashboards you own. Optional dashboard_id narrows the result.", true, false, func(ctx context.Context, p principal.Principal, in struct {
		DashboardID string `json:"dashboard_id,omitempty"`
	}) (struct {
		Items []sharing.Grant `json:"items"`
	}, error) {
		v, e := s.Sharing.ListOwned(ctx, p, in.DashboardID)
		return struct {
			Items []sharing.Grant `json:"items"`
		}{v}, e
	})
	add(r, "shared_with_me_list", "dashboard", "ledger:read", "Use this when finding active dashboard grants from other users. Open /shared/{id} for rendered output; editor tools use the share id as dashboard_id.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Items []sharing.Grant `json:"items"`
	}, error) {
		v, e := s.Sharing.ListReceived(ctx, p)
		return struct {
			Items []sharing.Grant `json:"items"`
		}{v}, e
	})
}
