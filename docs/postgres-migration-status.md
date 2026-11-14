# PostgreSQL migration execution

Plan: https://artifacts.microapps.space/a/jvyzek5xcjz7 (version 4).
Source baseline: `dev` at `55c5d50`. Work branch: `pg-migration`.
The owner explicitly authorized replacing the MongoDB rules on 2026-11-14 and proceeding with reasonable assumptions without clarification pauses.
Production still runs MongoDB. This branch is not ready for cutover.

## Prerequisites and baseline, 2026-11-14

- VM root has 44 GB free. No cleanup of unrelated databases or releases was needed.
- Swap is absent; remote passwordless sudo is unavailable. The swap prerequisite remains open. No privilege workarounds or shared server configuration changes were made.
- Mongo backup saved privately on the VM under `ci/moneybag/backups/` and copied off-host to `~/.local/state/moneybag-pg/`. SHA-256: `8da3f816b44c75f36038ec1958779653d8df68546c84681599694a48d25614de`.
- Restored that archive into local disposable MongoDB on 27098, database `moneybag_restoretest`. All 38 dumped collection counts match the dump log exactly: 69,678 documents. These are counts of the backup, not an assertion that live production stopped changing.
- Production also contains `system.profile`, excluded automatically by mongodump. Preserved all 5,679 profiler records separately as canonical extended JSON, copied off-host, SHA-256 `23463c842f0c4bbeb16c92476c9ffb71dbc1ab7f73c521d05cf823055c68b83d`. Final copy-tool mapping must account for this collection explicitly; it must not silently skip it.
- Dedicated `moneybag` PostgreSQL login and database created in the existing cluster. Database uses UTF8 and C collation/ctype; role has 16 MB work_mem and a 30 s statement timeout. Staged credential lives in mode-0600 `ci/moneybag/pg-migration.env`; production's `moneybag.env` is unchanged. No PostgreSQL server configuration edits.
- Off-VM nightly pull backup: `deploy/backup-postgres.sh`, installed as `~/.local/libexec/moneybag-backup-postgres`, with the accompanying systemd user service/timer. Timer is enabled for 02:30 UTC with missed-run catch-up. Archives stay private under `~/.local/state/moneybag-pg/nightly/`. Initial database is empty; restore validation must be repeated with cutover data. The off-VM machine must be online with its user manager and SSH credentials available.
- The first backup attempt exposed that the remote login shell is fish. The script now explicitly runs bash and parses the URL into libpq environment variables, so credentials do not become command arguments. The staged credential was rotated after the failed shell attempt. No production credential was changed.
- Original Mongo build in `/tmp/moneybag-mongo-baseline` passes `e2e/speed-budget-ci.sh`: repeat navigation median 18 ms, worst 24 ms; range change median 18 ms, worst 23 ms; bootstrap 2.4 KB, dashboard view 16.3 KB; range-step CLS 0.0003 and one view request. Seed emitted existing publication-gate refusals for synthetic dashboards; the speed budget itself passed. This is a synthetic baseline, not production parity. Log: `/tmp/moneybag-mongo-speed-baseline.log`.

## Rules and infrastructure

- Rules replaced and branch pushed in `2eab3f3`; `dev` retains its original rules until the cutover merge.
- Added pgx v5.13.0 and goose v3.30.1, the current Go-1.26-compatible releases. Goose provider and session-lock APIs checked against upstream documentation and installed package docs.
- PostgreSQL package implements five-connection pool with ping, embedded forward migrations under advisory locking, owner-scoped atomic counters, not-found/unique helpers, and the unchanged workflow lock.
- Initial DDL currently covers counters, including the `rates` revision field omitted by the plan's abbreviated DDL. Remaining component tables are still to be added before Gate 0 is complete.
- `pgtest.New` clones a migrated template into isolated disposable databases. A unique template remains per test process until the disposable PostgreSQL container is removed.
- Golden fixture captures 272 original `ParseDecimal` outputs/errors from `55c5d50`, including signs, zero scale, 34/35 digits, precision-reducing trailing zeros, fractional exponent formatting, and the 80-character boundary. The Decimal representation is still BSON-backed pending the ledger port. Added SQL Value/Scan without changing its public JSON.
- Passing checks: `go build ./...`; targeted ledger/PostgreSQL vet and unit tests; real PostgreSQL 18 concurrent migration/idempotency tests, concurrent and owner-isolated counters, SQLSTATE classification, workflow nesting/cancellation, and exact numeric round-trips. No existing test expectation changed.

