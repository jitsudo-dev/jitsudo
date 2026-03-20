# jitsudo

**sudo for your cloud.** Request temporary admin-level access to AWS, Azure, GCP, or Kubernetes — with approval workflows, audit logs, and automatic expiry — from a single CLI that works across all your clouds.

[![CI](https://github.com/jitsudo-dev/jitsudo/actions/workflows/ci.yml/badge.svg)](https://github.com/jitsudo-dev/jitsudo/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/jitsudo-dev/jitsudo)](https://goreportcard.com/report/github.com/jitsudo-dev/jitsudo)
[![License: Apache 2.0 / ELv2](https://img.shields.io/badge/license-Apache%202.0%20%2F%20ELv2-blue)](#license)

---

## What is jitsudo?

jitsudo is an open source, cloud-agnostic, CLI-first Just-In-Time (JIT) privileged access management tool for infrastructure administrators and SREs.

The name combines **JIT** (Just-In-Time) and **sudo** — the Unix privilege escalation utility. jitsudo enables engineers to temporarily elevate their cloud permissions through an audited, policy-driven, approval-based workflow. Permissions are granted for a defined duration and automatically revoked when the window expires.

## Quickstart (5 minutes)

**Prerequisites:** Docker or Podman, and a terminal.

```bash
# 1. Clone and start the local dev environment
git clone https://github.com/jitsudo-dev/jitsudo
cd jitsudo
make docker-up

# 2. Install the CLI (macOS/Linux)
curl -fsSL https://jitsudo.dev/install.sh | sh

# 3. Log in (uses the local dev OIDC provider)
jitsudo login --provider http://localhost:5556/dex

# 4. Request temporary elevated access
jitsudo request \
  --provider aws \
  --role prod-infra-admin \
  --scope 123456789012 \
  --duration 1h \
  --reason "Investigating ECS crash - INC-4421"

# 5. Execute a command with elevated credentials
jitsudo exec <request-id> -- aws ecs describe-tasks --cluster prod
```

## Key Features

- **Cloud-agnostic** — AWS, Azure, GCP, and Kubernetes from a single CLI
- **CLI-first** — designed for SREs who live in the terminal
- **Approval workflows** — policy-driven approvals via OPA/Rego
- **Tamper-evident audit log** — every action logged with a hash chain
- **Break-glass mode** — emergency access with immediate alerts
- **Self-hosted** — credentials never leave your infrastructure
- **OIDC native** — integrates with Okta, Entra ID, Google Workspace, Keycloak

## CLI Reference

```
jitsudo login                  Authenticate via OIDC device flow
jitsudo request                Submit a new elevation request
jitsudo status                 Check request status
jitsudo approve / deny         Approve or deny a pending request
jitsudo exec <id> -- <cmd>     Run a command with elevated credentials
jitsudo shell <id>             Open an elevated interactive shell
jitsudo revoke <id>            Revoke an active elevation early
jitsudo audit                  Query the audit log
jitsudo policy                 Manage OPA/Rego policies (admin)
jitsudo server init            Bootstrap a new control plane instance
jitsudo server status          Check control plane health
jitsudo server version         Print server version and API compatibility
jitsudo server reload-policies Trigger OPA policy engine reload
```

## Architecture

jitsudo follows the Kubernetes model: a versioned API server (control plane) that all clients interact with through a stable, authenticated API. The CLI is a first-class but not privileged client.

```
jitsudo CLI  ──gRPC/REST──>  jitsudod Control Plane
                               ├── Auth (OIDC/JWKS)
                               ├── Policy Engine (OPA embedded)
                               ├── Request State Machine
                               ├── Provider Adapter Layer
                               │     AWS / Azure / GCP / Kubernetes
                               ├── Audit Log (append-only, hash chain)
                               └── Notification Dispatcher
                                     Slack / email / webhook
```

See [docs/adr/](docs/adr/) for Architecture Decision Records.

## Deployment

| Method | Target | Command |
|--------|--------|---------|
| Docker Compose | Local / evaluation | `make docker-up` |
| Bootstrap command | Single VM / bare metal | `jitsudo server init` |
| Helm chart | Kubernetes (production) | `helm install jitsudo jitsudo/jitsudo` |
| Terraform modules | Cloud-bootstrapped | `terraform apply` |

## License

jitsudo uses a split open core licensing model:

- **CLI, Provider SDK, OPA libraries** — [Apache License 2.0](LICENSE-APACHE)
- **Control plane server (`jitsudod`)** — [Elastic License 2.0](LICENSE-ELV2)

Self-hosted use for your own organization is unrestricted. See [LICENSE](LICENSE) for details.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). All contributors must sign a Developer Certificate of Origin (DCO).

## Security

To report a vulnerability, see [SECURITY.md](SECURITY.md).
