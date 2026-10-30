package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"github.com/olegpustovit/moneybag/backend/internal/components/admin"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/feedback"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/components/semantic"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"net/url"
)

func registerSemantic(r registration, s Services) {
	add(r, "metric_upsert", "dashboard", "dash:write", "Use this to define or change a named metric, the one definition of a number that every dashboard shares. Definitions are data: accounts and exclude are id prefixes, and where is an optional CEL escape hatch. Declare unit, kind, additivity and estimates. Undoable.", false, false, s.Semantic.UpsertMetric)
	add(r, "metric_list", "dashboard", "ledger:read", "Use this to see the defined metrics before writing CEL or a new metric.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Items []semantic.Metric `json:"items"`
	}, error) {
		v, e := s.Semantic.Metrics(ctx, p)
		return struct {
			Items []semantic.Metric `json:"items"`
		}{v}, e
	})
	add(r, "metric_query", "dashboard", "ledger:read", "Use this to evaluate a metric exactly: decimal totals valued at each entry's date, with compare, split, bucket, filters, an estimates override and facts. Shows what a stat, time_chart, ranked_bars or sankey panel will read. entries gives the ledger filter behind the number.", true, false, s.Semantic.Query)
	add(r, "label_set", "dashboard", "dash:write", "Use this to give a raw key a human name: an account, category, pot, tag, dimension value or series. Keys for tags include any prefix, such as trip:lisbon. Undoable.", false, false, s.Semantic.SetLabel)
	add(r, "payee_alias_upsert", "dashboard", "dash:write", "Use this to show a bank's payee text under a readable name. pattern is a case-insensitive regular expression; the bank text stays in the entry. Undoable.", false, false, s.Semantic.UpsertAlias)
	add(r, "annotation_upsert", "dashboard", "dash:write", "Use this to mark a period on charts, such as a move or a job change. Give from and to, or a tag_rule such as trip:* whose entries supply the dates. Undoable.", false, false, s.Semantic.UpsertAnnotation)
	add(r, "field_values", "dashboard", "ledger:read", "Use this to see the real values of a dimension, with counts and EUR totals, before writing a filter, split or label. Dimensions: category, type, source, pot, class, account, payee, trip, tag or an account group key.", true, false, func(ctx context.Context, p principal.Principal, in semantic.FieldValuesInput) (struct {
		Items []semantic.FieldValue `json:"items"`
	}, error) {
		v, e := s.Semantic.FieldValues(ctx, p, in)
		return struct {
			Items []semantic.FieldValue `json:"items"`
		}{v}, e
	})
	add(r, "nav_set", "dashboard", "dash:write", "Use this to replace the app's navigation. Each item opens a dashboard or an app route such as /ledger. An empty list restores the default. Undoable.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		Items []semantic.NavItem `json:"items"`
	}) (struct {
		Items []semantic.NavItem `json:"items"`
	}, error) {
		out := struct {
			Items []semantic.NavItem `json:"items"`
		}{}
		for _, item := range in.Items {
			if item.Dashboard == "" {
				continue
			}
			if _, e := s.Dashboard.Get(ctx, p, item.Dashboard, ""); e != nil {
				return out, fmt.Errorf("item %s: dashboard %s: %w", item.ID, item.Dashboard, e)
			}
		}
		var e error
		out.Items, e = s.Semantic.SetNav(ctx, p, in.Items)
		return out, e
	})
	add(r, "feedback_list", "dashboard", "ledger:read", "Use this to read numbers people flagged as wrong, with the dashboard, component, metric and value they were looking at. Newest first; open by default.", true, false, func(ctx context.Context, p principal.Principal, in feedback.ListInput) (struct {
		Items []feedback.Flag `json:"items"`
	}, error) {
		v, e := s.Feedback.List(ctx, p, in)
		return struct {
			Items []feedback.Flag `json:"items"`
		}{v}, e
	})
	add(r, "feedback_resolve", "dashboard", "dash:write", "Use this after investigating a flag: say whether it was fixed, explained, wont_fix or a duplicate, with a note the person who flagged it can read. Undoable.", false, false, s.Feedback.Resolve)
	add(r, "usage_get", "dashboard", "ledger:read", "Use this to see which pages and dashboards people open, so effort goes where it is used. Defaults to the last 30 days.", true, false, s.Feedback.Usage)

	add(r, "imports_list", "import", "ledger:read", "Use this to list statement imports with their status, newest first, before previewing or committing one.", true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Items []imports.Batch `json:"items"`
	}, error) {
		v, e := s.Imports.List(ctx, p)
		return struct {
			Items []imports.Batch `json:"items"`
		}{v}, e
	})
	type feedResult struct {
		Feed      imports.Feed `json:"feed"`
		SecretSet bool         `json:"secret_set"`
		SecretURL string       `json:"secret_url" jsonschema:"where a person enters the bank credential; it never passes through an agent"`
	}
	add(r, "feed_upsert", "import", "import:write", "Use this to set up a bank feed: public HTTPS url, account, format and date_format. It never takes a credential. Send a person to secret_url to enter one; feeds_sync works once secret_set is true.", false, false, func(ctx context.Context, p principal.Principal, in struct {
		ID         string `json:"id" jsonschema:"wise or finance_connector"`
		URL        string `json:"url"`
		Account    string `json:"account"`
		Format     string `json:"format" jsonschema:"wise_csv, generic_csv or plaid"`
		DateFormat string `json:"date_format,omitempty" jsonschema:"iso, dmy or mdy"`
	}) (feedResult, error) {
		feed, e := s.Imports.ConfigureFeed(ctx, p, imports.FeedInput{ID: in.ID, URL: in.URL, Account: in.Account, Format: in.Format, DateFormat: in.DateFormat})
		if e != nil {
			return feedResult{}, e
		}
		return feedResult{feed, s.Imports.HasFeedSecret(p, feed), s.PublicURL + "/secure/feed/" + url.PathEscape(in.ID)}, nil
	})

	const adminNote = " Needs the admin:users scope; a connection without it asks the owner through admin_request."
	add(r, "admin_request", "admin", "ledger:read", "Use this when a task needs admin:users and this connection lacks it. Returns a link for the owner; the page names this exact connection, and the scope applies as soon as they approve.", false, false, s.Admin.RequestAdmin)
	add(r, "user_list", "admin", principal.ScopeAdminUsers, "Use this to list the people on this Moneybag, with their role and whether they are disabled."+adminNote, true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Items []identity.User `json:"items"`
	}, error) {
		v, e := s.Admin.Users(ctx, p)
		return struct {
			Items []identity.User `json:"items"`
		}{v}, e
	})
	add(r, "user_invite", "admin", principal.ScopeAdminUsers, "Use this to invite a person. Returns a link they open to set their own password; passwords never pass through agents. Audited with the reason."+adminNote, false, false, s.Admin.Invite)
	add(r, "user_disable", "admin", principal.ScopeAdminUsers, "Use this to disable a person: they are signed out and cannot sign in, and their records stay. Disabling an admin returns needs_confirmation with a link for the owner. Audited with the reason; undoable."+adminNote, false, true, s.Admin.DisableUser)
	add(r, "agent_list", "admin", principal.ScopeAdminUsers, "Use this to list this user's connected agents and their scopes."+adminNote, true, false, func(ctx context.Context, p principal.Principal, _ Empty) (struct {
		Items []oauth.Grant `json:"items"`
	}, error) {
		v, e := s.Admin.Agents(ctx, p)
		return struct {
			Items []oauth.Grant `json:"items"`
		}{v}, e
	})
	add(r, "agent_revoke", "admin", principal.ScopeAdminUsers, "Use this to disconnect an agent; its tokens stop working at once. Audited with the reason. Not undoable: the agent reconnects through the consent page."+adminNote, false, true, func(ctx context.Context, p principal.Principal, in admin.RevokeInput) (struct {
		Revoked bool `json:"revoked"`
	}, error) {
		e := s.Admin.RevokeAgent(ctx, p, in)
		return struct {
			Revoked bool `json:"revoked"`
		}{e == nil}, e
	})
}

