# Telemetry redesign contract

Source proposal: https://artifacts.microapps.space/a/wsrjdma80xww. This file is the frozen interface between backend and frontend. If an agent can't use a feature through MCP alone, the feature isn't finished.

Rules: agents build and run everything; people read, zoom, filter, drill, save their own views, ask an agent and flag a number. Existing MCP tools keep working for one transition release. Finance stays append-only. Exact totals are server-computed in Decimal128; the cube powers exploration only.

## Metrics, labels, aliases, annotations (component `semantic`)

```go
type Metric struct {
  ID, Label, Description string
  Kind       string   // flow | balance
  Unit       string   // eur usd pct int mo days
  Additivity string   // sum | last | avg | none
  Estimates  string   // include | exclude | split
  Accounts   []string // account id prefixes; a posting matches any prefix
  Exclude    []string // account id prefixes removed after matching
  Sign       int      // +1 or -1 applied to the posting's EUR value
  Where      string   // optional CEL over a postings row, the escape hatch
  Dimensions []string // subset of category payee account trip source
}
```

- A posting is an estimate when its account starts with `expenses:estimated` or its entry has tag `estimated`.
- `category` is `groups.category` of the account, `trip` the entry tag `trip:<name>` shown through labels, `source` is Recorded or Estimated.
- Tools (all audited, undoable where they write): `metric_upsert`, `metric_list`, `metric_query {metric, from, to, compare: previous_period|previous_year|none, split, bucket: day|week|month, filter: [{field, op, values}]}` returning decimal strings plus `text`; `field_values {field, from, to, q, limit}` returning values with counts and totals; `label_set {kind: category|account|pot|trip|commodity, key, label}`; `payee_alias_upsert {pattern, alias}`; `annotation_upsert {id, label, from, to, tag?}`, `annotation_list`.
- Accounts gain `display_name` and `note` through `account_update`. The UI shows `display_name`, else `name`; `note` only in Details.

## Spec v2 (additive on `dashboard.Spec`)

```jsonc
{ "question": "Am I spending more than I can sustain?", "home": false,
  "scope": {"time": {"default": "12mo", "presets": ["90d","12mo","ytd"]}, "compare": "previous_period"},
  "explore": {"dimensions": ["category","payee","account","trip"]},
  "answer": {"metric": "burn", "facts": ["period_change","top_contributors"]},
  "tests": [{"ask": "Spending in 2026-Q2", "metric": "burn", "from": "2026-04-01", "to": "2026-06-30", "equals_ledger": {"accounts": ["expenses:"], "exclude": ["expenses:estimated"]}}] }
```

Component additions, all optional:

```jsonc
{ "type": "stat" | "time_chart" | "ranked_bars" | "sankey" | "entries" | <existing types>,
  "metric": "burn", "split": "category", "mark": "line|area|stack|bar",
  "x": {"type": "time|category"}, "color": {"by": "entity"}, "annotate": "trips",
  "estimates": "include|exclude|split", "compare": "previous_period|previous_year|none",
  "fixed_range": "reason this panel ignores the page clock",
  "drill": {"to": "entries", "filter": {}} }
```

`Column` gains `type` (`text money number date percent`) and `additive` (bool, default false). Totals exist only for additive columns. Alignment follows `type`. Chart components gain `format` (unit) honoured by the axis.

`scope.time` makes the server inject `params.from` and `params.to`. The `time_range` page param wins over the default. A panel ignoring the clock must declare `fixed_range`.

Facts engine, deterministic and server-computed: `period_change`, `top_contributors`, `largest_entries`, `new_payees`. Result `{id, text, value, entity?, delta?}`.

## Lint, tests, publish

`dashboard_lint` returns `{rule, severity: error|warning, path, msg, hint}` for: `time_scope`, `chart_unit`, `kpi_comparison`, `time_axis`, `panel_count` (12), `inline_rows` (50), `no_raw_ids`, `additive_totals`, `estimates_declared`, `metric_not_copied`. Rules read rendered templates, so `{{ data.x }}` holes are not violations. `dashboard_test {id, version}` checks `tests` against the ledger. `dashboard_publish` requires no lint errors, a successful render and passing tests, then returns link and diff. Legacy dashboards publish with warnings until rebuilt; `error` is only for specs using `question`.

## Render

`dashboard_render {id, version, width, params}` runs the real SPA in headless Chromium through a single-use, 60-second render token, one renderer only. Result: inline PNG, `layout` (text tree), `checks {overflow, label_collisions, empty_panels, time_scope_applies}`, `lint`, `tests`, `timing_ms`. `/render/{token}/...` serves the SPA read-only for one dashboard and version, scoped to the token's principal. The Recharts stack and server SVG are removed; server keeps value computation only.

