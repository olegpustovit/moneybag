# Identity rollout preparation

## Production cutover — 2026-11-16

The owner authorized the production rollout for Checklists, Nudge and Moneybag. ID login is enabled at `https://id.microapps.space`; local password sign-in and account creation are disabled. The existing owner account uses `oleg@olegpustovit.com` and retains its original application ID and data. Local password hashes remain inactive for rollback.

The shared Go SDK is pinned to Identity commit `9c0a96699ea8a766ced748691a127601ae128288` in `github.com/olegpustovit/identity`, without a local module replacement. The repository is private: developers need GitHub access, `GOPRIVATE=github.com/olegpustovit/identity`, and authenticated Git transport.

The remote-owned `identity.env` is mode 0600 and holds the enabled flag, exact client registration and independent flow key. Protected application environment files were preserved. Database backups taken before cutover are under `~/backups/id-cutover-20261116/` on Oracle VM. The persistent eval account is `eval-smoke@microapps.space`; Nudge uses only its `eval-smoke` workspace. Deployment browser smokes passed through the real provider, authenticated reads and own-session cleanup. Checklists also passed priority and routine-skip feature smokes. Sign-in screens were checked at 390 and 1440 px in light/dark themes.

For local deployment smokes, set the app's `*_SMOKE_PASSWORD_FILE` to a private credential file. Set `CHROMIUM_PATH` for Checklists and `CHROMIUM` for Nudge when the scripts' default browser is unavailable. Nudge tests run with host architecture even when the release artifact targets ARM64. Rollback preserves the sidecar; switching back to local authentication requires explicitly disabling the ID flag. The historical preparation notes below describe the earlier default-off state; the rollout and SDK-pinning prerequisites are now satisfied.


This branch prepares proposal T37 for Moneybag. It adds account linking, invite registration, browser-session isolation and default-off SDK configuration/handlers. SDK publication/pinning and the production human gate remain pending, so keep the flag disabled; production is unchanged. Moneybag remains PostgreSQL-only, and its existing human/agent principals and financial ownership stay authoritative.

## Persistence and account rules

Forward migration 0002 adds `user_identities`, with one unique subject per local user and a foreign key to the unchanged users table. Keeping the mapping separate preserves the old binary's strict SELECT * user decoder. Tests also run the old migration provider with only migration 0001 against the new schema and verify legacy user reads still work. No applied migration is edited and no password/finance table is removed.

`Identity.LinkIdentity` must receive only SDK-verified subject/email claims. It resolves subject first, then links an existing active user by exact normalized email inside a PostgreSQL transaction. Concurrent first links converge on one mapping. A changed verified email cannot move an existing subject onto another user's financial records; another subject cannot take an already linked user. Unknown and disabled users are denied. User IDs, profile/admin flags, password bytes, precise financial JSON and OAuth agent records are unchanged.

`Identity.RegisterIdentity` requires a live local Moneybag invitation bound to the verified email, including on an empty server. Invitation consumption, optional user creation and subject binding commit atomically. Newly created users are non-admins and have no local password credential. Existing users retain their profiles/credentials. Created reports account creation; migration 0003 persists pending first-time initialization so the SDK can retry safely after a partial failure. Missing, expired, used, foreign, disabled and conflicting invitations are rejected. Database failures roll back the account and invitation together, allowing retry; competing invitation/subject/email registrations cannot leave extra accounts or consume a losing invitation.

## Validation

Tests use Moneybag's pgtest helper against an owned disposable PostgreSQL 18 cluster; its template and cloned databases disappear with that cluster. They cover 16 concurrent links, competing registrations, injected persistence failure/retry, no uninvited/admin/password creation, unchanged account/credential/finance/agent snapshots, local password rollback, disabled session revocation and legacy migration/query compatibility. The new identity package and PostgreSQL tests pass against the real database, including identity race tests and the complete backend test suite/vet. Mapping primary/unique/foreign-key/length constraints, private subject serialization and deletion cascade are also covered. Go 1.26.11 is the required toolchain.

## Remaining work

- Pin the shared SDK reproducibly once its repository destination is confirmed; the draft currently uses the sibling Identity checkout.
- Preserve all protected production configuration, obtain the per-app production confirmation, and keep password rollback available for the proposal's week-long fallback period.

GitHub Actions remain manual-only; no workflow is dispatched.


## Browser-session isolation

Internal `handlers.Config.IdentityMode` selects `__Host-moneybag_session`, always Secure/HttpOnly/SameSite=Lax with root path and no Domain. Stored ID session hashes have a separate purpose from legacy tokens, so neither token can be reused across modes even by renaming its cookie. Existing rows, seven-day lifetime, expiry sweeping and flag-off login remain valid. ID session creation and reads use the current local user; disabling revokes both modes through the existing user revocation path.

