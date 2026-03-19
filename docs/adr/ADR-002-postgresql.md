# ADR-002: PostgreSQL as the Sole State Store

**Status:** Accepted
**Date:** 2026-03-18

## Context

jitsudo needs persistent storage for: elevation request state, policy definitions, and the append-only audit log. The store must support:

- Strong consistency for state transitions (PENDING → APPROVED → ACTIVE → EXPIRED/REVOKED)
- Row-level locking to prevent concurrent modification in HA deployments
- A rich query model for audit log filtering (by user, provider, time range, etc.)
- Advisory locks for distributed leader election (expiry sweeper, policy sync jobs)

Options considered: PostgreSQL, SQLite, etcd, CockroachDB.

## Decision

PostgreSQL 14+ is the sole state store. SQLite is not supported. etcd is not used.

## Consequences

**Positive:**
- Strong consistency guarantees via transactions and `SELECT FOR UPDATE`
- Advisory locks enable single-leader job scheduling without an external coordination system
- Rich SQL query model ideal for audit log filtering and reporting
- Operators have extensive operational experience with PostgreSQL; managed options (RDS, Cloud SQL, Azure Database) are universally available
- The audit log's append-only hash chain is naturally expressed as a SQL table
- golang-migrate provides a clean schema migration story

**Negative:**
- PostgreSQL is an operational dependency that must be deployed alongside jitsudod. This raises the bar for local evaluation vs. a SQLite-based zero-dependency mode.
- The Docker Compose environment mitigates this for local use.

**Why not SQLite:**
SQLite's single-writer model and lack of network accessibility make it unsuitable for any HA path. Starting with SQLite and migrating to PostgreSQL later is a painful breaking change (different schema features, different migration tooling, different driver). Since HA is a defined enterprise feature, the database must support it from day one. See ADR-009 for more detail.

**Why not etcd:**
etcd is optimized for high-frequency, low-latency key-value operations across many nodes — the Kubernetes use case. jitsudo's write volume (elevation requests) is orders of magnitude lower. etcd's query model is also inadequate for audit log filtering. PostgreSQL provides all the consistency guarantees etcd offers at jitsudo's scale, plus a richer data model.