## Remaining work

Phase 0 swap prerequisite; complete Phase 2 schema/Decimal/harness/sweeper; all component ports and Gates A–C; copy and verification tool and Gates D/E; cutover merge/deployment/smoke/production parity; day-30 reminder and delayed cleanup. None of these gates is claimed complete by the infrastructure checks above.

Unrelated pre-existing edits to `agentpack.toml` and `pack.lock` are preserved and excluded from migration commits.

## Rates port

- `rates` now uses PostgreSQL only. `prices.at` is timestamptz; projections return the same UTC RFC3339 second strings as the original code. Values remain exact numeric through Decimal's SQL methods. Provider-state string fields remain strings.
- Ported quote set/get/history, inverse lookups, request cache, archival, provider tracking/skip, ECB backfill and gold quote discovery. Unique `(user_id,pair,at)` and per-user time/provider indexes are in DDL.
- R2: all prices `$ne:true` filters become `archived=false` with `NOT NULL DEFAULT false`; `$in` pair lookup becomes `pair=ANY(@pairs::text[])`; absent provider state is `pgx.ErrNoRows`. No `$exists`, `$nin`, `$or` or null filters in this component.
- Existing cache query-count and provider-sync/correction tests pass with unchanged assertions against PostgreSQL; combined run 2.708 s. Rates unit tests, vet, and whole-app build pass.
- During the staged branch port, API startup opens/migrates PostgreSQL for ported components, while unported components still use their original MongoDB holders. There is no alternate driver implementation or datastore feature flag. This intermediate branch requires both connections and must not deploy. Final wiring will remove Mongo completely, put DATABASE_URL validation in loadConfig, and switch the health ping.
- Existing integration fixture setup now gives rates its own disposable PG database while other components remain unported; the cache test uses a pgx query tracer instead of Mongo's command monitor. No assertion values changed.
- The off-VM nightly timer's actual archive was restored successfully on PostgreSQL 18. It currently covers the empty pre-cutover database; populated-data verification is still required at cutover.

## Feedback port

- Flags, resolve/reopen, saved views and usage now use PostgreSQL only. Nested targets/actors remain jsonb. Saved views retain both the original Mongo `_id` as `storage_key` and their public logical `id`; using the same column for both would lose data.
- Usage retains its original per-owner/day/path/dashboard key, atomic increment, maximum timestamp, grouping, sort and 200-row cap. Saved-view upserts and deletes keep owner guards; an inconsistent conflicting storage key cannot transfer another owner's view.
- R2: this component has no `$ne`, `$exists`, `$in`, `$nin`, `$or` or null filters. Optional resolved timestamps and saved state remain nullable; missing non-pointer fields get zero defaults.
- Passing checks: whole-app build and feedback vet; existing view/bootstrap regressions (2.409 s); `TestSharedDatabaseLedgerIdentityOAuth` including its full product/MCP/telemetry workflows (11.615 s) with unchanged assertion values; PostgreSQL tests for saved-state round-trip, update/delete isolation, non-null empty lists, concurrent usage increments and owner isolation.

## Local-only verification policy

The owner requested no GitHub CI on 2026-11-14. The earlier branch pushes had triggered the repository's automatic workflow. Run 43603274847 is confirmed completed/cancelled; prior runs were already terminal. `df5fc6a` changes the workflow to manual dispatch only and records the policy in AGENTS.md. All further migration commits include `[skip ci]`; do not dispatch GitHub workflows.

