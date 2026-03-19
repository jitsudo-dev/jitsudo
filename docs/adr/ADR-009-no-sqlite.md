# ADR-009: No SQLite Support

**Status:** Accepted
**Date:** 2026-03-18

## Context

SQLite is frequently requested as a zero-dependency option for tools in this category. The appeal is that it lowers the barrier for local evaluation (no separate database process required). Several comparable tools (Teleport Community, HashiCorp Vault dev mode) offer an in-memory or SQLite mode for development.

## Decision

jitsudo does not support SQLite. PostgreSQL is required for all deployment modes, including local development. The Docker Compose environment provides PostgreSQL automatically.

## Consequences

**Positive:**
- The codebase uses a single database abstraction (pgx/v5 + golang-migrate). No conditional code paths for SQLite vs. PostgreSQL schema differences.
- PostgreSQL features used by jitsudo — advisory locks for leader election, `SELECT FOR UPDATE` for state transitions, `GENERATED ALWAYS AS IDENTITY` for audit log IDs — either don't exist in SQLite or behave differently. Supporting both would require significant abstraction overhead.
- No risk of "works in SQLite dev, fails in PostgreSQL prod" bugs

**Negative:**
- The local development setup requires Docker or a PostgreSQL installation. The Docker Compose environment (`make docker-up`) mitigates this, but it raises the bar vs. a true zero-dependency mode.
- Some contributors and evaluators may be put off by the PostgreSQL requirement

**Why the HA path argument matters:**
HA is a defined enterprise feature. The HA implementation uses PostgreSQL-specific features (advisory locks for leader election, `SELECT FOR UPDATE` for serializable state transitions across multiple instances). If SQLite were supported for development, the HA feature would require a full re-implementation of the consistency model when migrating from SQLite to PostgreSQL. Building on PostgreSQL from day one ensures the HA path is validated continuously, not discovered broken at the moment a customer needs it.
