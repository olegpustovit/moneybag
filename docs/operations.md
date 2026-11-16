# Operations

This build uses the existing PostgreSQL 18 cluster on oracle-vm, a dedicated `moneybag` role/database with C collation and one API writer. Caddy and PostgreSQL are managed separately; Moneybag does not change their server configuration. See [the migration ledger](postgres-migration-status.md) for the actual production cutover state.

## Test

`make test-integration` runs the complete Go suite against disposable PostgreSQL 18 and removes its container afterward. `pgtest.New` creates isolated test databases from a migrated template. Receipt, reference, key, batch and undo tests use the same PostgreSQL implementation. Run checks locally or through SSH; do not dispatch GitHub CI. Real-data read/write comparisons use [the parity runbook](../e2e/PARITY.md).

## Table writes on PostgreSQL

Logical schemas and row cells retain their existing shape, with nested JSONB values. The API serializes workflows in the existing single process. Each row and its replacement `row_keys` records commit in one transaction; a key conflict rolls that row back. This does not make a whole batch atomic.

`db_batch` prepares the complete proposed row state and validates cells, versions, references, keys, checks and ledger sums before writing. A validation failure saves no rows. Dry runs create no rows or audit events. After validation, rows are saved in deterministic order with one audit event. A successful batch can be undone with one call, subject to row-version checks.

**Multi-row writes are not atomic.** A database failure or interruption can leave earlier rows saved. Recovery errors report confirmed writes and, when available, the audit event id. The failing write may also have committed in PostgreSQL if its acknowledgement was lost. Reload affected rows, inspect the audit payload, run `consistency_check`, and complete or correct the intended data. Do not blindly retry with stale versions. No compensating writes or automatic rollback are performed. Receipt/table writes never modify the ledger's entries or postings.

`db_update_cells`, `db_delete_rows`, schema migrations and undo retain the same ordered, partial-write contract. Key metadata is replaced with each row; it is never cleared in advance. The `row_keys` primary key enforces owner/table/hash uniqueness throughout. A swap or transfer of an occupied unique key is refused before writes; use a distinct temporary key in a separate update when rearranging keys.

`consistency_check` reports broken references as `table_references`, and invalid cells, key metadata, checks and receipt ledger sums as `table_constraints`. Scans have explicit budgets and fail rather than silently truncating. Correct incomplete rows through the ordinary tools; the financial ledger remains the source of truth.

### Schemas and templates

Columns support exact `decimal` values, offset-preserving RFC 3339 `datetime`, `entry`, `account`, `ref`, string `list`, and bounded object `json`, plus descriptions/required/enum/pattern metadata. Composite keys use owner/table/key-hash records in `row_keys`. Row checks may block writes or return warnings. Ledger constraints compare exact row amounts with the configured account prefixes in one commodity; reversed entries are exempt. Removing all rows leaves an entry unexplained.

`db_alter` validates active rows before accepting new constraints. Constraint changes recompute derived values; ordinary formula additions retain the existing deferred behavior. A storage failure during a multi-row migration can leave earlier derived values saved, so inspect consistency and retry after resolving the cause. `keys:[]`, `checks:[]` and `ledger:{}` clear their respective guarantees. Schema undo preserves compatibility with older audit events.

`db_templates` returns ordinary schemas and an agent playbook. `db_create {template:"receipts"}` creates merchants, stores, products, receipts and receipt_items. Repeating it reuses identical active schemas and creates missing tables, allowing recovery after partial creation. Conflicting or archived schemas are never overwritten. Its event records only tables newly created by that call; undo archives those tables in reverse reference order. Creation and multi-table archive can partially succeed on storage failures.

### Joined reads

Raw `db_query` cells retain their original behavior. `join_entry:true` adds entry metadata; orphan entries yield `entry:null`. CEL sees the joined object under `row.entry`, while original-id equality/membership still work. A custom entry-column alias uses the stored column; an existing conflicting `entry` column causes an explicit error instead of hiding data.

`expand` inlines one level of owner-scoped refs. Missing/archived targets become null. `_row_id` identifies the target (also `id` when that column does not already exist). Historical EUR valuation uses exact decimals; missing quotes return a null value with a valuation error, while storage failures fail the query.

Literal equality/membership on key/ref/entry/account cells runs as a bound SQL predicate before CEL filtering. Owner/table/row indexes bound reads; per-cell GIN indexes remain deferred. Row-id pagination returns `next_cursor` and explicit `truncated`; custom sorting does not accept a cursor. Analytic `table:` sources join automatically, drop ineffective/orphan entries, and read complete bounded sources beyond 5,000 rows or return an error. Joined output also has a byte budget.

`entry_get.explained_by` lists table/row ids; positive and negative `entries_list.explained_by` filters run before ledger pagination.

## Deploy

Create `/home/snowbear/ci/moneybag/moneybag.env` with mode `0600` using `deploy/env.example`. Preserve its secret key across releases. Set `MONEYBAG_CHROMIUM_BIN` to a working Chromium installation; the VM already has `/opt/google/chrome/chrome`.