## Identity and OAuth port

- Ported identity users, binary credentials, sessions, invitations and setup marker. Preserved ordered non-atomic setup/registration/password changes, invitation single-use guards and session invalidation. Writes use UTC milliseconds. Maintenance principals keep the original projection's non-admin behavior.
- Ported OAuth clients, code exchange, refresh rotation/reuse revocation, grants, scopes, agent names and emergency stop. Bearer authentication remains exactly one query joining token, grant, current user and suspension, with the original public error identities for representable states. No new multi-statement transactions.
- The two ports are verified together because Bearer joins directly against identity's users. `pgtest.New(t)` now returns the same pool to all components of one test. Nested test fixtures capture the parent fixture pool rather than creating a separate database in a subtest.
- R2 identity: `invites.used_at $exists:false` -> `used_at IS NULL`; expiry `$gt` -> `expires_at>@now`. No other listed R2 operators. OAuth: revocation/used equality -> boolean equality on non-null columns; revoke `$or` -> `id=@hash OR refresh_hash=@hash`; scopes `$addToSet` -> conditional `array_append`, preserving order and uniqueness. Missing rows map to the original component errors.
- Tests retain HTTP/MCP expectations for ordinary behavior, including the one-query bearer assertion. **Mongo-internal fixture exception:** TestBearerOneRead previously inserted an absent revoked field or BSON arrays into scalar grant_id/access_expires_at/used fields and expected authentication refusal. PostgreSQL's NOT NULL/scalar types cannot hold those states. Those four fixtures now assert write-boundary refusal and that the unchanged valid token still authenticates, rather than pretending malformed scalar SQL rows exist. This is an explicit test-contract adaptation, not an identical-assertion claim.
- Passing local checks: identity/OAuth unit tests and vet; whole-app build; PostgreSQL infrastructure/ledger/feedback/OAuth checks; core product/MCP/HTTP/telemetry plus bearer, bootstrap and rate-sync regressions, combined 13.671 s.
- One earlier combined run reported a stale scheduled-rate snapshot; a diagnostic-only addition leaves the original assertion intact. Focused and subsequent combined runs passed. The full-suite diagnostic later established an early monotonic-timer wake while wall-clock time was still before the quote date; see the audit/full-suite section below for the fix.
- The migration-idempotency test now clears the entire isolated test schema before concurrent migration, so new component tables are exercised too; its assertions are unchanged.
- Classification remains to port: its direct accounts read requires the ledger table/data port before the existing cross-component tests can pass. Do not replace that read with a behavior-changing service shortcut just to make the leaf ordering work.

## Admin port

- Confirmation persistence now uses PostgreSQL. Issuing, owner-only lookup, expiry and atomic single-use consumption retain existing behavior. `UPDATE ... RETURNING` deliberately returns NULL for the previous used_at field, matching Mongo FindOneAndUpdate's pre-update response, while the stored row is consumed.
- R2: used_at `$exists:false` -> `IS NULL`; expiry `$gt` -> `>@now`. Requested actor stays jsonb; timestamps are UTC milliseconds.
- Local app build, admin vet/unit compile and the complete core/product/HTTP/MCP/telemetry regression pass (9.697 s), without changing their assertions.

## Sharing port

