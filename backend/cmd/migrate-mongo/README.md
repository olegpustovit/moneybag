# One-time MongoDB → PostgreSQL copy

Build from `backend/` with `go build -o bin/migrate-mongo ./cmd/migrate-mongo`.
Set `MONEYBAG_MIGRATE_MONGO_URI` and `MONEYBAG_DATABASE_URL` privately rather than putting production credentials in command arguments.

```
migrate-mongo copy --mongo-db moneybag
migrate-mongo verify --mongo-db moneybag
```

Stop every source writer before the final copy and keep them stopped through verification and cutover. The command reads source collections sequentially; it does not create a consistent live MongoDB snapshot. Rehearsals use a restored backup with no application writers.

The target must use C collation. Embedded migrations run first; copying then requires all application tables to be empty. `copy --truncate` explicitly replaces target application data after source shape and uniqueness preflight. It leaves Goose metadata intact. A failed copy can leave completed batches; repeat with `--truncate` while writers remain stopped. Each dynamic-row batch and its derived unique keys commit together.

All 44 known application collections have explicit typed legacy adapters. Unknown collections or top-level fields, duplicate target keys, invalid decimals and counters behind their journal/audit sequences fail the migration. No automatic deduplication or money conversion through floating point is performed. The new `row_keys` table and Decimal-valued cell metadata are derived from source row metadata/types.

Verification compares collection counts and canonical document fingerprints, every derived row-key tuple, sequence counters and exact posting totals grouped by owner/account/currency. It reads both stores and changes neither application's data. Keep copy and verification logs private.

If the source contains the mutable operational `system.profile` collection, both commands require `--profile-archive /private/path/profile.bson`. Copy preserves its raw BSON separately in a new mode-0600 file, reports its count/hash and refuses overwrite. Verify validates and hashes that archive; it does not equate a changing profiler collection with application state. Preserve the archive alongside the source backup. This is the sole explicit non-application collection exception.

Tests use isolated databases on dedicated disposable servers:

```
MONEYBAG_MIGRATE_TEST_MONGO_URI=mongodb://127.0.0.1:27098 \
MONEYBAG_TEST_DATABASE_URL=postgres://postgres:test@127.0.0.1:55432/postgres \
go test ./cmd/migrate-mongo
```

The MongoDB driver is confined to this command. Remove the command and driver after the approved 30-day rollback window; the running application uses PostgreSQL only.