// gatedPublish refuses a version that fails lint, tests or the render check, and otherwise publishes it and reports what changed.
func gatedPublish(ctx context.Context, p principal.Principal, s Services, id string, version int, note string) (dashboard.PublishResult, error) {
	access, e := s.Sharing.Access(ctx, p, id, true)
	if e != nil {
		return dashboard.PublishResult{}, e
	}
	gate, e := access.Dashboard.PublishGate(ctx, access.Principal, access.ID, version)
	if e != nil {
		return dashboard.PublishResult{}, e
	}
	if !gate.Passed {
		return dashboard.PublishResult{}, errors.New(gate.Refusal())
	}
	previous, e := access.Dashboard.Get(ctx, access.Principal, access.ID, "published")
	hasPrevious := e == nil
	result, access, e := dashboardWrite(ctx, p, s, id, func(tx context.Context, access sharing.Access) (dashboard.PublishResult, error) {
		return access.Dashboard.Publish(tx, access.Principal, access.ID, gate.Version, note)
	})
	if access.Grant != nil {
		result.URL = "/shared/" + access.Grant.ID
		result.EventID = ""
	}
	if e != nil {
		return result, e
	}
	if refreshErr := s.Sharing.RefreshDashboard(ctx, access.Principal, access.ID); refreshErr != nil {
		s.Logger.ErrorContext(ctx, "refresh dashboard snapshots after publication", "dashboard_id", access.ID, "error", refreshErr)
		result.Warnings = append(result.Warnings, "Published successfully; some shared snapshots could not refresh. Maintenance will retry.")
	}
	if hasPrevious {
		changes, e := access.Dashboard.Diff(ctx, access.Principal, access.ID, fmt.Sprint(previous.Version), fmt.Sprint(gate.Version))
		if e != nil {
			return result, nil // The publish stands; the diff is a courtesy.
		}
		// Paths only: a whole components array can outweigh the rest of the response. dashboard_diff has the values.
		for i := range changes {
			changes[i].Before, changes[i].After = nil, nil
		}
		result.Diff = changes
	}
	return result, nil
}
