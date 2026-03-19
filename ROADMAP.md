# Roadmap

This roadmap describes the planned development trajectory for jitsudo. It is a living document and will be updated as priorities evolve.

## Milestone 0: Foundation (Current)

- [x] Requirements & specification document
- [ ] Monorepo scaffold (CLI + server structure, Go module, licensing)
- [ ] Protobuf API definitions with buf.build
- [ ] Provider interface with contract tests
- [ ] Mock provider for unit testing
- [ ] Local development environment (Docker Compose + dex)
- [ ] Architecture Decision Records

## Milestone 1: Walking Skeleton

Goal: A minimal end-to-end flow that works locally with mock or real credentials.

- [ ] `jitsudo login` — OIDC device flow against a real IdP
- [ ] `jitsudo request` — Submit a request, persist to PostgreSQL
- [ ] `jitsudo approve` / `jitsudo deny` — Basic approval flow
- [ ] `jitsudo status` — Retrieve and display request state
- [ ] Request state machine (PENDING → APPROVED → ACTIVE → EXPIRED)
- [ ] OPA policy engine integration (eligibility + approval policies)
- [ ] Audit log (append-only, hash chain)
- [ ] AWS provider implementation (STS AssumeRole)
- [ ] `jitsudo exec` — Execute command with injected credentials

## Milestone 2: Full Provider Coverage

- [ ] Azure provider (RBAC role assignment via Microsoft Graph)
- [ ] GCP provider (IAM conditional role binding)
- [ ] Kubernetes provider (ClusterRoleBinding with TTL)
- [ ] `jitsudo shell` — Interactive elevated shell
- [ ] `jitsudo revoke` — Early revocation
- [ ] Break-glass mode
- [ ] Slack notification integration
- [ ] Email (SMTP) notification integration

## Milestone 3: Production Readiness

- [ ] Helm chart (`jitsudo/helm-charts`)
- [ ] `jitsudo server init` bootstrap command
- [ ] Database migrations (golang-migrate)
- [ ] mTLS for server-to-server communication
- [ ] `jitsudo audit` CLI command with filtering
- [ ] `jitsudo policy` CLI commands (CRUD + dry-run eval)
- [ ] Comprehensive documentation site (jitsudo.dev)
- [ ] Integration test suite (LocalStack + kind + dex)

## Milestone 4: Ecosystem

- [ ] Terraform modules (AWS/Azure/GCP)
- [ ] E2E test suite (live cloud accounts)
- [ ] Homebrew tap
- [ ] Docker Hub images
- [ ] Generic webhook notification
- [ ] `pkg/client` Go client library (stable)

## Enterprise Features (Future)

- Multi-tenancy
- SIEM integration connectors (Splunk, Datadog, SumoLogic)
- Slack interactive approval buttons
- Policy GitOps sync
- Hierarchical scope inheritance
- Multi-instance HA with HPA + PodDisruptionBudget
- Session recording
- ITSM integration (post-incident review)
