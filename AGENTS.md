# AGENTS.md

## Persistence: PostgreSQL

- Moneybag stores all data in the existing PostgreSQL 18 cluster on the host, reached through `MONEYBAG_DATABASE_URL` with pgx/pgxpool. Do not add MongoDB code or dependencies to the application. The isolated `cmd/migrate-mongo` command is the sole temporary exception during the 30-day rollback window.
- Schema changes are forward-only goose migrations embedded from `backend/internal/infrastructure/postgres/migrations` and applied at startup. Never edit an applied migration; add a new one.
- SQL lives as constants beside the code that runs it, inside the owning component. Pass values with `pgx.NamedArgs`; never build SQL from input. No ORM or query builder.
- Money is `numeric` in columns and a decimal string inside `jsonb`. Never pass an amount through `float64`.
- Write timestamps truncated to milliseconds. Use `NOT NULL` with a default for every non-pointer field.
- A transaction stays inside one component. Do not change PostgreSQL server configuration (`postgresql.conf`, extensions, `pg_hba.conf`) for an application feature; the cluster is shared with other apps.

## Verification

- Run checks locally or over SSH. Do not start GitHub Actions runs. Automatic push and pull-request CI is disabled; preserve that choice.

## Deploy (Aquifer)

Production runs on `oracle-vm` as the Aquifer app `moneybag`. Use the `aquifer-deploy` skill:
build locally, `scp` the artifact to `oracle-vm:/tmp/`, then `ssh oracle-vm aq deploy moneybag /tmp/<file>`.
Env vars: `printf %s "$VALUE" | ssh oracle-vm aq secret set moneybag NAME`. Logs, status and rollback:
`ssh oracle-vm aq logs|status|rollback moneybag`. Writable state lives in `/home/apps/data/`.
smdctl, its units and `/home/snowbear/ci/...` are retired: do not deploy there.
