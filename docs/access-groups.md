# Central app access integration

Moneybag's implementation and local qualification cover its stored subject, browser and OAuth credentials, independent private render credentials, recipient sharing and owner jobs. Production has not been changed. The complete proposal still requires provider migration/legacy proof, reviewed grants, each app's production acceptance and operational handoff.

## Configuration and ownership

`MONEYBAG_ID_ACCESS_ENABLED` defaults false and accepts only literal true/false. True requires ID sign-in and confidential client credentials. Public PKCE alone cannot use the check endpoint. Startup configures the central checker before starting owner rate sync, snapshot refresh or prewarming. The shared Go SDK is pinned with checksums to `v0.0.0-20261117021458-a8a6d8068d30`.

The fresh callback decision precedes subject linking, invitation consumption and ledger/model initialization. Local invitation and disabled-user requirements, original ownership, roles, scopes and CSRF remain in force. Every credential's local `user_id` resolves through the existing unique `user_identities` mapping to its stable ID subject. Email changes do not move that mapping. Unbound users fail closed when enforcement is on; neither the owner nor a local administrator has an exemption. Legacy verification and missing bindings need an explicitly reviewed migration before cutover; grants never fabricate email proof.

Positive leases last at most 60 seconds from request start. Watched work begins with a fresh check and rechecks every 25 seconds, with a two-second total bound per attempt, independently of traffic. Failed/denied checks invalidate old positive leases. Release joins the checker before the handler returns, including an in-flight check, because Fiber recycles the parent context. A regression test and the actual loaded race test cover that lifetime.

## Coverage

| Boundary | Enforcement |
| --- | --- |
| Callback and new ID session | Fresh eligibility before linking/account initialization and session creation; existing verified-email/invitation rules remain. |
| Browser | Every protected read, account/settings operation, confirmation/feed route and browser MCP branch owns a watched request context. Cookie resolution checks the current stored subject and local disabled state. Sign-in, logout, discovery, health and static UI remain independent. Both SDK and legacy cookie/CSRF logout work while centrally blocked. |
| OAuth | Fresh owner checks at authorization, code exchange and refresh; bearer resolution checks the owner lease. Client/resource/redirect/PKCE/scope/suspension and token-family reuse checks remain. Valid refresh survives denial/outage and works after restoration. |
| Remote MCP and stdio | Both full and read-only HTTP resources own watched contexts and propagate request cancellation. Stdio forwards the same human OAuth token; an agent actor is not a service exemption. Existing grant/admin/scope controls remain. |
| Private render token/cookie | Token shell/page/view/cube/report routes check the original principal before use. The separate render-cookie browser-MCP branch is covered and retains its read-only scope/toolset. Production token issuance occurs inside guarded capture work; the entire capture inherits its owner's watched lifetime and revokes its token on exit. |
| Recipient sharing | Check the actual viewer before projecting an owner-scoped finance principal. Preserve current grant, role, source/filter, publication and permission-hash checks. These are registered-recipient grants, not anonymous public sharing. |
| Scheduled share snapshots | Each owner's refresh starts fresh and stays watched. A denied owner leaves the grant, timestamps and financial snapshot unchanged; restoration resumes due work. The `system` actor label does not exempt owner work. |
| Rates and prewarming | Per-user rate synchronization, including startup, checks before provider/data work. Deferred prewarming and detached view refresh have their own owner lifetimes; cancellation/denial releases their worker/cache flight. |
| Independent maintenance | Explicit integrity verification, expiry/schema maintenance and bounded audit outcome persistence remain independent service work. Cancellation-independent finalization records outcomes, rather than launching new human finance/provider operations. |

The net/http implementation runs through the production Fiber/compression adapter. The app uses stateless JSON MCP responses and has no separate user WebSocket/SSE route in this inventory. In-flight tools and headless work still need cancellation, and are tested through the adapter. Already delivered content and browser offline replicas cannot be recalled.

## Evidence and reproduction