- Grants, active/revoked/expired resolution, owner/recipient lists, snapshot persistence, refresh/unpublish deletion and refresh-due scanning now use PostgreSQL. The obsolete recursive BSON-to-map normalizer is deleted; jsonb decoding already produces native JSON maps/arrays.
- Frozen legacy `rendered.html` remains inside the rendered jsonb object and is read explicitly for the old HTML fallback. The copy tool must preserve that raw legacy key, not lose it by decoding only the current Rendered struct.
- R2: revoked_at `$exists:false` -> `IS NULL`; active expiry `$or` -> `expires_at IS NULL OR expires_at>@now`; all owner/grantee guards retained. No new multi-statement transaction.
- Concrete schema exception required by the core workflow: sources is a **nullable** text array. A source-free dashboard legitimately produces nil sources; coercing it to an empty array changes both public JSON and PermissionHash, invalidating its snapshot. Both nil and empty values must survive the copy. The first component check caught this NOT NULL failure; the schema was corrected without changing assertions.
- Local app build, sharing vet/unit compile and core plus restricted-share/legacy-HTML regression pass (11.556 s), with unchanged behavioral assertions.


## Audit port and staged full backend check

- Audit intent, outcomes, revisions, list/get/changes, latest-completed lookup, undo guards and verification now use PostgreSQL. The workflow lock moved unchanged to PostgreSQL; every snapshot read guard now recognizes that same context marker. No second lock remains in the Mongo package.
- Actor and target stay jsonb with indexed actor_id/target_id columns. **Raw before/after payloads use bytea**, because their original bytes (including key order and decimal text) are hashed and BSON stored json.RawMessage as binary. Re-encoding those values through jsonb would invalidate historical hashes. The new real-PG test preserves a non-alphabetical payload with an integer beyond float precision, verifies its chain, and detects a modified outcome.
- R2: outcome/grant-tool `$in` -> typed `ANY(text[])`; optional date bounds -> nullable timestamp parameters; missing outcomes still mean interrupted; counters still advance after a partial or failed callback. The previous query and callback ordering is retained.
- Fixture/measurement adaptations: audit-row inserts/counts/outcome tampering now address PostgreSQL; read/write monitors count both databases while the branch is staged. The planner test checks actual PostgreSQL EXPLAIN ANALYZE index scans with the original 70/42-row limits and representative cross-owner cardinality, while the unported journal retains its original Mongo planner test. The read-only render check now counts both stores.
- Full original-Mongo baseline passed locally in 31.017 s after correcting the disposable Mongo container's startup file limit to 64,000. Two earlier attempts timed out after the restore container inherited a 1,024-file startup limit; increasing the live limit was insufficient, so only that disposable container was recreated and the private scratch archive restored. No production process or system MongoDB configuration changed. Logs: `/tmp/moneybag-mongo-backend-baseline*.log`.
- The first staged full suite caught a missing PostgreSQL read in the prewarm monitor and reproduced the scheduled-rate timing failure. Its diagnostic read wall time **14:35:55.253**, before the scheduled **14:35:56** quote, after the monotonic timer woke. The test now waits until its original wall-clock deadline is reached; it keeps the same expected rate. This fixes test timing under host clock correction, not rate-cache behavior. Prewarm again counts the original one read using a SQL tracer.
- Full staged backend suite now passes locally with both dedicated test URLs and the real Chromium renderer: 37.513 s. Whole-backend vet and build pass. This is **20.94% above** the 31.017 s Mongo baseline; the performance comparison remains open for the final PostgreSQL-only gate. This intermediate suite still initializes the old Mongo index inventory alongside new PostgreSQL databases, adds real-PG/hash/schema checks, and includes the corrected wall-clock wait; do not claim the final speed gate from it. Logs: `/tmp/moneybag-pg-stage-backend*.log`.
- The disposable PostgreSQL container is back to loopback-only publishing before any production-copy data is loaded. Its earlier all-interface diagnostic binding held synthetic test data only. The existing local Docker-exec relay supplies access from the tool network namespace.

## Ledger, classification, budget and reconciliation port

