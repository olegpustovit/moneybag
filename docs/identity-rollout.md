# Identity rollout preparation

This branch prepares proposal T37 for Moneybag. It adds account linking, invite registration and browser-session isolation; no public environment flag, SDK handler, UI action, production registration or production deployment is enabled yet. Moneybag remains PostgreSQL-only, and its existing human/agent principals and financial ownership stay authoritative.

## Persistence and account rules

Forward migration 0002 adds `user_identities`, with one unique subject per local user and a foreign key to the unchanged users table. Keeping the mapping separate preserves the old binary's strict SELECT * user decoder. Tests also run the old migration provider with only migration 0001 against the new schema and verify legacy user reads still work. No applied migration is edited and no password/finance table is removed.

`Identity.LinkIdentity` must receive only SDK-verified subject/email claims. It resolves subject first, then links an existing active user by exact normalized email inside a PostgreSQL transaction. Concurrent first links converge on one mapping. A changed verified email cannot move an existing subject onto another user's financial records; another subject cannot take an already linked user. Unknown and disabled users are denied. User IDs, profile/admin flags, password bytes, precise financial JSON and OAuth agent records are unchanged.

`Identity.RegisterIdentity` requires a live local Moneybag invitation bound to the verified email, including on an empty server. Invitation consumption, optional user creation and subject binding commit atomically. Newly created users are non-admins and have no local password credential. Existing users retain their profiles/credentials. The returned Created boolean lets the eventual transport perform first-time ledger/model initialization. Missing, expired, used, foreign, disabled and conflicting invitations are rejected. Database failures roll back the account and invitation together, allowing retry; competing invitation/subject/email registrations cannot leave extra accounts or consume a losing invitation.

## Validation

Tests use Moneybag's pgtest helper against an owned disposable PostgreSQL 18 cluster; its template and cloned databases disappear with that cluster. They cover 16 concurrent links, competing registrations, injected persistence failure/retry, no uninvited/admin/password creation, unchanged account/credential/finance/agent snapshots, local password rollback, disabled session revocation and legacy migration/query compatibility. The new identity package and PostgreSQL tests pass against the real database, including identity race tests and the complete backend test suite/vet. Mapping primary/unique/foreign-key/length constraints, private subject serialization and deletion cascade are also covered. Go 1.26.11 is the required toolchain.

## Remaining work

- Add default-off SDK configuration and a reproducible shared SDK pin once its repository destination is confirmed.
- Wire verified login/callback/logout, state-bound local invitation intent and recoverable first-time ledger/model initialization without changing existing users' data. Register the configured public/Tailnet callback origins and allow ID mutations only for the exact configured callback origins.
- Keep the existing visual design while adding Continue with ID and closing local credential/setup/signup paths only when enabled.
- Add persistent eval smoke/rollback qualification against the actual Identity provider.
- Preserve all protected production configuration, obtain the per-app production confirmation, and keep password rollback available for the proposal's week-long fallback period.

GitHub Actions remain manual-only; no workflow is dispatched.


## Browser-session isolation

Internal `handlers.Config.IdentityMode` selects `__Host-moneybag_session`, always Secure/HttpOnly/SameSite=Lax with root path and no Domain. Stored ID session hashes have a separate purpose from legacy tokens, so neither token can be reused across modes even by renaming its cookie. Existing rows, seven-day lifetime, expiry sweeping and flag-off login remain valid. ID session creation and reads use the current local user; disabling revokes both modes through the existing user revocation path.

All browser-cookie readers/writers now share the selected cookie name, including normal bootstrap precedence over render captures. ID mode requires the exact configured PublicURL Origin plus CSRF for browser mutations; legacy Tailnet/optional-Origin behavior is unchanged. Public ID login will use the registered public origin; the alternative Tailnet origin is not an ID cookie-mutation origin. Local password/setup/register/change-password endpoints are closed in internal ID mode. This mode is not exposed through application configuration yet, so no incomplete sign-in UI can be enabled in production.

Mounted PostgreSQL/HTTP tests verify session reads, renamed-cookie rejection, current admin authority, disabled users, local-auth closure, CSRF/origin logout, OAuth consent/code/PKCE exchange and unchanged agent bearer reads/scopes. An invalid browser cookie cannot interfere with bearer authentication, browser logout does not revoke the agent grant, and user disable still blocks it. The complete preview-route read-only matrix now runs in both modes, including normal-session precedence and refusal of every mutating route without data changes. Focused identity/handler/integration race tests pass (integration 3.76 s). SDK login, verified invitation handoff and recoverable first-time ledger/model initialization remain the next transport work; defaults must initialize only new ID-created accounts, never overwrite existing user data, and remain retryable after a partial initialization failure.

Final session-slice checks: the complete backend suite passes on an owned PostgreSQL 18 cluster (integration package 21.68 s), focused identity/handler/integration races pass, and full vet/API build pass. Production remains unchanged.
