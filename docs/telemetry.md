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
- New component types, all metric-driven: `stat`, `time_chart`, `ranked_bars`, `sankey`, `entries`. Fields: `metric`, `split`, `x:{type:"time"|"category"}`, `mark` (`line|area|stack|bar`), `estimates` (`include|exclude|split`), `annotate` (tag rule or `trips`), `color:{by:"entity"}`, `drill:{to:"entries",filter}`, `compare` (`previous_period|previous_year|none`; `none` states that no comparison makes sense), `fixed:{reason,range}` (opts a panel out of the page clock; shown as a "Fixed range" label; `range` is time grammar and defaults to all). `metric` names a metric on these five types and stays CEL on the legacy ones. When a spec has `answer`, its facts arrive under the reserved `values.answer` key as `{metric,label,unit,total,text,compare,sentence,facts}`.
- `Column` gains `type` (`text|money|number|date|pct|days|count`) and `total` (bool, default false). Alignment follows type. Totals exist only when `total:true`.
- Legacy charts (`type:"chart"`) gain `x.type`. `format` drives axis units in the browser.
- Spec JSON schema gets descriptions and, on input schemas, enums for `mark`, `format`, `type`, `estimates` and `compare`. `tone` holds CEL, so its description names the values it must produce (`ok`, `neg`) instead of an enum. `params.from/to` is documented in the schema and in `catalog_get`, which also returns components, metrics, dimensions, facts, time ranges, enums and lint rules.

## Metrics, labels, annotations

Stored per user, audited, undoable, all via MCP.

`Metric{id,label,description,unit(eur|usd|int|pct|mo|days),kind(flow|balance|rate),additivity(sum|last|avg|none),estimates(include|exclude|split),accounts[](prefixes),exclude[](prefixes),sign(1|-1),where(CEL escape hatch),dimensions[]}`

