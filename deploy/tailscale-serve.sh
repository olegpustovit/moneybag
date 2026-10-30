#!/usr/bin/env bash
# Serves the app to the tailnet only, on HTTPS, for the daily browser path. Safe to run again.
# It never enables Funnel, so nothing here is reachable from the internet.
set -euo pipefail
: "${MONEYBAG_TAILNET_PORT:=8443}"
: "${MONEYBAG_LISTEN:=127.0.0.1:6459}"
tailscale serve --bg --https="$MONEYBAG_TAILNET_PORT" "http://$MONEYBAG_LISTEN"
tailscale serve status
host=$(tailscale status --self --peers=false --json | sed -n 's/.*"DNSName": *"\([^"]*\)\.".*/\1/p')
echo
echo "Set this in moneybag.env, then restart the service:"
echo "MONEYBAG_TAILNET_URL=https://$host:$MONEYBAG_TAILNET_PORT"