- Full owned PostgreSQL 18 `make check` passes: Go tests/vet including the database integration suite, Bun lint/build and standalone scripts, 38 Vitest tests, smoke preflight and rollout tests. The full Go race suite passes. Native ARM64 build passes. No frontend design or finance representation changed.
- Stable binding, disabled/unbound users, storage failure, a stale cached initial allow, actual 25-second interval/two-second deadline and watcher release are tested. Mounted Fiber tests cover cookies, local OAuth/MCP, independent private render tokens/cookies/report APIs, refresh preservation and blocked logout. Startup rates, scheduled snapshots, prewarm and detached refresh have pause/cancellation tests.
- The loaded rehearsal uses real PostgreSQL 18 and the published SDK with an owned policy endpoint and capture fake. Four workers read cookie, bearer, render-cookie and private-token boundaries every 100 ms. Actual in-flight browser/OAuth MCP calls enter capture and then stop, alongside idle owner work. The browser capture retains the production guard; the agent capture delegates only its caller context, isolating HTTP/MCP cancellation so its own timer cannot mask a broken propagation link. The earlier passing run with both capture guards retained measured 25,001/24,999 ms across 1,962 responses and is preserved in the report. Removal completes within **24,997 ms** and policy unavailability within **24,998 ms**, with **1,964** expected responses and successful restoration. The first loaded race run exposed a watcher-parent lifetime race, fixed by joining before release; the passing report is retained in `measurements/access-moneybag-20261117.json`.
- The separate actual-provider scenario runs the Rust Identity binary and Moneybag HTTP implementation in owned native test processes with two PostgreSQL 18 clusters. Captured verified invitations and reasoned protected-admin-API policy drive signed callback/local invitation/replay checks, existing browser/OAuth/MCP access, Block, a real isolated policy-table outage, refresh restoration and blocked-cookie logout. An asserted sequence ensures those policy actions cannot be silently skipped. Financial decimal/profile/model/permission/existing-grant snapshots, both persistent smokes, mode rollback and eval-device cleanup pass. The report is `measurements/access-moneybag-native-20261117.json`. This scenario qualifies the native HTTP implementation, not a production deployment or external financial-provider execution.
- Launcher/actual remote-body fixtures cover both enabled and disabled activation/rollback, wrong enforcement, health/smoke failure, concurrent release protection, literal/private configuration and a frozen mode overriding inherited/staged flag-off. Binary rollback preserves enforcement; policy restoration must use a new audited revision.

```bash
GOFLAGS=-p=4 bash deploy/test-owned-postgres.sh make check
make access-check
make access-rehearsal
bash deploy/test-owned-postgres.sh make test-identity-provider
```

The native runner requires PostgreSQL 18 tools; it explicitly verifies that version instead of accepting another installed major. It owns a loopback cluster and cleans its directory/process on exit. Provider scenarios also require the sibling Identity debug binary and installed e2e dependencies. The native OAuth callback uses an ephemeral owned listener, rather than depending on a fixed port or browser interception. Test fixtures stay on these owned servers; no production account, grant, database, secret or service changes.

## Cutover and rollback

Deployment captures the currently running mode with the exact previous release version. Both previous and next binaries retain an owned mode-0600 `access.flag`; the launcher reads it after operator-owned base/ID settings. Activation and rollback verify exact version and mode, and reject a changed previous snapshot. A legacy binary lacking the field can only be captured as flag-off. Credentials remain operator-owned; no environment file is uploaded or rewritten. Invalid credentials/configuration fail closed when the frozen flag is true.

Install the supporting binary in the existing mode before the separate reviewed enforcement cutover. Complete provider proof/migration and seed review, reconcile unbound accounts, and verify each app's acceptance before enabling its release snapshot and restarting. Never revert a policy by selecting flag-off or a pre-enforcement binary. Restore the reviewed policy with a fresh audited revision while retaining enforcement. Existing unrelated local `agentpack.toml`, `pack.lock` and `.envrc` edits are outside this integration and are not included in its commit.
