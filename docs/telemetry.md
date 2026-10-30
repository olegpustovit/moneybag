# Telemetry redesign contract

The product rule: **agents build and run everything through MCP; people read, zoom, filter, drill, save personal views, flag numbers and ask an agent.** A feature is finished only when it has its spec field, catalog entry, lint rule and eval task. Everything below is optional in the spec so existing dashboards keep working until they are rebuilt.

## Spec v2 additions

`Spec`: `question`, `home` (bool), `scope`, `explore`, `answer`, `tests`.

```json
{"question":"Am I spending more than I can sustain?","home":false,
 "scope":{"time":{"default":"12mo","presets":["30d","90d","12mo","ytd","all"]},"compare":"previous_period"},
 "explore":{"dimensions":["category","payee","account","trip"]},
 "answer":{"metric":"burn","facts":["period_change","top_contributors"]},
 "tests":[{"ask":"Spending in 2026-Q2","metric":"spending","from":"2026-04-01","to":"2026-06-30","equals_ledger":{"accounts":["expenses:"]}}]}
```

- `scope.time.default` uses the time grammar. `scope.compare`: `previous_period | previous_year | none`. A dashboard with `scope` passes `params.from`/`params.to`/`params.compare_from`/`params.compare_to` to every CEL expression.
- New component types, all metric-driven: `stat`, `time_chart`, `ranked_bars`, `sankey`, `entries`. Fields: `metric`, `split`, `x:{type:"time"|"category"}`, `mark` (`line|area|stack|bar`), `estimates` (`include|exclude|split`), `annotate` (tag rule or `trips`), `color:{by:"entity"}`, `drill:{to:"entries",filter}`, `compare`, `fixed:{reason}` (opts a panel out of the page clock; shown as a "Fixed range" label).
- `Column` gains `type` (`text|money|number|date|pct|days|count`) and `total` (bool, default false). Alignment follows type. Totals exist only when `total:true`.
- Legacy charts (`type:"chart"`) gain `x.type`. `format` drives axis units in the browser.
- Spec JSON schema gets descriptions and enums for `mark`, `format`, `tone`, `type`, and `params.from/to` is documented.

## Metrics, labels, annotations

Stored per user, audited, undoable, all via MCP.

`Metric{id,label,description,unit(eur|usd|int|pct|mo|days),kind(flow|balance|rate),additivity(sum|last|avg|none),estimates(include|exclude|split),accounts[](prefixes),exclude[](prefixes),sign(1|-1),where(CEL escape hatch),dimensions[]}`

Tools: `metric_upsert`, `metric_list`, `metric_query{metric,from,to,compare,split,bucket(day|week|month|auto),filters[{field,op,values}]}` returning exact server-computed Decimal totals: `{unit,total,text,compare:{total,text,delta_pct,direction,good},series:[{bucket,name,value,text,estimated}],estimates,formula,entries:{from,to,accounts}}`.
`label_set{kind(account|category|pot|tag|dimension|series),key,label}`, `payee_alias_upsert{pattern,name,note}`, `annotation_upsert{id,label,from,to,tag_rule,kind}`, `field_values{dimension,from,to,prefix,limit}` returning `{value,label,count,total}`.
`account_update` gains `display_name` and `note`; the name stops carrying notes.

`facts` is a server-computed list usable from `answer`: `period_change`, `top_contributors`, `top_detractors`, `estimate_share`, `largest_entry`. Output `{kind,sentence,value,text,direction,good,contributors:[{name,delta,text,share}]}`. The sentence is plain English, deterministic and never uses identifiers.

Verify tools: `dashboard_lint` returns `{rule,severity(error|warning),path,component_id,msg,fix}` for: page responds to time range; chart declares unit; KPI has comparison; time on a time axis; at most 12 panels per page; inline tables under 50 rows; no identifiers in visible *rendered* text (a `{{ … }}` template is not an identifier; evaluate it first); totals only on additive columns; estimates declared; metric logic not copied (near-identical CEL across components). `dashboard_test{id,version}` runs `spec.tests` against the ledger. `dashboard_publish` requires lint errors = 0, tests pass, and the render checks to pass when a renderer exists; it returns the link and diff. Rollback remains one call.

## Browser reads

All are session-authenticated `GET`s with gzip, `ETag` derived from the revision and params, and `304` support.

