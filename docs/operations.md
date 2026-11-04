# Operations

Production uses the existing standalone MongoDB on oracle-vm, one shared `moneybag` database and one API writer. Caddy and the MongoDB service are managed separately. Moneybag does not change their configuration.

## Test

`make test-integration` runs the complete Go suite against one disposable standalone MongoDB. It accepts only `moneybag_test_*` databases and removes the container afterward. Receipt, reference, key, batch and undo tests run on this same topology; no replica-set test or operator conversion is needed.

## Table writes on standalone MongoDB

The existing schema and row documents remain the storage model. There is no replacement storage format, database migration, transaction session, replica set, or topology change. The API serializes writes in the existing single process.

`db_batch` prepares the complete proposed row state and validates cells, versions, references, keys, checks and ledger sums before writing. A validation failure saves no rows. Dry runs create no rows or audit events. After validation, rows are saved in deterministic order with one audit event. A successful batch can be undone with one call, subject to row-version checks.

**Multi-row writes are not atomic.** A database failure or interruption can leave earlier rows saved. Recovery errors report confirmed writes and, when available, the audit event id. The failing write may also have reached MongoDB if its acknowledgement was lost. Reload affected rows, inspect the audit payload, run `consistency_check`, and complete or correct the intended data. Do not blindly retry with stale versions. No compensating writes or automatic rollback are performed. Receipt/table writes never modify the ledger's entries or postings.

`db_update_cells`, `db_delete_rows`, schema migrations and undo follow the same standalone constraints. Key metadata is replaced with each row; it is never cleared in advance. MongoDB's unique key index remains active throughout. A swap or transfer of an occupied unique key is refused before writes; use a distinct temporary key in a separate update when rearranging keys.

`consistency_check` reports broken references as `table_references`, and invalid cells, key metadata, checks and receipt ledger sums as `table_constraints`. Scans have explicit budgets and fail rather than silently truncating. Correct incomplete rows through the ordinary tools; the financial ledger remains the source of truth.

### Schemas and templates

Columns support exact `decimal` values, offset-preserving RFC 3339 `datetime`, `entry`, `account`, `ref`, string `list`, and bounded object `json`, plus descriptions/required/enum/pattern metadata. Composite keys use the user/table/key-hash MongoDB unique index. Row checks may block writes or return warnings. Ledger constraints compare exact row amounts with the configured account prefixes in one commodity; reversed entries are exempt. Removing all rows leaves an entry unexplained.

`db_alter` validates active rows before accepting new constraints. Constraint changes recompute derived values; ordinary formula additions retain the existing deferred behavior. A storage failure during a multi-row migration can leave earlier derived values saved, so inspect consistency and retry after resolving the cause. `keys:[]`, `checks:[]` and `ledger:{}` clear their respective guarantees. Schema undo preserves compatibility with older audit events.

`db_templates` returns ordinary schemas and an agent playbook. `db_create {template:"receipts"}` creates merchants, stores, products, receipts and receipt_items. Repeating it reuses identical active schemas and creates missing tables, allowing recovery after partial creation. Conflicting or archived schemas are never overwritten. Its event records only tables newly created by that call; undo archives those tables in reverse reference order. Creation and multi-table archive can partially succeed on storage failures.

### Joined reads

Raw `db_query` cells retain their original behavior. `join_entry:true` adds entry metadata; orphan entries yield `entry:null`. CEL sees the joined object under `row.entry`, while original-id equality/membership still work. A custom entry-column alias uses the stored column's index; an existing conflicting `entry` column causes an explicit error instead of hiding data.

`expand` inlines one level of owner-scoped refs. Missing/archived targets become null. `_row_id` identifies the target (also `id` when that column does not already exist). Historical EUR valuation uses exact decimals; missing quotes return a null value with a valuation error, while storage failures fail the query.

Literal equality/membership on indexed key/ref/entry/account columns runs in MongoDB before CEL filtering. Row-id pagination returns `next_cursor` and explicit `truncated`; custom sorting does not accept a cursor. Analytic `table:` sources join automatically, drop ineffective/orphan entries, and read complete bounded sources beyond 5,000 rows or return an error. Joined output also has a byte budget.

`entry_get.explained_by` lists table/row ids; positive and negative `entries_list.explained_by` filters run before ledger pagination.

## Deploy

Create `/home/snowbear/ci/moneybag/moneybag.env` with mode `0600` using `deploy/env.example`. Preserve its secret key across releases. Set `MONEYBAG_CHROMIUM_BIN` to a working Chromium installation; the VM already has `/opt/google/chrome/chrome`.

Run `deploy/deploy.sh` from a committed checkout. It builds an ARM64 binary, copies a versioned release, atomically replaces `current`, installs the smdctl user service and checks the running release's health/version. A failed upgrade restores the previous symlink. Old releases remain available for manual rollback. Subsequent deploys restart the existing user service. If unit-level settings in `smdctl.yml` change, update that service definition separately; runtime settings are read from the private environment file at restart.

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

Inside the sole API process, daily UTC jobs refresh ECB prices at 17:00 and check journal/audit invariants and balance assertions at 03:00. Snapshot sharing refreshes hourly according to each grant's policy. Failed maintenance is logged with the job and user ID.

For an operator integrity check, run the binary with the same environment and the `verify` argument. It reads each user's ledger and validates its balanced postings and audit intent/outcome hashes. It does not repair an interrupted workflow automatically.

`reconcile_status` only reads. It reports the result of the nightly assertion check; `reconcile_verify` runs that check on demand after backdated entries, reversals or import undos.
