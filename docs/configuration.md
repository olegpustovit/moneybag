# Configuration

Settings are read once at startup: typed defaults, optional Vault values, then explicit `MONEYBAG_*` environment overrides. Explicit empty values override Vault. Configuration errors name keys without printing secrets.

| Variable | Default / meaning |
|---|---|
| `MONEYBAG_LISTEN` | `127.0.0.1:6459` |
| `MONEYBAG_PUBLIC_URL` | `https://moneybag.microapps.space`; HTTPS origin, HTTP permitted on loopback for development |
| `MONEYBAG_MONGO_URI` | `mongodb://127.0.0.1:27017/moneybag` |
| `MONEYBAG_MONGO_DB` | `moneybag`; one shared application database |
| `MONEYBAG_LOG_FORMAT` | `text` or `json` |
| `MONEYBAG_SETUP_TOKEN` | Empty disables first-owner setup; remove after owner creation |
| `MONEYBAG_SECRET_KEY` | Stable base64 encoding of exactly 32 bytes; encrypts feed credentials |
| `MONEYBAG_TAILNET_URL` | Empty; optional second browser origin such as `https://host.tailnet.ts.net:8443`. Sign-in and CSRF checks accept it. OAuth, connectors and share links always use `MONEYBAG_PUBLIC_URL` |
| `MONEYBAG_CHROMIUM_BIN` | Chromium executable for `dashboard_render`, which opens the app on its own loopback listener; blank returns validation without an image |
| `MONEYBAG_TOOLSETS` | Empty enables all; comma-separated `account,ledger,rates,budget,tables,model,dashboard,audit,classify,import,reconcile` |

`bootstrap` is an optional noninteractive first-owner command using `MONEYBAG_OWNER_EMAIL`, `MONEYBAG_OWNER_NAME` and `MONEYBAG_OWNER_PASSWORD`. It refuses a second owner bootstrap. Never put passwords in command arguments or commit an environment file.

## Vault

Disabled by default. Set `MONEYBAG_VAULT_ENABLED=true`, `MONEYBAG_VAULT_ADDR`, `MONEYBAG_VAULT_PATH` and optional `MONEYBAG_VAULT_MOUNT` (default `kv`) / `MONEYBAG_VAULT_NAMESPACE`.

The default auth method is AppRole, using `MONEYBAG_VAULT_ROLE_ID` and `MONEYBAG_VAULT_SECRET_ID`. For an intentional token integration, set `MONEYBAG_VAULT_AUTH_METHOD=token` and `MONEYBAG_VAULT_TOKEN`. TLS verification remains enabled. A configured Vault failure aborts startup. AppRole tokens are revoked after the one-time static configuration read; no runtime lease is retained.

KV v2 string keys are `listen`, `public_url`, `mongo_uri`, `mongo_db`, `log_format`, `setup_token`, `secret_key`, `chromium_bin` and `toolsets`. Unknown keys are rejected. The implementation uses the [official Vault client and AppRole API](https://developer.hashicorp.com/vault/docs/auth/approle).

## Clients and feeds

OAuth access tokens last 15 minutes; refresh tokens rotate with a 30-day lifetime. Use exact registered callback URIs and the endpoint as the resource audience. A full-endpoint token does not authorize the read-only endpoint.

Feed credentials are user-owned and AES-GCM encrypted using the stable secret key. Settings accepts a public HTTPS statement endpoint and optional bearer token. `feeds_sync` stages its output; it does not commit money automatically. Wise and generic feeds return CSV; Plaid feeds return a complete single-account transactions JSON export. Pending Plaid transactions are excluded. For ambiguous numeric CSV dates, pass `mapping.date_format` as `dmy` or `mdy`; `iso` requires ISO dates. Ambiguous slash dates are rejected when no ordering is supplied. Plaid outflows use its documented positive sign and are converted to negative bank postings ([Plaid API](https://plaid.com/docs/api/products/transactions/)).

The ECB daily reference feed is built in. Manual historical quotes accept currencies, metals and security commodities via `rate_set`. No bank account, paid market-data subscription or external credential is bundled.
