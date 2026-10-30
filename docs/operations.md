# Operations

Production uses the existing standalone MongoDB on oracle-vm, one shared `moneybag` database and one API writer. Caddy and the MongoDB service are managed separately. Moneybag does not change their configuration.

## Test

`make test-integration` runs every Go test against a throwaway in-memory MongoDB in Docker in about 20 seconds. Point `MONEYBAG_TEST_MONGO_URI` at a remote `moneybag_test_` database only when you must: every round trip then costs network time and the suite takes minutes.

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
