# PostgreSQL migration parity

Run locally or through SSH; these scripts do not dispatch GitHub workflows. All output contains private financial data: use `umask 077`, keep it outside the repository and never publish it.

## Read capture

```
e2e/parity.sh --base http://127.0.0.1:6498 \
  --session /private/session.json --out /private/out-mongo --as-of 2026-11-14
# Stop Mongo harness; start PostgreSQL harness on the same origin, then:
e2e/parity.sh --base http://127.0.0.1:6498 \
  --session /private/session.json --out /private/out-pg --as-of 2026-11-14
diff -r /private/out-mongo /private/out-pg
```

`session.json` contains `cookie` (raw browser session cookie) and `csrf`. Use the same valid session in both restored copies. A synthetic session can be inserted into the restored MongoDB copy before copying: `_id` is SHA-256 hex of the raw cookie, with `user_id`, `csrf` and a future `expires_at`. This is a private test fixture, never a change to production credentials.

Use the same frozen backup, origin, embedded UI, version and calendar day. Scratch harness builds replace only the startup `startJobs(...)` call with an empty cleanup function, preventing rate/maintenance jobs from changing data during captures. The shipped application keeps its jobs. The original dev baseline also receives the same three deterministic ordering fixes as PostgreSQL: CEL overloads with identical signatures sort by member status, dashboard-summary source names sort alphabetically, and shock results sort by pot. Unmodified Mongo repeats/restarts demonstrated unstable order for these map-derived lists; no persistence logic changes in that baseline.

The fixed request inventory expands across discovered accounts, entries, tables, scenarios, metrics, views, imports and dashboards. Every advertised read-only MCP tool must be exercised or the script fails. No mutating tool is called. API errors are retained, including existing dashboard validation errors and joins on tables without entry columns. An intentionally missing discrepancy exercises the not-found contract when no open discrepancy exists. Actual PNG bytes are compared too. Existing shared links capture their browser-rendered DOM with the installed Playwright dependency (`parity-shared.mjs`); its request guard blocks usage beacons and every mutating request. Set `MONEYBAG_CHROMIUM_BIN` to the same real Chromium in both runs. An empty shared-link inventory is recorded as empty, not presented as successful shared-page coverage.

The only read exclusions are dashboard-render `timing_ms`/`render_ms` and dashboard-view `body.as_of` (the cache build time, not a valuation date). JSON object keys and MCP's duplicate JSON text are canonicalized. Array order, data, errors and versions remain exact. `tools/list` records its read-only registry; datastore wording in mutating-tool descriptions is outside this capture.

For the production baseline over SSH, run the capture on the VM with `--base http://127.0.0.1:6459` and `MONEYBAG_PARITY_ORIGIN=https://moneybag.microapps.space`. This keeps transport on loopback while sending the configured public origin for CSRF checks. The shared-DOM driver applies that origin only to allowed read-only MCP requests; all mutating requests remain blocked.

## Write capture on separate clones

Create fresh write clones of both read datasets. Keep the read datasets untouched. The write script requires an explicit `--restored-copy` and a loopback origin:

```
python3 e2e/parity-writes.py --restored-copy --base http://127.0.0.1:6499 \
  --session /private/session.json --out /private/writes-mongo --as-of 2026-11-14
# Run PostgreSQL write harness on the same origin and fresh matching clone:
python3 e2e/parity-writes.py --restored-copy --base http://127.0.0.1:6499 \
  --session /private/session.json --out /private/writes-pg --as-of 2026-11-14
```

Assertions cover draft creation/edit/cancellation via undo, zero saved rows on a duplicate-key batch, exact Decimal cell round-trip, one posted import despite repeated rows/retry, and OAuth registration/consent/PKCE exchange/authenticated MCP/refresh/revocation. Generated IDs and credentials have checked formats and consistent aliases; new creation/issuance/audit timestamps and audit hashes derived from them are the explicit volatile fields. Sequences, amounts, statuses, payloads, versions, errors and reference relationships remain comparable.

Before accepting normalized audit hashes, verify the complete source and target chains, including all pre-existing records, using the existing Go digest implementations:

```
cd backend
# Set private values without exposing credentials in arguments:
# MONEYBAG_PARITY_AUDIT_MONGO_URI, MONEYBAG_PARITY_AUDIT_MONGO_DB,
# MONEYBAG_PARITY_AUDIT_PG_URL identify only the disposable write clones.
go test -v ./cmd/migrate-mongo -run '^TestParityAuditChains$'
cd ..
diff -r /private/writes-mongo /private/writes-pg
python3 -m unittest discover -s e2e -p parity_test.py
```

Both capture commands, the audit-chain test and both diffs must pass. A directory left by a failed capture is incomplete evidence; use a fresh clone/output directory for a retry.
