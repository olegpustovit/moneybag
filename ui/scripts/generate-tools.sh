#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
schema=$(mktemp)
output=$(mktemp ui/src/lib/.tools.XXXXXX)
trap 'rm -f "$schema" "$output"' EXIT
(cd backend && go run ./cmd/toolschema) > "$schema"
bunx --bun --package json-schema-to-typescript@16.0.0 json2ts --input "$schema" --output "$output"
mv "$output" ui/src/lib/tools.d.ts
