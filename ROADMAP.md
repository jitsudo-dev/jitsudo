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

## Milestone 3: Production Readiness (Current)

- [x] Helm chart (`helm/jitsudo/` in main repo, postgresql subchart included)
- [x] `jitsudo server init` bootstrap command
- [x] mTLS for gRPC (server-only TLS and mutual TLS via TLSConfig)
- [x] Integration test suite (LocalStack for AWS, envtest for Kubernetes)
- [x] `jitsudo server` admin subcommands (status, version, reload-policies)
- [ ] Comprehensive documentation site (jitsudo.dev)

## Milestone 4: Ecosystem

- [ ] Terraform modules (AWS/Azure/GCP)
- [ ] E2E test suite (live cloud accounts)
- [ ] Homebrew tap
- [ ] Docker Hub images
- [ ] Generic webhook notification
- [ ] Multi-region / HA deployment guide

## Enterprise Features (Future)

- Multi-tenancy
- SIEM integration connectors (Splunk, Datadog, SumoLogic)
- Slack interactive approval buttons
- Policy GitOps sync
- Hierarchical scope inheritance
- Multi-instance HA with HPA + PodDisruptionBudget
- Session recording
- ITSM integration (post-incident review)
