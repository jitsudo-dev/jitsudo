# Roadmap

This roadmap describes the planned development trajectory for jitsudo. It is a living document and will be updated as priorities evolve.

## Milestone 0: Foundation ✓

- [x] Requirements & specification document
- [x] Monorepo scaffold (CLI + server structure, Go module, licensing)
- [x] Protobuf API definitions with buf.build
- [x] Provider interface with contract tests
- [x] Mock provider for unit testing
- [x] Local development environment (Docker Compose + dex)
- [x] Architecture Decision Records

## Milestone 1: Walking Skeleton ✓

Goal: A minimal end-to-end flow working locally against the mock provider.

- [x] `jitsudo login` — OIDC device flow (RFC 8628) with credential storage
- [x] `jitsudo request` — Submit a request, persist to PostgreSQL
- [x] `jitsudo approve` / `jitsudo deny` — Basic approval workflow
- [x] `jitsudo status` — Retrieve and display request state (single + list)
- [x] `jitsudo exec` — Execute command with provider credentials injected as env vars
- [x] Request state machine (PENDING → APPROVED → ACTIVE → EXPIRED → REVOKED)
- [x] OPA policy engine integration (eligibility + approval Rego policies, hot-reload)
- [x] Audit log (append-only, SHA-256 hash chain, serializable transactions)
- [x] Database layer (pgx/v5 pool, golang-migrate embedded migrations)
- [x] gRPC API + grpc-gateway REST proxy (`/api/v1alpha1/...`)
- [x] `pkg/client` Go client library
- [x] Two-stage Dockerfile + CI proto generation step

## Milestone 2: Full Provider Coverage ✓

- [x] AWS provider (STS AssumeRole + session tagging)
- [x] Azure provider (RBAC role assignment via Microsoft Graph)
- [x] GCP provider (IAM conditional role binding)
- [x] Kubernetes provider (ClusterRoleBinding with TTL annotation)
- [x] `jitsudo shell` — Interactive elevated shell
- [x] `jitsudo revoke` — Early revocation before natural expiry
- [x] `jitsudo audit` — Query audit log from the CLI with filtering
- [x] `jitsudo policy` — CRUD + dry-run policy evaluation from the CLI
- [x] Break-glass mode (bypass approval with immediate alerting)
- [x] Slack notification integration
- [x] Email (SMTP) notification integration

## Milestone 3: Production Readiness ✓

- [x] Helm chart (`helm/jitsudo/` in main repo, postgresql subchart included)
- [x] `jitsudod init` bootstrap command
- [x] mTLS for gRPC (server-only TLS and mutual TLS via TLSConfig)
- [x] Integration test suite (LocalStack for AWS, envtest for Kubernetes)
- [x] `jitsudo server` admin subcommands (status, version, reload-policies)
- [x] Comprehensive documentation site (jitsudo.dev)

## Milestone 4: Architecture (Current)

Goal: Implement the three-tier approval model and the architectural decisions captured in the design review. This milestone delivers the complete, production-grade access control model.

- [x] Three-tier approval model — OPA tier routing (`approver_tier: auto | ai_review | human`)
- [x] Tier 1: policy-driven auto-approve in `workflow.go` (no human or AI action required)
- [x] Tier 2: AI-assisted review — MCP approver interface on `jitsudod`
- [x] Trust tier system for principals (Tier 0–4, principal enrollment, `input.trust_tier` policy input)
- [x] AI approver audit trail (model reasoning captured per Tier 2 decision)
- [x] Generic webhook notification
- [x] SIEM integration (basic JSON streaming + syslog forwarding)
- [x] Multi-instance HA deployment (HPA, PodDisruptionBudget, PostgreSQL replication)

## Milestone 5: Ecosystem

- [x] GitHub Container Registry images (ghcr.io/jitsudo-dev) — jitsudod server + jitsudo CLI, multi-arch, tag-triggered release workflow
- [x] Homebrew tap — `jitsudo-dev/homebrew-tap` repo, formula auto-updated on release
- [x] Terraform modules (AWS/Azure/GCP) — provision IAM roles, Azure app registrations, GCP service accounts for jitsudo deployments
- [x] E2E test suite (live cloud accounts) — full JIT lifecycle tests against real AWS/Azure/GCP/Kubernetes, `workflow_dispatch`-triggered CI

## Milestone 6: Enterprise Features

- [ ] Multi-tenancy
- [ ] SIEM integration connectors (Splunk, Datadog, SumoLogic)
- [ ] Slack interactive approval buttons
- [ ] Policy GitOps sync
- [ ] Hierarchical scope inheritance
- [ ] Session recording
- [ ] ITSM integration (post-incident review)
