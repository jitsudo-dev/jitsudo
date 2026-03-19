# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for jitsudo.
ADRs document why key design decisions were made, not just what was decided.

| ADR | Title | Status |
|-----|-------|--------|
| [ADR-001](ADR-001-go-language.md) | Go as the implementation language | Accepted |
| [ADR-002](ADR-002-postgresql.md) | PostgreSQL as the sole state store | Accepted |
| [ADR-003](ADR-003-opa-embedded.md) | OPA embedded as the policy engine | Accepted |
| [ADR-004](ADR-004-grpc-rest.md) | gRPC + REST dual API surface | Accepted |
| [ADR-005](ADR-005-licensing.md) | ELv2 for control plane, Apache 2.0 for CLI/SDK | Accepted |
| [ADR-006](ADR-006-provider-interface.md) | Provider interface as the core abstraction | Accepted |
| [ADR-007](ADR-007-oidc-device-flow.md) | OIDC device flow for CLI authentication | Accepted |
| [ADR-008](ADR-008-monorepo.md) | Monorepo structure with separate repos for Helm and Terraform | Accepted |
| [ADR-009](ADR-009-no-sqlite.md) | No SQLite support | Accepted |
| [ADR-010](ADR-010-buf-build.md) | buf.build for protobuf management | Accepted |

## Format

Each ADR follows this structure:
- **Status**: Proposed / Accepted / Deprecated / Superseded
- **Context**: The situation that led to this decision
- **Decision**: What was decided
- **Consequences**: The resulting trade-offs