- Ledger journal/accounts/tag overlays/balance markers now use PostgreSQL only. Ported account editing/closure, posted/draft writes, commitment/reversal, exact numeric balances and history, lots, linked corrections, bulk references, filtering and keyset pagination, amount/date sorting, owner guards, effective reversal chains and bounded shared/combined journal reads. Removed the old ledger Init's cross-component Mongo index inventory; owner-specific DDL now supplies migrated indexes.
- Budget and classification are ported with their direct reads of ledger-owned tables. Reconciliation is ported with its direct journal aggregation, status, discrepancy resolution/reopening, assertion archival and verification. Existing validation and ordered, non-atomic audited workflows remain; no multi-statement transaction was added.
- R2: absent closed_at -> IS NULL; archived != true -> NOT archived on non-null defaults; legacy absent/zero revision -> revision=0; logical owner/id and old-status predicates remain on conditional updates. ID/account membership uses typed ANY(text[]), and empty filter arrays are explicitly distinguished from MatchIDs=nil. Posting/link nil JSON slices remain JSON null and nullable text arrays preserve nil versus empty. Missing rows retain the old component-specific error mapping.
- Scalar budget limits use numeric for arithmetic and a nullable limit_lexeme only for negative zero, because PostgreSQL otherwise rewrites -0.00 as 0.00. This is required by the original decimal/public-string contract; nested posting amounts/prices retain string values in JSONB. New PostgreSQL tests cover exact cancellation beyond float precision, negative zero, revisions, group mappings, immutable journal reads, Unicode literal search, tags, balances, reversals and audit integrity. Payee regexes use PostgreSQL 18's built-in pg_c_utf8 collation to retain Unicode case matching without changing the database's C sorting collation.
- The combined SQL query explicitly orders entry and head frames; raw JSON decoding restores the owner field omitted from public JSON. The 32 MiB/250k-entry bounds, 5000-group bounds, narrow-range fallback and request sharing are retained. 64-bit cursors have explicit SQL casts.
- Fixture adaptations are explicit: raw typed SQL fixtures replace Mongo inserts; tracers observe the same query categories as the original monitors. Oversized combined streaming now expects exactly three SQL reads (tags, combined attempt, fallback), replacing Mongo's getMore-dependent lower bound of four. Invalid scalar tag fixtures now assert PostgreSQL write rejection and intact head results; SQL cannot store the old malformed BSON scalar in a text-array column.
- The journal EXPLAIN test follows the plan's explicit instruction to defer postings/tag GIN indexes: it measures the bounded 4000-row owner scan and the same 100 matching rows, rather than asserting Mongo's removed postings index. The final speed gate, not a fabricated SQL index assertion, decides whether GIN is needed. Existing audit index-plan limits remain unchanged. Read-only render document-count checks include all eight new tables.
- Targeted original pagination, budget precision/cache, large journal, balance history, combined-source/cache fallback, source cache, reconciliation aggregation/batched verification and core workflow assertions pass locally. Whole-backend vet and build pass. Full-suite result for this checkpoint is recorded below after completion.
- Full local backend suite passes at this checkpoint (integration package 43.311 s), with both disposable database URLs and the real Chromium renderer. Log: `/tmp/moneybag-pg-ledger-stage-green.log`. This staged run is still slower than the original baseline; the final PostgreSQL-only performance gate remains open. No GitHub workflow was run.

## Model port

- Scenarios and income models now use PostgreSQL. Scenarios retain storage/logical IDs, conditional revision guards, archive invalidation, legacy inputs, overrides and event arrays. Income streams remain one nested JSONB value, preserving nil versus empty and the existing public number types. Computed/input-only scenario fields are not persisted.
- R2: archived != true -> NOT archived with a non-null default; missing/zero revision -> revision=0; missing income model -> original empty state. Old sequential audit/write behavior remains; no transaction was added.
- Local model unit tests, model/live revision and concurrent-update integration, income streaming/limits, and the full core workflow pass (combined integration 13.789 s). The read-only count check includes the two new tables.
- Money-core batch full backend suite passes locally (integration 42.640 s), with Chromium and both disposable databases. Model vet and whole-app build pass. Log: `/tmp/moneybag-pg-money-batch.log`; final performance comparison remains open.