## Browser reads (session cookie, no MCP)

- `GET /api/bootstrap` → `{session, nav, dashboards: [{id,title,question,home}], labels, revision}`.
- `GET /api/dashboards/{id}/view?version=&from=&to=&month=&page=` → `{spec, status, version, author, published_at, values, computed, facts, validation, revision, as_of, stale}`. Cached gzip bytes, `ETag`, `304`. After rate-only writes the last render is returned with `stale: true` and `as_of`, and revalidates in the background.
- `GET /api/cube` → columnar JSON, gzip, `ETag: revision`:
  ```jsonc
  { "revision": 9, "as_of": "2026-10-30", "dict": {"account":[], "category":[], "payee":[], "trip":[]},
    "rows": {"day":[], "account":[], "category":[], "payee":[], "trip":[], "est":[], "eur":[], "n":[]},
    "net_worth": {"day":[], "eur":[], "estimated_until": "2025-10-01"},
    "rates": {"EUR/USD": {"day":[], "v":[]}, "XAU_g/USD": {"day":[], "v":[]}},
    "metrics": [Metric], "labels": {}, "annotations": [] }
  ```
  One row per day, account, payee and trip. `eur` is signed in ledger orientation (expense positive, income negative). Days are `YYYY-MM-DD`.
- `POST /api/feedback` `{target, value, note, context}` creates a flag. `POST /api/usage` `{path}` records one page view.
- `GET /api/views`, `POST /api/views`, `DELETE /api/views/{id}`: personal saved views `{id, name, path, search}`.

## Operate and learn tools

`nav_set {items: [{label, dashboard?|path?}]}`, `user_list`, `user_invite`, `user_disable`, `agent_list`, `agent_revoke` (scope `admin:users`, audited with reason; tool lists vary by scope), `feed_upsert` (returns a secure page link, never takes secrets), `imports_list`, `feedback_list`, `feedback_resolve {id, note}`, `usage_get {days}`. Granting `admin:users` or removing the owner or last admin needs a web confirmation page bound to that exact action. Settings adds one emergency stop that suspends all agents.

Writes accept optional `summary`, `batch_id` and `routine`. System rate writes are `routine: true`. The audit list exposes them and collapses batches.

## Frontend

- Charts: uPlot only. Recharts is removed. Donut and Sankey are small SVG renderers; RankedBars is HTML.
- `TimeBar` once per page, in the URL (`t=` the typed grammar, `cmp=`), keys `[` `]` `z` `t` `/`. `FilterBar` on dashboards, Ledger, Changes and Data: pills, value suggestions with counts, Advanced CEL. `TimeChart`: drag zoom, Ctrl/⌘ scroll, double-click out, synced crosshair, one tooltip, click-isolate legend, compare overlay, annotations, hatched estimates, table toggle, automatic day/week/month buckets.
- `Stat`, `RankedBars`, `DataGrid`, `Details` drawer (IDs, hashes, CEL, source rows, history, “How was this calculated?”, “Flag this”), Now page, ⌘K command bar.
- Nav areas: Now, Spending, Income, Budget, Future, Ledger (Journal, Accounts, Pots), Data (Datasets, Rates, Imports, Reconcile), Changes (Activity). Old URLs redirect.
- Editing controls leave the web UI: dashboard editor and actions, entry form, budget and scenario editors, dataset cell editing, import commit, new dashboard and new database. Read views stay. Owner keeps sign-in, password, connection approval and revocation, the emergency stop and the secure bank-feed page.
- Local cache: last bootstrap, views and cube in IndexedDB; paint first, revalidate by revision. All access in try/catch.
- Speed budget in CI: repeat navigation under 100 ms to paint, range change under 50 ms, first view under 400 ms on tailnet, dashboard payload under 100 KB.
- Light and dark, square hairline identity from `DESIGN.md`, amber light value `#a87700`, colour follows the entity, 390 px works, keyboard and screen-reader access. Human text only: no IDs, tool names or raw keys by default.

## Default metric definitions (seeded through `metric_upsert`, editable data)

| ID | Accounts | Exclude | Estimates | Unit |
|----|----------|---------|-----------|------|
| `spending` | `expenses:` | `expenses:reimbursed` | split | eur |
| `burn` | `expenses:` | `expenses:reimbursed`, `expenses:estimated` | exclude | eur |
| `home_baseline` | `expenses:` | `expenses:reimbursed`, `expenses:estimated`, `expenses:trip`, `expenses:one_off`, `expenses:legal` | exclude | eur |
| `income` | `income:` (Sign −1) | | include | eur |
| `net_worth` | balance of `assets:`, `liabilities:` | | include | eur, additivity last |
