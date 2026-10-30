package audit

import (
	"encoding/json"
	"regexp"
	"strings"
)

// summaryRule words one tool: with names the record from a payload field, without is the fallback that never leaks an id.
type summaryRule struct {
	with, without string
	fields        []string
}

var summaryRules = map[string]summaryRule{
	"account_create":             {"Added the %s account", "Added an account", []string{"display_name", "name"}},
	"account_update":             {"Updated the %s account", "Updated an account", []string{"display_name", "name"}},
	"assertion_archive":          {"", "Removed a balance check", nil},
	"balance_assert":             {"", "Recorded a balance check", nil},
	"budget_archive":             {"", "Removed a budget line", nil},
	"budget_set_line":            {"Changed the %s budget line", "Changed a budget line", []string{"name"}},
	"dashboard_add_component":    {"Added a panel to the %s dashboard", "Added a panel to a dashboard", []string{"spec.title"}},
	"dashboard_archive":          {"", "Deleted a dashboard", nil},
	"dashboard_compose":          {"Rearranged the %s dashboard", "Rearranged a dashboard", []string{"spec.title"}},
	"dashboard_create":           {"Created the %s dashboard", "Created a dashboard", []string{"spec.title"}},
	"dashboard_patch":            {"Edited the %s dashboard", "Edited a dashboard", []string{"spec.title"}},
	"dashboard_publish":          {"Published the %s dashboard", "Published a dashboard", []string{"title"}},
	"dashboard_remove_component": {"Removed a panel from the %s dashboard", "Removed a panel from a dashboard", []string{"spec.title"}},
	"dashboard_share":            {"", "Shared a dashboard", nil},
	"dashboard_shared_read":      {"", "Opened a shared dashboard", nil},
	"dashboard_unpublish":        {"Took the %s dashboard offline", "Took a dashboard offline", []string{"title"}},
	"dashboard_unshare":          {"", "Stopped sharing a dashboard", nil},
	"dashboard_update_component": {"Edited a panel on the %s dashboard", "Edited a panel on a dashboard", []string{"spec.title"}},
	"db_alter":                   {"", "Changed a dataset's columns", nil},
	"db_archive":                 {"", "Removed a dataset", nil},
	"db_create":                  {"Created the %s dataset", "Created a dataset", []string{"name"}},
	"db_update_cells":            {"", "Edited dataset rows", nil},
	"entry_commit":               {"Posted an entry for %s", "Posted an entry", []string{"payee"}},
	"entry_draft_status":         {"", "Changed a draft entry", nil},
	"entry_post":                 {"Added an entry for %s", "Added an entry", []string{"payee"}},
	"entry_reverse":              {"Reversed an entry for %s", "Reversed an entry", []string{"payee"}},
	"entry_update":               {"Edited a draft entry for %s", "Edited a draft entry", []string{"payee"}},
	"feed_configure":             {"", "Configured a bank feed", nil},
	"import_commit":              {"", "Imported a statement", nil},
	"import_edit":                {"", "Corrected a staged statement row", nil},
	"import_redo":                {"", "Reapplied a statement", nil},
	"import_start":               {"", "Staged a statement", nil},
	"import_status":              {"", "Changed a staged statement", nil},
	"import_undo":                {"", "Undid a statement import", nil},
	"income_model_set":           {"", "Updated the income model", nil},
	"rate_archive":               {"", "Ignored a rate quote", nil},
	"rate_set":                   {"Updated the %s rate", "Updated a rate", []string{"pair"}},
	"reclassify":                 {"Recategorized an entry for %s", "Recategorized an entry", []string{"payee"}},
	"reconcile_resolve":          {"", "Settled a balance discrepancy", nil},
	"rule_upsert":                {"", "Saved a categorization rule", nil},
	"scenario_archive":           {"", "Removed a scenario", nil},
	"scenario_upsert":            {"Saved the %s scenario", "Saved a scenario", []string{"name"}},
	"tags_set":                   {"", "Changed tags on entries", nil},
	"transfer_record":            {"", "Recorded a transfer", nil},
	"undo":                       {"", "Undid an earlier change", nil},
	"view_upsert":                {"", "Saved a data view", nil},
}

var machineText = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$|^(assets|liabilities|income|expenses|equity):|^https?://|^[a-z0-9]+(_[a-z0-9]+)+$`)

// Summarize words an event from its tool and payload. New tools pass their own summary; this covers the rest and old events.
func Summarize(e Event) string {
	rule, ok := summaryRules[e.Tool]
	if !ok {
		return "Changed " + strings.ReplaceAll(e.Tool, "_", " ")
	}
	if rule.with != "" {
		for _, payload := range []json.RawMessage{e.After, e.Before} {
			var doc map[string]any
			if json.Unmarshal(payload, &doc) != nil {
				continue
			}
			for _, path := range rule.fields {
				if name := pick(doc, path); name != "" && len(name) <= 80 && !machineText.MatchString(name) {
					return strings.Replace(rule.with, "%s", name, 1)
				}
			}
		}
	}
	return rule.without
}

func pick(doc map[string]any, path string) string {
	var value any = doc
	for _, part := range strings.Split(path, ".") {
		m, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = m[part]
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