All browser-cookie readers/writers now share the selected cookie name, including normal bootstrap precedence over render captures. Session-only internal mode requires exact PublicURL Origin plus CSRF; configured SDK mode requires one of its exact callback origins plus CSRF for browser mutations; legacy Tailnet/optional-Origin behavior is unchanged. Configured SDK mode supports the exact public and optional Tailnet callback origins; internal session-only tests default to the public origin. Local password/setup/register/change-password endpoints are closed in internal ID mode. The public flag is now default-off. Keep it disabled until the pending UI/provider checks and production gate are complete.

Mounted PostgreSQL/HTTP tests verify session reads, renamed-cookie rejection, current admin authority, disabled users, local-auth closure, CSRF/origin logout, OAuth consent/code/PKCE exchange and unchanged agent bearer reads/scopes. An invalid browser cookie cannot interfere with bearer authentication, browser logout does not revoke the agent grant, and user disable still blocks it. The complete preview-route read-only matrix now runs in both modes, including normal-session precedence and refusal of every mutating route without data changes. Focused identity/handler/integration race tests pass (integration 3.76 s). The SDK now supplies login, verified invitation handoff and recoverable first-time ledger/model initialization as described below; UI and actual-provider qualification remain open.

Final session-slice checks: the complete backend suite passes on an owned PostgreSQL 18 cluster (integration package 21.68 s), focused identity/handler/integration races pass, and full vet/API build pass. Production remains unchanged.


## Default-off shared SDK transport

`MONEYBAG_ID_ENABLED` configures the shared Go SDK through the same typed default/Vault/environment pipeline. Enabled startup validates independent nonzero flow-key material, canonical callback origins and issuer configuration, then performs discovery before starting background jobs or listening. Each configured public/Tailnet host selects its own SDK client and exact registered callback; unknown hosts are rejected. The SDK owns PKCE/state/nonce and signature/issuer/audience/expiry/email verification. Only exact configured callback origins pass ID-mode CSRF checks; SDK logout additionally requires its selected client's exact origin.

The login, callback and logout routes are `/api/auth/id/login`, `/api/auth/id/callback` and `/api/auth/id/logout`. Optional local invitation intent is authenticated in a private cookie bound to the SDK state. Invalid callbacks consume both invitation and SDK flow cookies. Successful logout revokes only the app session and clears the three app-local cookies; it does not sign out the global ID device.

New ID-created accounts are marked pending in the same registration transaction. The resolver initializes ledger/model defaults under the existing serialized workflow and marks completion only after success. ID session creation rejects pending accounts. After partial failure, a fresh normal sign-in resolves the already-bound account and retries pending initialization; it does not reuse the consumed invitation or reset existing assumptions. Existing linked accounts are never marked pending or initialized during login.

Tests use local discovery metadata plus an owned disposable PostgreSQL server. They verify callback/PKCE selection for both hosts, configuration/key errors, invitation binding/tampering, cleanup, origin/host rejection, session adapter identity and disabled-state checks. A forced failure after assumptions are written leaves initialization pending; eight concurrent retrying sign-ins complete it without resetting retained values. Existing profile/password/admin state and empty legacy models remain untouched. This is backend SDK qualification, not a successful provider/browser sign-in; UI and actual-provider checks remain open.

The shared module is temporarily replaced with `../../identity/idclient-go`; the PR remains draft until there is a reproducible published pin and the remaining app qualification passes. See configuration.md for the new keys and callback contract.

SDK login/callback routes share the existing 60-per-minute authentication budget; exhaustion never prevents app logout. Password rollback remains available only for users with pre-existing local credentials; new ID-only accounts deliberately have no local password. The API dependency scan reports zero reachable vulnerabilities, while retaining one uncalled imported-package and one module-only finding in its report.

Final backend SDK checks: complete backend tests on owned PostgreSQL 18 passed (integration package 21.92 s), focused identity/handler/integration race suites passed (3.92 s integration), vet/API build passed, and the API vulnerability scan reported zero reachable findings. No frontend or production behavior is qualified by these backend-only results.

## Sign-in and account UI

Configured ID mode renders the existing sign-in and invitation page with a native GET Continue with ID form, retaining the requested local destination and invitation token. Unknown callback hosts remain rejected and local credential POST routes remain closed. With the flag off, the original password forms and account settings remain available.

Both session and bootstrap responses expose only the enabled flag and configured issuer account URL for this UI. Settings hides local password changes in ID mode, links to ID account management and submits sign-out to the SDK endpoint after clearing the cached ledger. Legacy sign-out keeps its original route and spacing.

