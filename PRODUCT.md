# Moneybag

Personal double-entry finance engine for humans and MCP agents. The authoritative brief is the private Moneybag MCP v0.4 proposal (artifact 7e9dz284qc1e, version 5). Preserve its square, quiet, dense interface and its functional scope.

## Users and tasks

An invite-only owner records and imports bank activity, reconciles balances, reviews budgets, models scenarios, and views dashboards. Agents use OAuth-authenticated MCP to perform the same workflows and publish validated dashboards without a human approval gate. All users share one MongoDB database, with user-scoped financial collections; dashboard grants are the only cross-user data path.

## Stack

One Go binary: Fiber v3, official MCP Go SDK, server-rendered HTML and SVG, buildless JavaScript, the existing standalone MongoDB on oracle-vm. Source conventions follow build-go-stack. Exactly one shared moneybag database for all users, using the existing MongoDB instance at 127.0.0.1:27017. No replica set or new database server. Single-document atomic writes, conditional updates, idempotent imports and durable audit intent/outcome records replace multi-document transactions. Production listens on 127.0.0.1:6459, behind user-configured Caddy at https://moneybag.microapps.space.

## Brand commitments

Preserve the supplied design: zero corner radii, shared 1px borders, 4px spacing grid, system sans body, system monospace identifiers and labels, blue data, green within budget, red over budget, amber drafts. Light, dark and system themes. Sidebar databases and dashboards, spreadsheet editing, version and agent attribution, spec drawer and rollback.

## Data

Financial data and the reference are private and excluded from Git. Public docs, examples, tests and screenshots use synthetic fixtures. The user will have Grokbot ingest and populate real data. Do not require or fabricate a migration from the rendered tracker. The engine retains import provenance and reproduces the tracker’s eight reference forecasts in a private parity test.

## Completion

See IMPLEMENTATION.md for the requirement and evidence ledger. Public repository, real screenshot, tested production service, and port 6459 are required. Caddy configuration belongs to the user.
