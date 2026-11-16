# Configuration

Settings are read once at startup: typed defaults, optional Vault values, then explicit `MONEYBAG_*` environment overrides. Explicit empty values override Vault. Configuration errors name keys without printing secrets.

| Variable | Default / meaning |
|---|---|
| `MONEYBAG_LISTEN` | `127.0.0.1:6459` |
| `MONEYBAG_PUBLIC_URL` | `https://moneybag.microapps.space`; HTTPS origin, HTTP permitted on loopback for development |
| `MONEYBAG_DATABASE_URL` | Required PostgreSQL URL for the dedicated `moneybag` role/database on localhost; no default |
| `MONEYBAG_LOG_FORMAT` | `text` or `json` |
| `MONEYBAG_SETUP_TOKEN` | Empty disables first-owner setup; remove after owner creation |
| `MONEYBAG_SECRET_KEY` | Stable base64 encoding of exactly 32 bytes; encrypts feed credentials |
| `MONEYBAG_TAILNET_URL` | Empty; optional second browser origin such as `https://host.tailnet.ts.net:8443`. Sign-in and CSRF checks accept it. OAuth, connectors and share links always use `MONEYBAG_PUBLIC_URL` |
| `MONEYBAG_CHROMIUM_BIN` | Chromium executable for `dashboard_render`, which opens the app on its own loopback listener; blank returns validation without an image |
| `MONEYBAG_TOOLSETS` | Empty enables all; comma-separated `account,ledger,rates,budget,tables,model,dashboard,audit,classify,import,reconcile` |
| `MONEYBAG_ID_ENABLED` | `false`; opt-in shared Identity SDK (draft rollout; UI/provider qualification still pending) |
| `MONEYBAG_ID_ISSUER` | `https://id.microapps.space`; HTTPS or exact loopback HTTP, no credentials/query/fragment |
| `MONEYBAG_ID_CLIENT_ID` | Required when ID is enabled |
| `MONEYBAG_ID_CLIENT_SECRET` | Optional confidential-client secret, never printed |
| `MONEYBAG_ID_COOKIE_KEY` | Independent nonzero 32-byte base64 flow key, required when enabled; cannot equal the feed secret key |

`bootstrap` is an optional noninteractive first-owner command using `MONEYBAG_OWNER_EMAIL`, `MONEYBAG_OWNER_NAME` and `MONEYBAG_OWNER_PASSWORD`. It refuses a second owner bootstrap. Never put passwords in command arguments or commit an environment file.

## Vault

Disabled by default. Set `MONEYBAG_VAULT_ENABLED=true`, `MONEYBAG_VAULT_ADDR`, `MONEYBAG_VAULT_PATH` and optional `MONEYBAG_VAULT_MOUNT` (default `kv`) / `MONEYBAG_VAULT_NAMESPACE`.

The default auth method is AppRole, using `MONEYBAG_VAULT_ROLE_ID` and `MONEYBAG_VAULT_SECRET_ID`. For an intentional token integration, set `MONEYBAG_VAULT_AUTH_METHOD=token` and `MONEYBAG_VAULT_TOKEN`. TLS verification remains enabled. A configured Vault failure aborts startup. AppRole tokens are revoked after the one-time static configuration read; no runtime lease is retained.

KV v2 string keys are `listen`, `public_url`, `database_url`, `log_format`, `setup_token`, `secret_key`, `chromium_bin`, `toolsets`, `id_enabled`, `id_issuer`, `id_client_id`, `id_client_secret` and `id_cookie_key`. Unknown keys are rejected. The implementation uses the [official Vault client and AppRole API](https://developer.hashicorp.com/vault/docs/auth/approle).

## Clients and feeds

OAuth access tokens last 15 minutes; refresh tokens rotate with a 30-day lifetime. Use exact registered callback URIs and the endpoint as the resource audience. A full-endpoint token does not authorize the read-only endpoint.

Feed credentials are user-owned and AES-GCM encrypted using the stable secret key. Settings accepts a public HTTPS statement endpoint and optional bearer token. `feeds_sync` stages its output; it does not commit money automatically. Wise and generic feeds return CSV; Plaid feeds return a complete single-account transactions JSON export. Pending Plaid transactions are excluded. For ambiguous numeric CSV dates, pass `mapping.date_format` as `dmy` or `mdy`; `iso` requires ISO dates. Ambiguous slash dates are rejected when no ordering is supplied. Plaid outflows use its documented positive sign and are converted to negative bank postings ([Plaid API](https://plaid.com/docs/api/products/transactions/)).

The ECB daily reference feed is built in. Manual historical quotes accept currencies, metals and security commodities via `rate_set`. No bank account, paid market-data subscription or external credential is bundled.


The API ignores legacy `MONEYBAG_MONGO_*` environment entries. Preserve them during the 30-day rollback window because the previous MongoDB release needs them. The isolated copy command uses `MONEYBAG_MIGRATE_MONGO_URI`; see [its runbook](../backend/cmd/migrate-mongo/README.md). Keep both database URLs in private mode-0600 environment files.


## Identity callbacks

Enabled startup validates configuration and discovers the issuer before listening. Register each exact callback: `MONEYBAG_PUBLIC_URL` plus `/api/auth/id/callback`, and the optional `MONEYBAG_TAILNET_URL` plus that same path. Callback origins must be canonical (lowercase hosts, no default :443/:80 port, path, query or fragment), with distinct hosts/ports. The app selects only those hosts; unknown Host values cannot choose a callback. Each SDK client binds its flow cookie to its own callback origin. ID cookie mutations accept only the exact configured callback origins; logout additionally uses the selected SDK client's exact origin. Legacy mode retains its existing origin behavior.

The backend mounts GET `/api/auth/id/login`, GET `/api/auth/id/callback` and POST `/api/auth/id/logout`. Local invitation intent is signed, HttpOnly and bound to SDK state. Existing users retain profiles and credentials; newly invited ID users stay pending until their default ledger/model setup succeeds. A failed partial setup can finish on a fresh normal ID sign-in without reusing the consumed invitation or resetting existing assumptions.

UI, real-provider and persistent eval smokes are qualified locally. Keep ID disabled until the published SDK pin and per-app production gate are complete. The draft uses a local sibling SDK replacement until the shared module has a reproducible published pin. No secrets belong in the repository or command arguments.