Run `deploy/deploy.sh` from a clean committed `dev` checkout, after the app's production gate. It requires a known executable previous release for rollback. Before any build, SSH or upload, it validates the persistent eval credentials described below. It runs local checks, builds a static ARM64 binary, verifies the uploaded checksum, atomically replaces `current` and checks the running release's exact health/version. The local browser smoke then signs in through the real app/ID flow. Health or smoke failure restores the previous symlink and verifies its version while preserving the original failure status. Old releases remain available for manual rollback. `deploy/deploy.sh --dry-run` prints the plan without builds or remote calls.

Subsequent deploys restart the existing user service. If unit-level settings in `smdctl.yml` change, update that service definition separately. The launcher reads the protected `moneybag.env` without rewriting it. Optional remote-owned mode-0600 `identity.env` contains only literal `MONEYBAG_ID_ENABLED`, `MONEYBAG_ID_ISSUER`, `MONEYBAG_ID_CLIENT_ID`, `MONEYBAG_ID_CLIENT_SECRET` and `MONEYBAG_ID_COOKIE_KEY` assignments; shell expressions are not evaluated. Symlinks, unsafe permissions and unrelated keys are refused. Deployment never uploads either environment file.

### Persistent authentication smoke

Provision an existing dedicated `eval-` account through the normal invitation flow before deployment. Store its password in a local mode-0600 regular file. Set `MONEYBAG_SMOKE_PASSWORD_FILE`; `MONEYBAG_SMOKE_EMAIL` defaults to `eval-smoke@microapps.space`. Optional `MONEYBAG_SMOKE_ORIGIN` and `MONEYBAG_SMOKE_ID_ORIGIN` select the app and ID origins (HTTPS or exact loopback HTTP). Install the locked e2e dependencies and Playwright Chromium; `CHROMIUM` selects an existing executable.

`make smoke` detects the existing login form, uses email/password through the actual flow, verifies the account and authenticated bootstrap/settings/saved-view reads, checks mode-specific account controls and signs out through the UI. It then clears only its own app session and ID device. It creates no account and writes no financial records. `MONEYBAG_SMOKE_VERSION` optionally requires an exact health version; deployment sets it to the release being activated. Errors withhold credentials and token-bearing browser details. `make smoke-check rollout-check` verifies configuration/file guards, the literal sidecar loader, preflight ordering and health/smoke rollback using owned temporary files and command shims.

```sh
ssh oracle-vm 'bash -lc "smdctl status moneybag"'
ssh oracle-vm 'curl -fsS http://127.0.0.1:6459/healthz'
```

The service restarts on failure and after login/boot through the VM's existing user linger setup. It listens only on loopback. Point Caddy at `127.0.0.1:6459`, then visit `/setup` with the private setup token from the environment file. Remove that token after creating the owner. No real finance data is preloaded; the user will populate it through Grokbot.

## Tailnet route

People on the tailnet can skip Cloudflare, which saves about 25 to 50 ms per request. The public domain stays the address for agents, OAuth and share links.

```sh
scp deploy/tailscale-serve.sh oracle-vm:/tmp/
ssh oracle-vm 'bash /tmp/tailscale-serve.sh'
```

The script is safe to rerun. It runs `tailscale serve --bg --https=8443 http://127.0.0.1:6459`, shows the result and prints the `MONEYBAG_TAILNET_URL` line to add to `moneybag.env`. Restart the service after adding it; until then the tailnet origin can load pages but cannot sign in or post, because the origin checks reject it. The session cookie is host-only, so each origin signs in separately. To remove the route, run `tailscale serve --https=8443 off`.

## Maintenance

Inside the sole API process, daily UTC jobs refresh ECB prices at 17:00 and check journal/audit invariants and balance assertions at 03:00. Snapshot sharing refreshes hourly according to each grant's policy. An hourly SQL sweep removes expired sessions, invitations, OAuth codes/tokens and admin confirmations; refresh-valid tokens survive access-token expiry. Failed maintenance is logged with the job and user ID.

For an operator integrity check, run the binary with the same environment and the `verify` argument. It reads each user's ledger and validates its balanced postings and audit intent/outcome hashes. It does not repair an interrupted workflow automatically.

`reconcile_status` only reads. It reports the result of the nightly assertion check; `reconcile_verify` runs that check on demand after backdated entries, reversals or import undos.


## Migration backups and rollback

The dedicated role uses a five-connection application pool. Embedded forward migrations apply at startup; never edit an already applied migration. Keep database credentials private and preserve the feed encryption key. The off-VM `moneybag-pg-backup.timer` runs nightly at 02:30 UTC and streams a custom-format `pg_dump` over SSH to the local private backup directory; the VM is not the only backup location. Test restores in a throwaway PostgreSQL 18 database.

At cutover, stop the API writer, retain a fresh MongoDB backup and explicitly archive `system.profile`, run the copy/verify command, preserve an off-VM PostgreSQL dump and deploy through the existing script. A mismatch restarts the previous MongoDB release before investigating. Keep MongoDB databases, old credentials and the previous binary for 30 days. Health or smoke failure restores the previous symlink and restarts that release. Do not remove the shared MongoDB server or other applications' databases. The migration ledger and [copy runbook](../backend/cmd/migrate-mongo/README.md) record the exact evidence and remaining prerequisites.