The complete owned-PostgreSQL backend suite passes (integration 23.77 s), focused SDK/page/metadata race checks pass, and API vet/build pass. UI tests pass (38 component cases plus existing utility checks); production build, type check and size budgets pass. Lint reports the existing unrelated no-useless-spread warning in ledger accounts. Twenty browser captures cover 1440/390 px and light/dark sign-in, invitation and settings in both modes, checking keyboard focus, hidden password controls, correct sign-out route and no overflow. Native auth renders use the real templates/built stylesheet; settings uses the built SPA with fixture API responses. These checks do not substitute for successful actual-provider sign-in or persistent eval deployment smokes, which remain pending.

## Actual Identity provider qualification

`e2e/identity-provider-scenario.mjs` runs inside the sibling Identity owned e2e runner. It claims only a disposable local owner, uses MCP OAuth to register the confidential Moneybag app and issue two invitations, and joins through captured email proof. An opt-in PostgreSQL integration fixture then serves the actual Fiber/SDK handlers and built SPA. Seven independent browser flows cover existing-account linking, Moneybag OAuth consent/PKCE, an invitation for an existing account, refusal of an unknown ID, creation through a new invitation, repeat sign-in for that new account, and flag-off password rollback for the original account.

The fixture verifies stable local IDs/name/permissions, unchanged password material, exact financial JSON/precision, model rows and existing agent grant. New ID users have no local password and complete their pending initialization. It checks replay refusal, closed local credential routes, origin guards, account settings, real UI logout, scoped agent reads/write refusal/revocation and app/global session separation. MCP confirms app sign-in/audit activity and zero remaining member ID devices. The seven-flow race run passes (9.41 s browser fixture, 10.47 s integration package). All resources are owned temporary fixtures; no production records are used.

This browser qualification exposed CSP redirect restrictions. ID mode now permits only the configured issuer origin in its form policy; the validated OAuth consent page additionally permits its selected registered callback origin (or registered native scheme). Normal pages retain their existing restrictions, and legacy sign-in policy remains unchanged. Unit tests cover HTTP/native callbacks, unsafe source rejection and mode-specific issuer policy.

To reproduce, provide an owned disposable `MONEYBAG_TEST_DATABASE_URL`, build the sibling Identity debug binary, install its e2e prerequisites, then run `make test-identity-provider`. The target builds the UI and uses the existing locked Playwright dependency. Set `CHROMIUM` if the app browser needs an explicit executable. Never point this test at a production database: its migrated template is intentionally retained until the disposable PostgreSQL server is removed.

Persistent eval deployment smoke conversion, a published SDK pin and production human gates remain pending. The flag remains disabled by default.

After the CSP fixes, the complete backend suite passes on owned PostgreSQL 18 (integration 21.96 s), full vet and API build pass, and focused SDK/page/consent-policy race checks pass. No UI source changed in this qualification slice; the prior production UI build and browser layout checks remain applicable.

## Persistent eval deployment gate

The deployment smoke now uses a pre-existing dedicated `eval-` account and private mode-0600 password file, with no account-creation fallback. It detects the live auth form, completes email/password sign-in, checks its account and bootstrap/settings/saved-view reads, verifies the correct account controls and signs out through the actual UI. Cleanup revokes only the smoke's app session and own ID device; the financial/model fixture and existing agent grant remain unchanged. Both ID-enabled and flag-off smokes pass against the same linked legacy eval account inside the actual-provider race fixture (11.28 s fixture / 12.35 s package); MCP confirms zero leftover member devices. Full backend tests pass (integration 24.23 s), full vet/API build pass, and the stripped ARM64 build is statically linked.

`deploy/deploy.sh` now requires clean `dev` and credential preflight before any build/SSH/upload, runs local checks, requires a known prior release, verifies the upload checksum and exact activated health version, then runs the browser smoke inside the rollback boundary. Failed health or smoke restores and checks the prior release while preserving the original failure code. The protected `moneybag.env` is never rewritten or uploaded. The launcher accepts only a private owned non-symlink Identity sidecar and exports its five ID keys literally; unrelated settings and shell evaluation are refused.

`make smoke-check rollout-check` passes. Tests cover credential/origin/file guards, unsafe sidecars, protected-file preservation, preflight refusing all remote/build calls, dry-run, single rollback and original failure preservation. They also execute the actual remote activation/health/rollback script bodies against owned temporary releases with local command shims, verifying both symlink restoration and exact-version health behavior. Bash syntax and shellcheck 0.11 pass. No deployment command was executed against Oracle VM and no GitHub Actions run was started. Publication/pinning, production registration and per-app human confirmation remain open.