`unit`: `eur`/`usd` value each posting at its entry date's rate; `int` counts postings; `pct`, `mo` and `days` sum native amounts, so they suit a commodity that already holds that quantity. `kind`: `flow` sums the period, `balance` values holdings at the period end at that date's rate, `rate` is the flow per calendar month. `additivity` folds a flow's total: `sum`, `avg` (per month), `last` (the last month), `none` (no table total). Estimated means the account's `estimated` property, which defaults from the `expenses:estimated` id prefix. Dimensions are `category` (account group, else the account's name), `type`, `source`, `pot`, `class`, `account`, `payee` (after aliases), `trip` (tags starting `trip:`), `tag`, or any account group key.

Tools: `metric_upsert`, `metric_list`, `metric_query{metric,from,to,compare,split,bucket(day|week|month|auto),estimates,filters[{field,op(in|not_in|prefix),values}],facts[]}` returning exact server-computed Decimal totals: `{metric,label,unit,total,text,compare:{from,to,total,text,delta_pct,direction,good},series:[{bucket,name,value,text,estimated}],estimates:{policy,total,text,share_pct},formula,entries:{from,to,accounts,exclude},facts,truncated,message}`. `series` keeps the 20 largest names, folds the rest into Other and stops at 500 rows with `truncated:true`.
`label_set{kind(account|category|pot|tag|dimension|series),key,label}`, `payee_alias_upsert{pattern,name,note}`, `annotation_upsert{id,label,from,to,tag_rule,kind}`, `field_values{dimension,from,to,prefix,limit}` returning `{items:[{value,label,count,total}]}`.
`account_update` gains `display_name`, `note` and `estimated`; the name stops carrying notes.

`facts` is a server-computed list usable from `answer`: `period_change`, `top_contributors`, `top_detractors`, `estimate_share`, `largest_entry`. Output `{kind,sentence,value,text,direction,good,contributors:[{name,delta,text,share}]}`. The sentence is plain English, deterministic and never uses identifiers.

Verify tools: `dashboard_lint` returns `{issues:[{rule,severity(error|warning),path,component_id,msg,fix}],errors,warnings}`, where `path` is a JSON Pointer for `dashboard_apply`, for: `time_scope` page responds to time range; `chart_unit` chart declares unit; `kpi_compare` KPI has comparison; `time_axis` time on a time axis; `max_panels` at most 12 panels per page; `inline_table_rows` inline tables under 50 rows; `no_raw_ids` no identifiers in visible *rendered* text (a `{{ … }}` template is not an identifier; evaluate it first); `additive_totals` totals only on additive columns; `estimates_declared` estimates declared (a warning on raw ledger queries); `metric_logic_copied` metric logic not copied (near-identical CEL across components, a warning). `dashboard_test{id,version}` runs `spec.tests` against the ledger and returns `{passed,failed,results:[{ask,ok,metric,got,want,msg}]}`; `equals_ledger` takes `{accounts,exclude}`. `dashboard_publish` requires validation, lint errors = 0, tests pass, and `Service.RenderCheck` to pass when set; it returns the link and `diff` as changed paths, with values in `dashboard_diff`. A refusal names every failure and its fix. Rollback remains one call and applies only the validation gate.

## Browser reads

All are session-authenticated `GET`s with gzip, a weak `ETag` over the response body, leaving out `as_of` on views so a rebuild with the same numbers still answers `304`, `Cache-Control: private, no-cache` and `304` support. Authentication and access checks run before any `304`.

- `GET /api/bootstrap` → `{session:{user,csrf,version,mcp_url},nav:[{id,label,icon,dashboard|route}],dashboards:[Head],tables:[Table],counts:{imports,reconcile,feedback},labels}`. One round trip instead of session + discover + lists. `/api/session` stays for the sign-in check.
- `GET /api/dashboards/{id}/view?version=&from=&to=&month=` → `{head,spec,version,status,pages,values,computed,validation,as_of,stale}`, with `stale` always last. `version` is `draft`, `published` or a number; omitted means the draft if one exists, otherwise published, like `dashboard_get`. `pages` has resolved titles, `version` and `status` describe what is shown, and `as_of` is the RFC 3339 time of the render. `{id}` may be an editor grant id; viewer grants get 403 and revoked ones 404. Rate-only writes serve the last render with `stale:true` and its `as_of` while it refreshes; ledger and spec writes always answer fresh. The page never uses MCP for this.
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
`d` is a day offset from `start`; `v` is signed base-currency **cents** at the entry date's rate; one row per day/account/payee/tag-set; estimated flag comes from the account. Equity legs are left out, and a posting with no quote carries `v:0` and is counted in a top-level `unvalued`. `balances` is daily-resolution net worth (only changed days plus month ends is fine). Cube totals power exploring only. Any tile links to the exact `metric_query` figure in the Details drawer.
- `GET /api/changes?from=&to=&q=&routine=&cursor=` → grouped readable feed `{groups:[{id,at,actor:{kind,label},summary,count,routine,batch_id,status,undoable,targets,events:[…]}],next}`.
- `POST /api/feedback` (CSRF) → flag a number with `{target:{dashboard,component,metric,value,params},note}`.
- `GET|PUT|DELETE /api/saved-views` (CSRF on writes) → personal views. `PUT {id?,name,page,state}`, `DELETE ?id=`. Not shared, not audited as finance writes.
- `POST /api/usage` (CSRF, so send it with `fetch(..., {keepalive:true})`) with `{path,dashboard}` → page views for `usage_get`. Not audited, so a beacon never invalidates a cached view.

## Operate and learn tools

`nav_set{items:[{id,label,icon,dashboard|route}]}` and `home` on a dashboard. `user_list`, `user_invite`, `user_disable`, `agent_list`, `agent_revoke` under a separate `admin:users` scope; each audited with agent, user, scopes and reason. `admin:users` is never offered at consent. `admin_request{reason}` returns a link for the owner to add it to the calling connection; `user_disable` on any admin returns `{status:"needs_confirmation",confirm_url}`. Both open `/confirm/{token}`, a single-use, one-hour web confirmation page tied to that exact action. Passwords come from invite links and never pass through agents. `feed_upsert` takes no credential and returns `secret_url` (`/secure/feed/{id}`), the page where a person enters it; `imports_list`. `feedback_list`, `feedback_resolve{id,resolution,note}`, `usage_get{from,to}`. Owner emergency stop: `POST /settings/agents/stop` suspends every agent grant until `POST /settings/agents/resume`. Bearer checks and token refreshes then answer `403 {"error":"agents_suspended","error_description":…}`, and `GET /api/settings` reports `agents_suspended` and `is_admin`.

Audit events gain optional `summary`, `batch_id`, `routine`, excluded from the hash when empty so old events verify. Every write produces a required human `summary`. Routine system writes (rate syncs) set `routine:true`.

`tools/list` is filtered by granted scope. A dashboard-toolset agent, which also means any connection holding `dash:write` without `ledger:write`, `import:write` or `model:write`, sees under 40 tools with schemas under 60 KB. The five overlapping edit tools stay callable for one transition release but are hidden from every list in favour of `dashboard_apply`. Largest default tool response stays under 25k tokens.

## Render

`dashboard_render{id,version,width,params}` is the only preview. Chromium opens the real SPA on the app's loopback listener at `/render/{token}` (in-memory, single dashboard, version and params, read-only, 60 s, bound to the principal and any grant). Result: inline PNG plus `{layout:[…],checks:{overflow,label_collisions,empty_panels,time_scope_applies},lint,tests,timing_ms,render_ms,validation}`. A page that never reports is an error, never a blank image. A page taller than 1200 px is captured again at its full height, up to 6000 px. The server HTML/SVG renderer and `html`/`png_url` output are removed.

The SPA contract for `/render/{token}`, which needs no session and shows no app chrome:

1. `GET /api/render/{token}/view` returns the same body as `/api/dashboards/{id}/view`, always fresh, for the token's version and params.
2. `GET /api/render/{token}/cube` returns the same body as `/api/cube`. It is 404 until the cube ships and for grant tokens.
3. When the page has settled, `POST /api/render/{token}/report` with exactly `{"layout":[{"component":"id","x":0,"y":0,"w":0,"h":0}],"checks":{"overflow":["id"],"label_collisions":["id"],"empty_panels":["id"],"time_scope_applies":true},"timing_ms":0}`. Coordinates are CSS pixels from the page top. `time_scope_applies` is `null` without a page clock. Unknown fields are rejected, and it answers 204.

Chromium's virtual time runs page timers fast and waits for fetches, with an 8 second budget. The screenshot comes after the budget, so report once and do not animate afterwards. The publish gate is `preview.Service.Gate`; call it before entering a workflow.

## Speed

Fixes: no revision bump for no-op rate syncs; rate-only writes keep the last render (`stale`); audit outcomes batched, tool and actor indexes, no N+1; `(user_id, postings.account, date)` index; `reconcile_status` read-only for interactive calls; `tailscale serve` route for the app with the public domain kept for agents and sharing. CI budget (`e2e/spa-speed-budget.mjs`): repeat navigation under 100 ms to paint, range change under 50 ms, INP at most 200 ms, first view under 400 ms on tailnet, payload under 100 KB.

## Web UI

Shared pieces: TimeBar, FilterBar, TimeChart (uPlot), RankedBars, Stat, DataGrid, Details drawer, Command bar. Recharts is removed. Navigation (`nav_set`, with this default): Now, Spending, Income, Budget, Future, Ledger, Data, Changes. Editing controls leave the web UI: dashboard editor and publish, post entry, budget and scenario editing, dataset cell editing, imports and reconcile actions. People keep Settings (password, connected agents, emergency stop) and approvals for escalations.
