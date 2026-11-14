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
