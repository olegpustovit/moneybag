package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/olegpustovit/moneybag/backend/internal/components/audit"
	"github.com/olegpustovit/moneybag/backend/internal/components/dashboard"
	"github.com/olegpustovit/moneybag/backend/internal/components/feedback"
	"github.com/olegpustovit/moneybag/backend/internal/components/identity"
	"github.com/olegpustovit/moneybag/backend/internal/components/imports"
	"github.com/olegpustovit/moneybag/backend/internal/components/ledger"
	"github.com/olegpustovit/moneybag/backend/internal/components/oauth"
	"github.com/olegpustovit/moneybag/backend/internal/components/rates"
	"github.com/olegpustovit/moneybag/backend/internal/components/sharing"
	"github.com/olegpustovit/moneybag/backend/internal/infrastructure/principal"
	"github.com/olegpustovit/moneybag/backend/internal/mcpserver"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// evalLedger is a synthetic ledger whose Q2 2026 spending is €280.00: €100 of food, €50 and $110 at 1.10 of travel, and €30 estimated.
func evalLedger(t *testing.T, ctx context.Context, owner principal.Principal, s mcpserver.Services) {
	t.Helper()
	for _, a := range []ledger.Account{
		{ID: "assets:bank", Type: "asset", Name: "Everyday bank", Commodities: []string{"EUR", "USD"}},
		{ID: "expenses:food", Type: "expense", Name: "Food", Commodities: []string{"EUR"}, Groups: map[string]string{"category": "groceries_home"}},
		{ID: "expenses:travel", Type: "expense", Name: "Travel", Commodities: []string{"EUR", "USD"}, Groups: map[string]string{"category": "Travel"}},
		{ID: "expenses:estimated:cash", Type: "expense", Name: "Estimated cash uses", Commodities: []string{"EUR"}, Groups: map[string]string{"category": "Cash"}},
		{ID: "income:salary", Type: "income", Name: "Salary", Commodities: []string{"EUR"}},
	} {
		if _, e := s.Ledger.CreateAccount(ctx, owner, a); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.Rates.Set(ctx, owner, rates.SetInput{Pair: "EUR/USD", Value: ledger.MustDecimal("1.10"), At: "2026-05-01T16:00:00Z", Source: "Synthetic"}); e != nil {
		t.Fatal(e)
	}
	spend := func(date, payee, account, amount, commodity string, tags ...string) {
		t.Helper()
		v := ledger.MustDecimal(amount)
		if _, e := s.Ledger.Post(ctx, owner, ledger.EntryInput{Date: date, Payee: payee, Commit: true, Tags: tags, Postings: []ledger.Posting{{Account: account, Amount: ledger.Amount{Value: v, Commodity: commodity}}, {Account: "assets:bank", Amount: ledger.Amount{Value: v.Negate(), Commodity: commodity}}}}); e != nil {
			t.Fatal(e)
		}
	}
	spend("2026-02-03", "LIDL 123 ZAGREB", "expenses:food", "200.00", "EUR")
	spend("2026-04-05", "LIDL 456 ZAGREB", "expenses:food", "100.00", "EUR")
	spend("2026-05-07", "Air Synthetic", "expenses:travel", "50.00", "EUR", "trip:lisbon")
	spend("2026-05-10", "Hotel Synthetic", "expenses:travel", "110.00", "USD", "trip:lisbon")
	spend("2026-06-01", "Cash withdrawals", "expenses:estimated:cash", "30.00", "EUR")
	v := ledger.MustDecimal("1000.00")
	if _, e := s.Ledger.Post(ctx, owner, ledger.EntryInput{Date: "2026-04-01", Payee: "Employer", Commit: true, Postings: []ledger.Posting{{Account: "assets:bank", Amount: ledger.Amount{Value: v, Commodity: "EUR"}}, {Account: "income:salary", Amount: ledger.Amount{Value: v.Negate(), Commodity: "EUR"}}}}); e != nil {
		t.Fatal(e)
	}
}

// testTelemetryEval is the deterministic eval: a fresh agent holding only the MCP builds a metric and a scoped dashboard, gets it through lint and tests, publishes, and answers a person's flag.
func testTelemetryEval(t *testing.T, ctx context.Context, admin principal.Principal, s mcpserver.Services, db *mongo.Database) {
	t.Helper()
	token, e := s.Identity.Invite(ctx, admin, "eval-owner@example.test")
	if e != nil {
		t.Fatal(e)
	}
	user, e := s.Identity.Register(ctx, token, "Eval owner", "synthetic-test-password")
	if e != nil {
		t.Fatal(e)
	}
	owner := identity.Principal(user)
	if e = ledger.NewMongoRepository(db).Init(ctx, owner); e != nil {
		t.Fatal(e)
	}
	evalLedger(t, ctx, owner, s)
	agent := principal.Principal{UserID: user.ID, Scopes: []string{"ledger:read", "dash:write"}, Actor: principal.Actor{Kind: "agent", ID: "https://eval.example.test/client.json"}}
	cs := mcpSession(t, ctx, agent, s, false)
	const maxResponse = 100_000 // about 25k tokens
	call := func(name string, args map[string]any) (map[string]any, string, bool) {
		t.Helper()
		result, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(name, e)
		}
		raw, _ := json.Marshal(result)
		if len(raw) > maxResponse {
			t.Errorf("%s response is %d bytes, over the token budget", name, len(raw))
		}
		var out map[string]any
		if b, _ := json.Marshal(result.StructuredContent); len(b) > 0 {
			_ = json.Unmarshal(b, &out)
		}
		text := ""
		for _, c := range result.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		return out, text, result.IsError
	}
	must := func(name string, args map[string]any) map[string]any {
		t.Helper()
		out, text, failed := call(name, args)
		if failed {
			t.Fatal(name, text)
		}
		return out
	}

	t.Run("sees only what it can use", func(t *testing.T) {
		tools, e := cs.ListTools(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		size := 0
		listed := []string{}
		for _, tool := range tools.Tools {
			raw, _ := json.Marshal(tool)
			size += len(raw)
			listed = append(listed, tool.Name)
		}
		if len(tools.Tools) >= 40 || size >= 60*1024 {
			t.Fatal("authoring budget", len(tools.Tools), size)
		}
		if slices.Contains(listed, "user_list") || slices.Contains(listed, "dashboard_patch") || slices.Contains(listed, "entry_post") {
			t.Fatal("list too wide", listed)
		}
	})

	t.Run("learns what exists", func(t *testing.T) {
		catalog := must("catalog_get", map[string]any{})
		text, _ := json.Marshal(catalog)
		for _, want := range []string{`"time_chart"`, `"no_raw_ids"`, `"since march"`, `"compare_from"`, `"period_change"`} {
			if !strings.Contains(string(text), want) {
				t.Fatal("catalog misses", want)
			}
		}
		values := must("field_values", map[string]any{"dimension": "category"})
		raw, _ := json.Marshal(values)
		if !strings.Contains(string(raw), `"value":"groceries_home"`) || !strings.Contains(string(raw), `"label":"Groceries home"`) {
			t.Fatal("field values", string(raw))
		}
	})

	t.Run("names raw keys", func(t *testing.T) {
		must("label_set", map[string]any{"kind": "category", "key": "groceries_home", "label": "Groceries"})
		must("payee_alias_upsert", map[string]any{"pattern": "^LIDL", "name": "Lidl"})
		must("annotation_upsert", map[string]any{"id": "move", "label": "Moved flat", "from": "2026-05-01", "to": "2026-05-03"})
		_, text, failed := call("payee_alias_upsert", map[string]any{"pattern": "(", "name": "Broken"})
		if !failed || !strings.Contains(text, "pattern") {
			t.Fatal("bad pattern accepted", text)
		}
	})

	t.Run("defines one metric with an exact total", func(t *testing.T) {
		_, text, failed := call("metric_upsert", map[string]any{"id": "spending", "label": "Spending", "unit": "euros", "kind": "flow", "additivity": "sum", "estimates": "include", "accounts": []string{"expenses:"}})
		if !failed || !strings.Contains(text, "eur, usd") {
			t.Fatal("bad unit should name the choices", text)
		}
		must("metric_upsert", map[string]any{"id": "spending", "label": "Spending", "unit": "eur", "kind": "flow", "additivity": "sum", "estimates": "split", "accounts": []string{"expenses:"}, "dimensions": []string{"category", "payee", "trip"}})
		q := must("metric_query", map[string]any{"metric": "spending", "from": "2026-04-01", "to": "2026-06-30", "compare": "previous_period", "split": "category", "bucket": "month", "facts": []string{"period_change", "top_contributors", "top_detractors", "estimate_share", "largest_entry"}})
		if q["total"] != "280" || q["text"] != "€280.00" || q["unit"] != "eur" {
			t.Fatal("total must be exact, with USD at the entry date's rate", q)
		}
		cmp := q["compare"].(map[string]any)
		if cmp["total"] != "200" || cmp["direction"] != "up" || cmp["good"] != false || cmp["delta_pct"] != 40.0 {
			t.Fatal("compare", cmp)
		}
		series, _ := json.Marshal(q["series"])
		if !strings.Contains(string(series), `"name":"Groceries"`) || !strings.Contains(string(series), `"bucket":"2026-05"`) || !strings.Contains(string(series), `"estimated":true`) {
			t.Fatal("series", string(series))
		}
		if est := q["estimates"].(map[string]any); est["policy"] != "split" || est["total"] != "30" {
			t.Fatal("estimates", est)
		}
		facts := q["facts"].([]any)
		sentences := []string{}
		for _, fact := range facts {
			sentences = append(sentences, fact.(map[string]any)["sentence"].(string))
		}
		joined := strings.Join(sentences, " ")
		for _, want := range []string{"Spending rose 40% to €280.00, compared with the previous period.", "Travel rose the most, up €150.00", "Groceries fell the most, down €100.00", "Estimates make up 11% of spending.", "The largest entry was €100.00 at Lidl on 5 April 2026."} {
			if !strings.Contains(joined, want) {
				t.Errorf("facts lack %q: %s", want, joined)
			}
		}
		if regexp.MustCompile(`expenses:|groceries_home|[0-9A-HJKMNP-TV-Z]{26}`).MatchString(joined) {
			t.Fatal("facts leak identifiers", joined)
		}
		excluded := must("metric_query", map[string]any{"metric": "spending", "from": "2026-04-01", "to": "2026-06-30", "estimates": "exclude"})
		if excluded["total"] != "250" {
			t.Fatal("estimates exclude", excluded)
		}
		trip := must("metric_query", map[string]any{"metric": "spending", "from": "2026-04-01", "to": "2026-06-30", "filters": []map[string]any{{"field": "trip", "op": "in", "values": []string{"lisbon"}}}})
		if trip["total"] != "150" {
			t.Fatal("trip filter", trip)
		}
		_, text, failed = call("metric_query", map[string]any{"metric": "spending", "split": "account"})
		if !failed || !strings.Contains(text, "category, payee, trip") {
			t.Fatal("split outside the metric's dimensions should name them", text)
		}
		must("metric_upsert", map[string]any{"id": "income", "label": "Income", "unit": "eur", "kind": "flow", "additivity": "sum", "estimates": "include", "accounts": []string{"income:"}, "sign": -1})
		if income := must("metric_query", map[string]any{"metric": "income", "from": "2026-04-01", "to": "2026-06-30", "compare": "previous_period"}); income["total"] != "1000" || income["compare"].(map[string]any)["good"] != true {
			t.Fatal("income", income)
		}
		must("metric_upsert", map[string]any{"id": "bank", "label": "Bank balance", "unit": "eur", "kind": "balance", "additivity": "last", "estimates": "include", "accounts": []string{"assets:bank"}})
		if bank := must("metric_query", map[string]any{"metric": "bank", "to": "2026-06-30"}); bank["total"] != "520" {
			t.Fatal("balance values holdings at the period end", bank)
		}
	})

	spec := map[string]any{
		"id": "eval-spending", "title": "Spending", "version": 1, "question": "Am I spending more than I can sustain?",
		"scope":   map[string]any{"time": map[string]any{"default": "2026-Q2", "presets": []string{"30d", "12mo", "ytd", "all"}}, "compare": "previous_period"},
		"explore": map[string]any{"dimensions": []string{"category", "payee", "trip"}},
		"answer":  map[string]any{"metric": "spending", "facts": []string{"period_change", "top_contributors"}},
		"data":    map[string]any{},
		"pages":   []map[string]any{{"id": "main", "title": "Main", "layout": []map[string]any{{"cols": []map[string]any{{"span": 4, "component": "spend"}, {"span": 8, "component": "trend"}}}, {"cols": []map[string]any{{"span": 6, "component": "where"}, {"span": 6, "component": "behind"}}}, {"cols": []map[string]any{{"span": 12, "component": "flow"}}}}}},
		"components": []map[string]any{
			{"id": "spend", "type": "stat", "metric": "spending", "title": "Spending"},
			{"id": "trend", "type": "time_chart", "metric": "spending", "title": "Since {{ params.from }}", "split": "category", "x": map[string]any{"type": "time"}, "mark": "stack", "annotate": "trips"},
			{"id": "where", "type": "ranked_bars", "metric": "spending", "title": "Where it went", "split": "category"},
			{"id": "behind", "type": "entries", "metric": "spending", "title": "The entries behind these numbers", "drill": map[string]any{"to": "entries", "filter": "same_as_page"}},
			{"id": "flow", "type": "sankey", "metric": "spending", "title": "Money out", "split": "category"},
		},
		"tests": []map[string]any{{"ask": "Spending in 2026-Q2", "metric": "spending", "from": "2026-04-01", "to": "2026-06-30", "equals_ledger": map[string]any{"accounts": []string{"expenses:"}}}},
	}
	replace := func(value any) []map[string]any {
		return []map[string]any{{"op": "replace", "path": "", "value": value}}
	}

	t.Run("builds a scoped dashboard and sees its numbers", func(t *testing.T) {
		must("dashboard_create", map[string]any{"id": "eval-spending", "title": "Spending"})
		bad := map[string]any{"id": "eval-spending", "title": "Spending", "version": 1, "data": map[string]any{}, "pages": []map[string]any{{"id": "main", "title": "Main", "layout": []map[string]any{}}}, "components": []map[string]any{{"id": "x", "type": "time_chart", "metric": "nope", "mark": "pie"}}}
		dry := must("dashboard_apply", map[string]any{"dashboard_id": "eval-spending", "ops": replace(bad), "validate_only": true})
		raw, _ := json.Marshal(dry["validation"])
		if !strings.Contains(string(raw), "line, area, stack, bar") {
			t.Fatal("a bad mark should name the choices", string(raw))
		}
		bad["components"] = []map[string]any{{"id": "x", "type": "time_chart", "metric": "nope"}}
		dry = must("dashboard_apply", map[string]any{"dashboard_id": "eval-spending", "ops": replace(bad), "validate_only": true})
		raw, _ = json.Marshal(dry["validation"])
		if !strings.Contains(string(raw), "metric_list") {
			t.Fatal("an unknown metric should point at metric_list", string(raw))
		}
		applied := must("dashboard_apply", map[string]any{"dashboard_id": "eval-spending", "ops": replace(spec)})
		if applied["validation"].(map[string]any)["ok"] != true {
			t.Fatal("spec rejected", applied)
		}
		preview := must("dashboard_preview", map[string]any{"dashboard_id": "eval-spending", "values_only": true})
		values := preview["values"].(map[string]any)
		stat := values["spend"].(map[string]any)
		if stat["total"] != "280" || stat["compare"].(map[string]any)["total"] != "200" || stat["from"] != "2026-04-01" || len(stat["sparkline"].([]any)) == 0 {
			t.Fatal("stat must read the page clock", stat)
		}
		answer := values["answer"].(map[string]any)
		if answer["sentence"] != "Spending rose 40% to €280.00, compared with the previous period." || len(answer["facts"].([]any)) != 2 {
			t.Fatal("answer", answer)
		}
		chart := values["trend"].(map[string]any)
		if chart["x"].(map[string]any)["type"] != "time" || len(chart["series"].([]any)) == 0 || len(chart["annotations"].([]any)) != 2 {
			t.Fatal("time chart", chart)
		}
		if title := chart["title"]; title != "Since 2026-04-01" {
			t.Fatal("template must evaluate", title)
		}
		if behind := values["behind"].(map[string]any); behind["total"] != 4.0 {
			t.Fatal("entries panel", behind)
		}
		if flow := values["flow"].(map[string]any); len(flow["links"].([]any)) != 3 {
			t.Fatal("sankey", flow)
		}
	})

	t.Run("cannot publish past the gate and can fix what it says", func(t *testing.T) {
		broken := map[string]any{}
		raw, _ := json.Marshal(spec)
		_ = json.Unmarshal(raw, &broken)
		broken["tests"] = []map[string]any{{"ask": "Food only", "metric": "spending", "from": "2026-04-01", "to": "2026-06-30", "equals_ledger": map[string]any{"accounts": []string{"expenses:food"}}}}
		components := broken["components"].([]any)
		components[0].(map[string]any)["title"] = "Spending (card_core_median)"
		must("dashboard_apply", map[string]any{"dashboard_id": "eval-spending", "ops": replace(broken)})
		lint := must("dashboard_lint", map[string]any{"id": "eval-spending"})
		if lint["errors"].(float64) != 1 {
			t.Fatal("lint", lint)
		}
		issue := lint["issues"].([]any)[0].(map[string]any)
		if issue["rule"] != "no_raw_ids" || issue["severity"] != "error" || issue["fix"] == "" || issue["path"] != "/components/0/title" {
			t.Fatal("lint issue", issue)
		}
		report := must("dashboard_test", map[string]any{"id": "eval-spending"})
		if report["failed"].(float64) != 1 || !strings.Contains(report["results"].([]any)[0].(map[string]any)["msg"].(string), "€280.00") {
			t.Fatal("test report", report)
		}
		_, text, failed := call("dashboard_publish", map[string]any{"dashboard_id": "eval-spending"})
		if !failed || !strings.Contains(text, "no_raw_ids") || !strings.Contains(text, "Food only") || !strings.Contains(text, "unchanged") {
			t.Fatal("gate should refuse and say why", text)
		}
		head := must("dashboard_list", map[string]any{})
		if raw, _ := json.Marshal(head); strings.Contains(string(raw), `"published_version":1`) {
			t.Fatal("refused publish changed the head", string(raw))
		}
		must("dashboard_apply", map[string]any{"dashboard_id": "eval-spending", "ops": replace(spec)})
		if clean := must("dashboard_lint", map[string]any{"id": "eval-spending"}); clean["errors"].(float64) != 0 {
			t.Fatal("still dirty", clean)
		}
		if passed := must("dashboard_test", map[string]any{"id": "eval-spending"}); passed["passed"].(float64) != 1 || passed["failed"].(float64) != 0 {
			t.Fatal("tests", passed)
		}
		published := must("dashboard_publish", map[string]any{"dashboard_id": "eval-spending", "note": "First version"})
		if published["url"] != "/dashboards/eval-spending" || published["published_version"].(float64) < 1 {
			t.Fatal("publish result", published)
		}
		must("nav_set", map[string]any{"items": []map[string]any{{"id": "spending", "label": "Spending", "dashboard": "eval-spending"}, {"id": "ledger", "label": "Ledger", "route": "/ledger"}}})
		_, text, failed = call("nav_set", map[string]any{"items": []map[string]any{{"id": "x", "label": "X", "dashboard": "missing"}}})
		if !failed || !strings.Contains(text, "missing") {
			t.Fatal("nav to a missing dashboard", text)
		}
	})

	t.Run("hears from a person and answers", func(t *testing.T) {
		flag, e := s.Feedback.Flag(ctx, owner, feedback.FlagInput{Target: feedback.Target{Dashboard: "eval-spending", Component: "spend", Metric: "spending", Value: "€280.00", Params: map[string]any{"from": "2026-04-01"}}, Note: "This looks high"})
		if e != nil {
			t.Fatal(e)
		}
		listed := must("feedback_list", map[string]any{})
		items := listed["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["note"] != "This looks high" || items[0].(map[string]any)["target"].(map[string]any)["metric"] != "spending" {
			t.Fatal("feedback list", listed)
		}
		_, text, failed := call("feedback_resolve", map[string]any{"id": flag.ID, "resolution": "maybe"})
		if !failed || !strings.Contains(text, "explained") {
			t.Fatal("bad resolution", text)
		}
		must("feedback_resolve", map[string]any{"id": flag.ID, "resolution": "explained", "note": "A trip to Lisbon"})
		if open := must("feedback_list", map[string]any{}); len(open["items"].([]any)) != 0 {
			t.Fatal("flag still open", open)
		}
		if _, e := s.Feedback.List(ctx, principal.Principal{UserID: admin.UserID, Scopes: []string{"ledger:read"}}, feedback.ListInput{}); e != nil {
			t.Fatal(e)
		}
		other, e := s.Feedback.Resolve(ctx, admin, feedback.ResolveInput{ID: flag.ID, Resolution: "fixed"})
		if e == nil {
			t.Fatal("another user resolved this flag", other)
		}
		if e = s.Feedback.Record(ctx, owner, feedback.UsageBeacon{Path: "/dashboards/eval-spending?from=2026-01-01", Dashboard: "eval-spending"}); e != nil {
			t.Fatal(e)
		}
		usage := must("usage_get", map[string]any{})
		if pages := usage["pages"].([]any); len(pages) != 1 || pages[0].(map[string]any)["path"] != "/dashboards/eval-spending" || pages[0].(map[string]any)["views"].(float64) != 1 {
			t.Fatal("usage", usage)
		}
	})

	t.Run("leaves a readable trail that still verifies", func(t *testing.T) {
		if e := s.Audit.Verify(ctx, owner); e != nil {
			t.Fatal(e)
		}
		page, e := s.Audit.Changes(ctx, owner, audit.ChangesInput{Routine: true, Limit: 200}, func(principal.Actor) string { return "Someone" })
		if e != nil {
			t.Fatal(e)
		}
		seen := map[string]bool{}
		for _, group := range page.Groups {
			if group.Summary == "" || regexp.MustCompile(`[0-9A-HJKMNP-TV-Z]{26}|expenses:|groceries_home`).MatchString(group.Summary) {
				t.Errorf("unreadable summary %q", group.Summary)
			}
			for _, event := range group.Events {
				seen[event.Tool] = true
			}
		}
		for _, tool := range []string{"metric_upsert", "label_set", "payee_alias_upsert", "annotation_upsert", "nav_set", "feedback_flag", "feedback_resolve", "dashboard_publish"} {
			if !seen[tool] {
				t.Errorf("no %s in the feed", tool)
			}
		}
		found, e := s.Audit.Changes(ctx, owner, audit.ChangesInput{Q: "spending metric"}, func(principal.Actor) string { return "Someone" })
		if e != nil || len(found.Groups) == 0 || !strings.Contains(found.Groups[0].Summary, "Spending metric") {
			t.Fatalf("search: %+v %v", found, e)
		}
	})

	t.Run("an agent's semantic writes can be undone", func(t *testing.T) {
		events, e := s.Audit.List(ctx, owner, audit.ListInput{Tool: "metric_upsert", Target: "income"})
		if e != nil || len(events.Events) == 0 {
			t.Fatal(events, e)
		}
		if _, e = s.History.Undo(ctx, owner, undoInput(events.Events[0].ID)); e != nil {
			t.Fatal(e)
		}
		if _, e = s.Semantic.Metric(ctx, owner, "income"); e == nil {
			t.Fatal("undoing a new metric should remove it")
		}
		labels, e := s.Audit.List(ctx, owner, audit.ListInput{Tool: "label_set"})
		if e != nil || len(labels.Events) == 0 {
			t.Fatal(labels, e)
		}
		if _, e = s.History.Undo(ctx, owner, undoInput(labels.Events[0].ID)); e != nil {
			t.Fatal(e)
		}
		got, e := s.Semantic.Labels(ctx, owner)
		if e != nil || got["category:groceries_home"] != "" {
			t.Fatal("label survived undo", got, e)
		}
	})

	t.Run("a share reads only the metrics its grant names", func(t *testing.T) {
		adminUser, e := s.Identity.User(ctx, admin.UserID)
		if e != nil {
			t.Fatal(e)
		}
		shared, e := s.Sharing.Share(ctx, owner, sharing.ShareInput{DashboardID: "eval-spending", Grantee: adminUser.Email, Role: "editor", Mode: "live"})
		if e != nil {
			t.Fatal(e)
		}
		access, e := s.Sharing.Access(ctx, admin, shared.ShareID, true)
		if e != nil {
			t.Fatal(e)
		}
		probe := func(metric string) dashboard.Validation {
			t.Helper()
			v, e := access.Dashboard.ValidateSpec(ctx, access.Principal, dashboard.Spec{ID: "probe", Title: "Probe", Pages: []dashboard.Page{{ID: "main", Layout: []dashboard.Row{{Cols: []dashboard.Placement{{Span: 12, Component: "s"}}}}}}, Components: []dashboard.Component{{ID: "s", Type: "stat", Metric: metric, Compare: "none"}}})
			if e != nil {
				t.Fatal(e)
			}
			return v
		}
		if v := probe("spending"); !v.OK {
			t.Fatalf("the granted metric should work: %+v", v)
		}
		if v := probe("bank"); v.OK || !strings.Contains(v.Errors[0].Msg, "source not granted") {
			t.Fatalf("an ungranted metric read the owner's ledger: %+v", v)
		}
		report, e := access.Dashboard.RunTests(ctx, access.Principal, dashboard.Spec{Tests: []dashboard.SpecTest{{Ask: "Probe", Metric: "spending", From: "2026-04-01", To: "2026-06-30", EqualsLedger: dashboard.LedgerRef{Accounts: []string{"income:"}}}}})
		if e != nil || report.Failed != 1 || report.Results[0].Got != "" || report.Results[0].Want != "" {
			t.Fatalf("a test read accounts past the grant: %+v %v", report, e)
		}
		if e = s.Sharing.Unshare(ctx, owner, shared.ShareID); e != nil {
			t.Fatal(e)
		}
	})

	testEvalTasks(t, ctx, admin, s, db)

	t.Run("keeps one user's meaning from another", func(t *testing.T) {
		if _, e := s.Semantic.Metric(ctx, admin, "spending"); e == nil {
			t.Fatal("metric crossed users")
		}
		stranger := mcpSession(t, ctx, admin, s, false)
		result, e := stranger.CallTool(ctx, &mcp.CallToolParams{Name: "metric_query", Arguments: map[string]any{"metric": "spending"}})
		if e != nil || !result.IsError {
			t.Fatal("cross-user query accepted", result, e)
		}
	})
}

// evalCaller wraps a session so every tool answer is measured and the largest one is remembered for the response budget.
func evalCaller(t *testing.T, ctx context.Context, cs *mcp.ClientSession, largest *int) (func(string, map[string]any) (map[string]any, string, bool), func(string, map[string]any) map[string]any) {
	call := func(name string, args map[string]any) (map[string]any, string, bool) {
		t.Helper()
		result, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil { // an unknown or unlisted tool is a refusal the agent sees
			return nil, e.Error(), true
		}
		raw, _ := json.Marshal(result)
		*largest = max(*largest, len(raw))
		var out map[string]any
		if b, _ := json.Marshal(result.StructuredContent); len(b) > 0 {
			_ = json.Unmarshal(b, &out)
		}
		text := ""
		for _, c := range result.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		return out, text, result.IsError
	}
	must := func(name string, args map[string]any) map[string]any {
		t.Helper()
		out, text, failed := call(name, args)
		if failed {
			t.Fatal(name, text)
		}
		return out
	}
	return call, must
}

func evalJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// testEvalTasks is the table of jobs a fresh agent does through MCP alone, on its own user and ledger. Each ends by checking state.
func testEvalTasks(t *testing.T, ctx context.Context, admin principal.Principal, s mcpserver.Services, db *mongo.Database) {
	t.Helper()
	token, e := s.Identity.Invite(ctx, admin, "eval-tasks@example.test")
	if e != nil {
		t.Fatal(e)
	}
	user, e := s.Identity.Register(ctx, token, "Eval tasks", "synthetic-test-password")
	if e != nil {
		t.Fatal(e)
	}
	owner := identity.Principal(user)
	if e = ledger.NewMongoRepository(db).Init(ctx, owner); e != nil {
		t.Fatal(e)
	}
	evalLedger(t, ctx, owner, s)
	actor := principal.Actor{Kind: "agent", ID: "https://eval-tasks.example.test/client.json"}
	largest := 0
	call, must := evalCaller(t, ctx, mcpSession(t, ctx, principal.Principal{UserID: user.ID, Scopes: principal.Scopes, Actor: actor}, s, false), &largest)
	adminScopes := append(slices.Clone(principal.Scopes), principal.ScopeAdminUsers)
	post := func(date, payee, account, amount string) {
		t.Helper()
		must("entry_post", map[string]any{"date": date, "payee": payee, "commit": true, "postings": []map[string]any{{"account": account, "amount": map[string]any{"c": "EUR", "v": amount}}, {"account": "assets:bank", "amount": map[string]any{"c": "EUR", "v": "-" + amount}}}})
	}
	must("rate_set", map[string]any{"at": "2019-12-31T12:00:00Z", "pair": "EUR/USD", "value": "1.10", "source": "Synthetic example quote"}) // postings rows convert every date they touch
	post("2025-05-10", "LIDL 789 ZAGREB", "expenses:food", "140.00")
	for i := range 30 { // enough 2020 postings to overflow an inline table, outside every window below
		post(fmt.Sprintf("2020-01-%02d", i%28+1), "Filler", "expenses:food", "1.00")
	}
	must("metric_upsert", map[string]any{"id": "spending", "label": "Spending", "unit": "eur", "kind": "flow", "additivity": "sum", "estimates": "split", "accounts": []string{"expenses:"}, "dimensions": []string{"category", "payee", "trip"}})
	must("metric_upsert", map[string]any{"id": "burn", "label": "Monthly burn", "unit": "eur", "kind": "rate", "additivity": "avg", "estimates": "include", "accounts": []string{"expenses:"}})
	query := func(extra map[string]any) map[string]any {
		t.Helper()
		args := map[string]any{"metric": "spending", "from": "2026-04-01", "to": "2026-06-30"}
		for k, v := range extra {
			args[k] = v
		}
		return must("metric_query", args)
	}
	sum := func(series any) *big.Rat {
		total := new(big.Rat)
		for _, p := range series.([]any) {
			total.Add(total, ledger.MustDecimal(p.(map[string]any)["value"].(string)).Rat())
		}
		return total
	}

	// A lab dashboard whose lint verdicts are the table below; the base spec is clean.
	lab := func(components ...map[string]any) map[string]any {
		comps := append([]map[string]any{{"id": "spend", "type": "stat", "metric": "spending", "title": "Spending"}}, components...)
		rows := []map[string]any{}
		for _, c := range comps {
			rows = append(rows, map[string]any{"cols": []map[string]any{{"span": 12, "component": c["id"]}}})
		}
		return map[string]any{"id": "lab", "title": "Lab", "version": 1, "question": "Is the lab clean?",
			"scope": map[string]any{"time": map[string]any{"default": "2026-Q2", "presets": []string{"30d"}}, "compare": "previous_period"},
			"data":  map[string]any{}, "pages": []map[string]any{{"id": "main", "title": "Main", "layout": rows}}, "components": comps}
	}
	replace := func(v any) []map[string]any { return []map[string]any{{"op": "replace", "path": "", "value": v}} }
	rowsQuery := func(limit int) map[string]any {
		return map[string]any{"a": map[string]any{"from": "postings", "where": `row.date < "2021-01-01" && !row.account.startsWith("expenses:estimated:")`, "limit": limit}}
	}
	table := func(total bool, colType string) map[string]any {
		return map[string]any{"id": "t", "type": "table", "title": "Rows", "rows": "a", "columns": []map[string]any{{"label": "Day", "value": "row.date", "type": colType, "total": total}}}
	}
	withData := func(spec map[string]any, data map[string]any) map[string]any {
		spec["data"] = data
		spec["scope"].(map[string]any)["time"].(map[string]any)["default"] = "all" // postings rows follow the page clock
		return spec
	}
	must("dashboard_create", map[string]any{"id": "lab", "title": "Lab"})
	lintOf := func(spec map[string]any) []map[string]any {
		t.Helper()
		must("dashboard_apply", map[string]any{"dashboard_id": "lab", "ops": replace(spec)})
		out := must("dashboard_lint", map[string]any{"id": "lab"})
		issues := []map[string]any{}
		for _, i := range out["issues"].([]any) {
			issues = append(issues, i.(map[string]any))
		}
		return issues
	}
	has := func(issues []map[string]any, rule string) bool {
		for _, i := range issues {
			if i["rule"] == rule {
				return true
			}
		}
		return false
	}
	unscoped := lab()
	delete(unscoped, "scope")
	unscoped["components"].([]map[string]any)[0]["compare"] = "none"
	twelve := []map[string]any{}
	for i := range 12 {
		twelve = append(twelve, map[string]any{"id": fmt.Sprintf("p%d", i), "type": "stat", "metric": "spending", "title": "Spending again", "compare": "none"})
	}
	kpi := func(extra map[string]any) map[string]any {
		c := map[string]any{"id": "k", "type": "kpi", "title": "Count", "value": "1"}
		for k, v := range extra {
			c[k] = v
		}
		return c
	}
	trend := func(axis string) map[string]any {
		return map[string]any{"id": "c", "type": "time_chart", "metric": "spending", "title": "Trend", "x": map[string]any{"type": axis}}
	}
	named := func(title string) map[string]any {
		return map[string]any{"id": "w", "type": "stat", "metric": "spending", "title": title}
	}
	copied := func(from string) map[string]any {
		return map[string]any{"from": "postings", "where": `row.account.startsWith("expenses:") && row.date >= "` + from + `"`, "agg": map[string]any{"t": "sum(row.value_eur)"}}
	}

	for _, tc := range []struct {
		name, rule string
		bad, good  map[string]any
	}{
		{"scope the page to a clock", "time_scope", unscoped, lab()},
		{"give a KPI a unit", "chart_unit", lab(kpi(map[string]any{"compare": "none"})), lab(kpi(map[string]any{"compare": "none", "format": "eur"}))},
		{"give a KPI a comparison", "kpi_compare", lab(kpi(map[string]any{"format": "eur"})), lab(kpi(map[string]any{"format": "eur", "compare": "none"}))},
		{"put time on a time axis", "time_axis", lab(trend("category")), lab(trend("time"))},
		{"keep to twelve panels", "max_panels", lab(twelve...), lab(twelve[:11]...)},
		{"keep inline tables short", "inline_table_rows", withData(lab(table(false, "date")), rowsQuery(100)), withData(lab(table(false, "date")), rowsQuery(10))},
		{"write plain words", "no_raw_ids", lab(named("Spending (card_core_median)")), lab(named("Spending"))},
		{"total only additive columns", "additive_totals", withData(lab(table(true, "text")), rowsQuery(5)), withData(lab(table(false, "text")), rowsQuery(5))},
		{"say how estimates are treated", "estimates_declared", withData(lab(table(false, "date")), map[string]any{"a": map[string]any{"from": "postings", "where": `row.date < "2021-01-01"`, "limit": 5}}), withData(lab(table(false, "date")), rowsQuery(5))},
		{"name logic once", "metric_logic_copied", withData(lab(), map[string]any{"a": copied("2026-01-01"), "b": copied("2025-01-01")}), lab()},
	} {
		t.Run("lint: "+tc.name, func(t *testing.T) {
			if bad := lintOf(tc.bad); !has(bad, tc.rule) {
				t.Fatalf("%s not caught: %v", tc.rule, bad)
			}
			if good := lintOf(tc.good); has(good, tc.rule) {
				t.Fatalf("%s still reported after the fix: %v", tc.rule, good)
			}
		})
	}

	for _, tc := range []struct {
		name string
		run  func(t *testing.T)
	}{
		{"spending in a period equals the ledger", func(t *testing.T) {
			spec := lab()
			spec["tests"] = []map[string]any{{"ask": "Spending in Q2", "metric": "spending", "from": "2026-04-01", "to": "2026-06-30", "equals_ledger": map[string]any{"accounts": []string{"expenses:"}}}}
			must("dashboard_apply", map[string]any{"dashboard_id": "lab", "ops": replace(spec)})
			if report := must("dashboard_test", map[string]any{"id": "lab"}); report["passed"].(float64) != 1 || report["failed"].(float64) != 0 {
				t.Fatal(report)
			}
			if query(nil)["total"] != "280" {
				t.Fatal("total")
			}
		}},
		{"a wrong definition fails dashboard_test", func(t *testing.T) {
			must("metric_upsert", map[string]any{"id": "food_only", "label": "Food only", "unit": "eur", "kind": "flow", "additivity": "sum", "estimates": "include", "accounts": []string{"expenses:food"}})
			spec := lab()
			spec["tests"] = []map[string]any{{"ask": "Spending in Q2", "metric": "food_only", "from": "2026-04-01", "to": "2026-06-30", "equals_ledger": map[string]any{"accounts": []string{"expenses:"}}}}
			must("dashboard_apply", map[string]any{"dashboard_id": "lab", "ops": replace(spec)})
			report := must("dashboard_test", map[string]any{"id": "lab"})
			if report["failed"].(float64) != 1 || !strings.Contains(evalJSON(report["results"]), "€280.00") {
				t.Fatal(report)
			}
			spec["tests"].([]map[string]any)[0]["metric"] = "spending"
			must("dashboard_apply", map[string]any{"dashboard_id": "lab", "ops": replace(spec)})
			if fixed := must("dashboard_test", map[string]any{"id": "lab"}); fixed["failed"].(float64) != 0 {
				t.Fatal("fixing the metric should pass", fixed)
			}
		}},
		{"a split by category sums to the total", func(t *testing.T) {
			out := query(map[string]any{"split": "category"})
			if got := sum(out["series"]); got.Cmp(big.NewRat(280, 1)) != 0 || out["total"] != "280" {
				t.Fatal(got, out["total"])
			}
		}},
		{"estimates include, exclude and split differ correctly", func(t *testing.T) {
			include, exclude, split := query(map[string]any{"estimates": "include"}), query(map[string]any{"estimates": "exclude"}), query(map[string]any{"estimates": "split"})
			if include["total"] != "280" || exclude["total"] != "250" || split["estimates"].(map[string]any)["total"] != "30" {
				t.Fatal(include["total"], exclude["total"], split["estimates"])
			}
		}},
		{"an average metric compares equal windows fairly", func(t *testing.T) {
			// Q2 holds three whole months of spending (280.00); the 91 days before it reach one day into December, so the earlier 200.00 is spread over 3 1/31 months.
			out := must("metric_query", map[string]any{"metric": "burn", "from": "2026-04-01", "to": "2026-06-30", "compare": "previous_period"})
			if cmp := out["compare"].(map[string]any); out["total"] != "93.33" || cmp["total"] != "65.96" || cmp["delta_pct"] != 41.5 {
				t.Fatal("an average is a rate per calendar month on both sides", out["total"], cmp)
			}
		}},
		{"previous_period and previous_year compare different windows", func(t *testing.T) {
			period, year := query(map[string]any{"compare": "previous_period"})["compare"].(map[string]any), query(map[string]any{"compare": "previous_year"})["compare"].(map[string]any)
			if period["total"] != "200" || period["delta_pct"] != 40.0 || year["total"] != "140" || year["delta_pct"] != 100.0 || year["from"] != "2025-04-01" {
				t.Fatal(period, year)
			}
		}},
		{"a label appears in a value", func(t *testing.T) {
			must("label_set", map[string]any{"kind": "category", "key": "groceries_home", "label": "Groceries"})
			if out := query(map[string]any{"split": "category"}); !strings.Contains(evalJSON(out["series"]), `"name":"Groceries"`) {
				t.Fatal(out["series"])
			}
		}},
		{"an alias changes the displayed payee, not the entry", func(t *testing.T) {
			must("payee_alias_upsert", map[string]any{"pattern": "^LIDL", "name": "Lidl"})
			if out := query(map[string]any{"split": "payee"}); !strings.Contains(evalJSON(out["series"]), `"name":"Lidl"`) {
				t.Fatal(out["series"])
			}
			if entries := must("entries_list", map[string]any{"limit": 500}); !strings.Contains(evalJSON(entries), "LIDL 456 ZAGREB") {
				t.Fatal("the bank text must stay in the entry")
			}
		}},
		{"an annotation appears as a band value", func(t *testing.T) {
			must("annotation_upsert", map[string]any{"id": "move", "label": "Moved flat", "from": "2026-05-01", "to": "2026-05-03"})
			spec := lab(trend("time"))
			spec["components"].([]map[string]any)[1]["annotate"] = "trips"
			must("dashboard_apply", map[string]any{"dashboard_id": "lab", "ops": replace(spec)})
			values := must("dashboard_preview", map[string]any{"dashboard_id": "lab", "values_only": true})["values"].(map[string]any)
			if !strings.Contains(evalJSON(values["c"].(map[string]any)["annotations"]), "Moved flat") {
				t.Fatal(values["c"])
			}
		}},
		{"field_values counts what the ledger holds", func(t *testing.T) {
			out := must("field_values", map[string]any{"dimension": "category", "from": "2026-04-01", "to": "2026-06-30"})
			byValue := map[string]map[string]any{}
			for _, i := range out["items"].([]any) {
				byValue[i.(map[string]any)["value"].(string)] = i.(map[string]any)
			}
			if travel := byValue["Travel"]; travel == nil || travel["count"] != 2.0 || travel["total"] == "" {
				t.Fatal(out)
			}
		}},
		{"validate_only writes nothing", func(t *testing.T) {
			before := must("dashboard_get", map[string]any{"id": "lab"})
			must("dashboard_apply", map[string]any{"dashboard_id": "lab", "ops": []map[string]any{{"op": "replace", "path": "/title", "value": "Dry"}}, "validate_only": true})
			if after := must("dashboard_get", map[string]any{"id": "lab"}); evalJSON(after) != evalJSON(before) {
				t.Fatal("a dry run changed the draft")
			}
		}},
		{"if_version rejects a stale edit", func(t *testing.T) {
			version := must("dashboard_get", map[string]any{"id": "lab"})["spec"].(map[string]any)["version"].(float64)
			_, text, failed := call("dashboard_apply", map[string]any{"dashboard_id": "lab", "ops": []map[string]any{{"op": "replace", "path": "/title", "value": "Stale"}}, "if_version": int(version) + 7})
			if !failed || !strings.Contains(text, "re-read") {
				t.Fatal("stale edit accepted", text)
			}
			must("dashboard_apply", map[string]any{"dashboard_id": "lab", "ops": []map[string]any{{"op": "replace", "path": "/title", "value": "Current"}}, "if_version": int(version)})
		}},
		{"a bad publish is rolled back", func(t *testing.T) {
			must("dashboard_apply", map[string]any{"dashboard_id": "lab", "ops": replace(lab())})
			first := must("dashboard_publish", map[string]any{"dashboard_id": "lab", "note": "Good"})["published_version"].(float64)
			edited := lab()
			edited["title"] = "Second"
			must("dashboard_apply", map[string]any{"dashboard_id": "lab", "ops": replace(edited)})
			if second := must("dashboard_publish", map[string]any{"dashboard_id": "lab"})["published_version"].(float64); second <= first {
				t.Fatal(first, second)
			}
			must("dashboard_rollback", map[string]any{"dashboard_id": "lab", "to_version": int(first)})
			if live := must("dashboard_get", map[string]any{"id": "lab", "version": "published"}); live["spec"].(map[string]any)["title"] != "Lab" {
				t.Fatal("rollback did not restore", live["spec"])
			}
		}},
		{"a staged import is listed and posts nothing", func(t *testing.T) {
			csv := "Date,Amount,Currency,Description,TransferWise ID\n2026-10-01,-12.34,EUR,\"Coffee, lunch\",ID-1\n"
			start := must("import_start", map[string]any{"account": "assets:bank", "format": "wise_csv", "content_b64": base64.StdEncoding.EncodeToString([]byte(csv))})
			items := must("imports_list", map[string]any{})["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["id"] != start["batch_id"] || items[0].(map[string]any)["status"] == "committed" {
				t.Fatal(items, start)
			}
			if october := must("metric_query", map[string]any{"metric": "spending", "from": "2026-10-01", "to": "2026-10-31"}); october["total"] != "0" {
				t.Fatal("staging must post nothing", october)
			}
		}},
		{"nav_set then empty restores the default", func(t *testing.T) {
			must("nav_set", map[string]any{"items": []map[string]any{{"id": "ledger", "label": "Ledger", "route": "/ledger"}}})
			if items, _ := s.Semantic.Nav(ctx, owner); len(items) != 1 {
				t.Fatal(items)
			}
			must("nav_set", map[string]any{"items": []map[string]any{}})
			if items, _ := s.Semantic.Nav(ctx, owner); len(items) != 0 {
				t.Fatal("an empty list should restore the default", items)
			}
		}},
		{"user_list needs admin:users", func(t *testing.T) {
			if _, text, failed := call("user_list", map[string]any{}); !failed {
				t.Fatal("listed people without admin:users", text)
			}
			_, adminMust := evalCaller(t, ctx, mcpSession(t, ctx, principal.Principal{UserID: user.ID, Scopes: adminScopes, Admin: true, Actor: actor}, s, false), &largest)
			if out := adminMust("user_list", map[string]any{}); len(out["items"].([]any)) < 2 {
				t.Fatal(out)
			}
		}},
		{"an agent can be revoked", func(t *testing.T) {
			oa := oauth.NewService(oauth.NewMongoRepository(db), s.Identity, "http://localhost:6459")
			reg, e := oa.Register(ctx, oauth.RegisterInput{Name: "Doomed agent", RedirectURIs: []string{"http://127.0.0.1:8484/callback"}, AuthMethod: "none"})
			if e != nil {
				t.Fatal(e)
			}
			resource, verifier := oa.Issuer+"/mcp", identity.Random()
			auth := oauth.Authorization{ClientID: reg.ID, RedirectURI: reg.RedirectURIs[0], ResponseType: "code", Scope: "ledger:read", State: "s", Challenge: oauth.Challenge(verifier), ChallengeMethod: "S256", Resource: resource}
			code, e := oa.Authorize(ctx, auth, owner)
			if e != nil {
				t.Fatal(e)
			}
			tokens, e := oa.Exchange(ctx, reg.Client, code, auth.RedirectURI, verifier, resource)
			if e != nil {
				t.Fatal(e)
			}
			adminCall, adminMust := evalCaller(t, ctx, mcpSession(t, ctx, principal.Principal{UserID: user.ID, Scopes: adminScopes, Admin: true, Actor: actor}, s, false), &largest)
			id := ""
			for _, g := range adminMust("agent_list", map[string]any{})["items"].([]any) {
				if g.(map[string]any)["client_name"] == "Doomed agent" {
					id = g.(map[string]any)["id"].(string)
				}
			}
			if id == "" {
				t.Fatal("agent not listed")
			}
			if _, _, failed := adminCall("agent_revoke", map[string]any{"id": id}); !failed {
				t.Fatal("revoked without a reason")
			}
			adminMust("agent_revoke", map[string]any{"id": id, "reason": "No longer used"})
			if _, e = oa.Bearer(ctx, tokens.AccessToken, resource); e == nil {
				t.Fatal("the revoked agent's token still works")
			}
		}},
		{"feed_upsert returns a secure link and stores no secret", func(t *testing.T) {
			out := must("feed_upsert", map[string]any{"id": "wise", "url": "https://example.com/statement.csv", "account": "assets:bank", "format": "generic_csv", "date_format": "iso"})
			if !strings.HasSuffix(out["secret_url"].(string), "/secure/feed/wise") || out["secret_set"] != false {
				t.Fatal(out)
			}
			if s.Imports.HasFeedSecret(owner, imports.Feed{ID: "wise"}) {
				t.Fatal("a secret was stored")
			}
		}},
	} {
		t.Run(tc.name, tc.run)
	}

	t.Run("the largest default answer fits in 25k tokens", func(t *testing.T) {
		for _, tool := range []string{"catalog_get", "metric_list", "entries_list", "accounts_list", "dashboard_list"} {
			must(tool, map[string]any{})
		}
		if largest > 100_000 { // about 25k tokens at four bytes each
			t.Fatal("largest answer is", largest, "bytes")
		}
		t.Log("largest answer", largest, "bytes")
	})
}
