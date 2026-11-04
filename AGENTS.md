# AGENTS.md

## STRICT: standalone MongoDB only

- This project must run on the existing, simple, single-instance **standalone MongoDB** on the user's machine.
- **Never introduce features that require a replica set or sharded cluster. A single-node replica set is also forbidden.**
- Do not use MongoDB multi-document transactions, sessions that require replication, change streams, or any other replication-dependent feature.
- Do not enable, provision, recommend, or require replication. Do not change the system MongoDB configuration or require a topology upgrade for an application feature.
- Design persistence and consistency around capabilities supported by standalone MongoDB, including single-document atomic operations where needed.
- Do not claim multi-document writes are atomic or fake transaction guarantees with compensating writes. If a proposed feature depends on those guarantees, redesign it to work on standalone MongoDB and state its actual guarantees.
- Existing standalone installations must continue working without additional MongoDB services or infrastructure changes.
