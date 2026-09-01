# Public repository scope

The public repository contains the complete reproducible application source: Go backend, Mineflayer collector, Fabric client, schemas, tests, deployment scripts, architecture/API/workflow documentation, decision and review logs, CI, and retained release JARs.

The following runtime material is deliberately excluded because publishing it would expose credentials, personal data, or large mutable state rather than useful source code:

- Donut API keys and scoped backend/client tokens
- Microsoft authentication caches and account configuration
- proxy credentials and observed egress addresses
- SQLite databases, WAL files, backups, and auction-history snapshots
- live menu captures, player inventories/NBT/chat, usernames, and local mod state
- logs, compiled caches, dependency directories, and machine-specific executables
- SSH private keys and machine-specific deployment coordinates

Sanitized example configuration and deterministic fixtures cover every excluded configuration shape needed to build and test the system. The Git history is secret-scanned before public releases.
