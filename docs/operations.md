# Operations

Production uses the existing standalone MongoDB on oracle-vm, one shared `moneybag` database and one API writer. Caddy and the MongoDB service are managed separately. Moneybag does not change their configuration.

## Deploy

Create `/home/snowbear/ci/moneybag/moneybag.env` with mode `0600` using `deploy/env.example`. Preserve its secret key across releases. Set `MONEYBAG_CHROMIUM_BIN` to a working Chromium installation; the VM already has `/opt/google/chrome/chrome`.

Run `deploy/deploy.sh` from a committed checkout. It builds an ARM64 binary, copies a versioned release, atomically replaces `current`, installs the smdctl user service and checks the running release's health/version. A failed upgrade restores the previous symlink. Old releases remain available for manual rollback.

```sh
ssh oracle-vm 'bash -lc "smdctl status moneybag"'
ssh oracle-vm 'curl -fsS http://127.0.0.1:6459/healthz'
```

The service restarts on failure and after login/boot through the VM's existing user linger setup. It listens only on loopback. Point Caddy at `127.0.0.1:6459`, then visit `/setup` with the private setup token from the environment file. Remove that token after creating the owner. No real finance data is preloaded; the user will populate it through Grokbot.

## Maintenance

Inside the sole API process, daily UTC jobs refresh ECB prices at 17:00 and check journal/audit invariants and balance assertions at 03:00. Snapshot sharing refreshes hourly according to each grant's policy. Failed maintenance is logged with the job and user ID.

For an operator integrity check, run the binary with the same environment and the `verify` argument. It reads each user's ledger and validates its balanced postings and audit intent/outcome hashes. It does not repair an interrupted workflow automatically.
