# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-03-24

### Added

**Project foundation**
- Monorepo structure, Go module (`github.com/jitsudo-dev/jitsudo`), and split licensing (Apache 2.0 for CLI/SDK, ELv2 for control plane)
- Protobuf API definitions (`api/proto/jitsudo/v1alpha1/`) managed via buf.build
- Provider interface (`internal/providers`) with contract test suite and mock implementation
- Docker Compose local development environment (jitsudod + PostgreSQL + dex OIDC)
- Architecture Decision Records ADR-001 through ADR-011

**Core access workflow**
- Request state machine: `PENDING → APPROVED → ACTIVE → EXPIRED → REVOKED`
- OPA policy engine integration (eligibility + approval Rego policies, hot-reload)
- Audit log: append-only, tamper-evident SHA-256 hash chain, serializable transactions
- PostgreSQL state store (pgx/v5 connection pool, golang-migrate embedded migrations)
- gRPC API with grpc-gateway REST proxy (`/api/v1alpha1/...`)
- `pkg/client` Go client library for programmatic access

**Cloud providers**
- **AWS** — STS AssumeRole and AWS Identity Center modes, session tagging
- **Azure** — RBAC role assignment via Microsoft Graph API (service principal + managed identity)
- **GCP** — IAM conditional role binding with TTL-scoped conditions
- **Kubernetes** — ClusterRoleBinding with TTL annotation and jitsudo.dev expiry tracking

**Approval model and policies**
- Three-tier approval routing in `approver_tier` policy field: `auto | ai_review | human`
- Tier 1: OPA policy-driven auto-approve (no human or AI action required)
- Tier 2: AI-assisted review via MCP approver interface (`POST /mcp`) — model reasoning captured in audit log per decision
- Tier 3: policy-designated human approver via `jitsudo approve` / `jitsudo deny`

**Trust tier system**
- Principal trust tiers 0–4 with enrollment API
- `input.context.trust_tier` exposed to all OPA policy rules for fine-grained gating

**Break-glass emergency access**
- Bypass approval with a declared reason; triggers immediate Slack/email alerts and opens a mandatory post-incident review

**Notifications**
- Slack integration (webhook-based, configurable per request type)
- Email / SMTP integration
- Generic outbound webhook notification
- SIEM integration: structured JSON streaming and syslog forwarding

**CLI (`jitsudo`)**
- `login` — OIDC device flow (RFC 8628) with local credential storage
- `request` — submit an elevation request; supports `--provider`, `--role`, `--scope`, `--duration`, `--reason`, `--break-glass`
- `status` — retrieve single request or list all requests; `--output/-o` flag (table, json, yaml)
- `approve` / `deny` — human approval workflow
- `exec <id> -- <cmd>` — run a command with provider credentials injected as environment variables
- `shell <id>` — open an interactive elevated shell
- `revoke <id>` — early revocation before natural expiry
- `audit` — query the audit log with filtering; `--output/-o` flag
- `policy` — CRUD management and dry-run evaluation of OPA/Rego policies; `--output/-o` flag
- `server status` / `server version` / `server reload-policies` — admin subcommands
- `JITSUDO_SERVER` environment variable as fallback for `--server` flag

**Control plane (`jitsudod`)**
- `jitsudod init` — bootstrap a new control plane instance; `JITSUDOD_DATABASE_URL`, `JITSUDOD_OIDC_ISSUER`, and `JITSUDOD_OIDC_CLIENT_ID` environment variable fallbacks
- `jitsudod` — run the control plane daemon (ELv2)
- mTLS support for gRPC (server-only TLS and mutual TLS via `TLSConfig`)
- Multi-instance HA deployment support (HPA, PodDisruptionBudget, PostgreSQL replication guidance)

**Ecosystem and operations**
- GHCR multi-arch container images (`ghcr.io/jitsudo-dev/jitsudo`, `ghcr.io/jitsudo-dev/jitsudod`) with tag-triggered release workflow
- Homebrew tap (`jitsudo-dev/homebrew-tap`) with formula auto-updated on each release
- Terraform modules for AWS, Azure, and GCP (provision IAM roles, Azure app registrations, and GCP service accounts)
- Helm chart with PostgreSQL subchart for production Kubernetes deployments
- Integration test suite (LocalStack for AWS, envtest for Kubernetes)
- E2E test suite exercising the full JIT lifecycle against live AWS, Azure, GCP, and Kubernetes environments

### Fixed

- `make docker-up` OIDC issuer mismatch and incorrect client ID in development environment
- Checksum extraction `awk` patterns in release workflow
- `JITSUDO_SERVER` environment variable support and actionable error messages for missing flags
- gRPC client incorrectly including `http://` / `https://` scheme prefix when dialling
- JWKS fetches now follow the DiscoveryURL host when the OIDC issuer host differs
- Doubled `exec:` prefix in command-not-found errors from `jitsudo exec`
- `jitsudo audit` lacked `-o` shorthand for `--output` and accepted unknown format values silently
- Raw gRPC wire-level error messages surfaced directly in CLI output
- Rego compile errors in `ApplyPolicy` and `DeletePolicy` not caught until policy evaluation time

[0.1.0]: https://github.com/jitsudo-dev/jitsudo/releases/tag/v0.1.0