- `GET /api/bootstrap` → `{session,nav,dashboards,tables,counts:{imports,reconcile,feedback},labels}`. One round trip instead of session + discover + lists.
- `GET /api/dashboards/{id}/view?version=&from=&to=&month=` → `{head,spec,values,computed,validation,as_of,stale}`. Rate-only writes serve the last render with `stale:true` and `as_of` while it refreshes. The page never uses MCP for this.
- `GET /api/cube` → columnar ledger cube, under 150 KB gzipped on 10k entries:
```json
{"revision":1,"as_of":"2026-10-30","base":"EUR","start":"2023-01-01","end":"2026-10-30",
 "accounts":[{"id":"expenses:x:y","label":"Groceries","category":"Groceries","type":"Everyday","pot":"","source":"","class":"expense","estimated":false}],
 "payees":["Lidl"],"tag_sets":[["trip:Lisbon"]],
 "flows":{"d":[0,0,1],"a":[0,1,0],"p":[0,1,0],"t":[0,0,1],"v":[1234,-50,900],"n":[1,1,2]},
 "balances":{"d":[0,31],"net":[100,120],"assets":[100,120],"liabilities":[0,0],"estimated_until":"2025-09-30"},
 "rates":{"EUR/USD":{"d":[0],"v":[1.1]}},
 "metrics":[],"annotations":[],"labels":{}}
```
`d` is a day offset from `start`; `v` is signed base-currency **cents** at the entry date's rate; one row per day/account/payee/tag-set; estimated flag comes from the account. `balances` is daily-resolution net worth (only changed days plus month ends is fine). Cube totals power exploring only. Any tile links to the exact `metric_query` figure in the Details drawer.
- `GET /api/changes?from=&to=&q=&routine=&cursor=` → grouped readable feed `{groups:[{id,at,actor:{kind,label},summary,count,routine,batch_id,status,undoable,targets,events:[…]}],next}`.
- `POST /api/feedback` → flag a number with `{target:{dashboard,component,metric,value,params},note}`.
- `GET|PUT|DELETE /api/saved-views` → personal views (page URL state). Not shared, not audited as finance writes.
- `POST /api/usage` beacon → page views for `usage_get`.

## Operate and learn tools

`nav_set{items:[{id,label,icon,dashboard|route}]}` and `home` on a dashboard. `user_list`, `user_invite`, `user_disable`, `agent_list`, `agent_revoke` under a separate `admin:users` scope; each audited with agent, user, scopes and reason. Granting `admin:users` or removing the owner or last admin needs a web confirmation page tied to that exact action. Passwords come from invite links and never pass through agents. `feed_upsert` returns a secure-page link for bank secrets and only ever stores a reference; `imports_list`. `feedback_list`, `feedback_resolve{id,resolution,note}`, `usage_get{from,to}`. Owner emergency stop: a single action suspending every agent grant until re-enabled.

Audit events gain optional `summary`, `batch_id`, `routine`, excluded from the hash when empty so old events verify. Every write produces a required human `summary`. Routine system writes (rate syncs) set `routine:true`.

`tools/list` is filtered by granted scope. A dashboard-toolset agent sees under 40 tools with schemas under 60 KB. The five overlapping edit tools stay callable for one transition release but are hidden from the dashboard toolset in favour of `dashboard_apply`. Largest default tool response stays under 25k tokens.

## Render

`dashboard_render{id,version,width,params}` is the only preview. Chromium opens the real SPA at `/render/{token}` (in-memory, single dashboard, read-only, 60 s). The page posts its report to `/api/render/{token}/report`. Result: inline PNG plus `{layout:[…],checks:{overflow,label_collisions,empty_panels,time_scope_applies},lint,tests,timing_ms}`. The server HTML/SVG renderer and `html`/`png_url` output are removed.

## Speed

Fixes: no revision bump for no-op rate syncs; rate-only writes keep the last render (`stale`); audit outcomes batched, tool and actor indexes, no N+1; `(user_id, postings.account, date)` index; `reconcile_status` read-only for interactive calls; `tailscale serve` route for the app with the public domain kept for agents and sharing. CI budget (`e2e/spa-speed-budget.mjs`): repeat navigation under 100 ms to paint, range change under 50 ms, INP at most 200 ms, first view under 400 ms on tailnet, payload under 100 KB.

## Web UI

Shared pieces: TimeBar, FilterBar, TimeChart (uPlot), RankedBars, Stat, DataGrid, Details drawer, Command bar. Recharts is removed. Navigation (`nav_set`, with this default): Now, Spending, Income, Budget, Future, Ledger, Data, Changes. Editing controls leave the web UI: dashboard editor and publish, post entry, budget and scenario editing, dataset cell editing, imports and reconcile actions. People keep Settings (password, connected agents, emergency stop) and approvals for escalations.
