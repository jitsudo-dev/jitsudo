# Contributing to jitsudo

Thank you for your interest in contributing to jitsudo! This document describes how to get involved.

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](https://www.contributor-covenant.org/version/2/1/code_of_conduct/). By participating, you agree to uphold this standard.

## Developer Certificate of Origin (DCO)

All contributions must be signed with a Developer Certificate of Origin. Add a `Signed-off-by` trailer to every commit:

```
git commit -s -m "feat: add Azure provider stub"
```

This certifies that you wrote the patch or have the right to submit it. See [developercertificate.org](https://developercertificate.org/).

## Getting Started

### Prerequisites

- Go 1.26+
- Docker or Podman (for integration tests)
- `buf` CLI (for protobuf code generation) — `brew install bufbuild/buf/buf`
- `golangci-lint` — `brew install golangci-lint`

### Local Development

```bash
git clone https://github.com/jitsudo-dev/jitsudo
cd jitsudo

# Start the local development environment
make docker-up

# Build both binaries
make build

# Run unit tests
make test

# Run linter
make lint

# Regenerate protobuf code (after editing .proto files)
make proto
```

### Project Structure

```
cmd/jitsudo/       CLI entrypoint
cmd/jitsudod/      Control plane daemon entrypoint
internal/cli/      CLI command implementations (cobra)
internal/server/   Control plane implementation
internal/providers/Provider interface + cloud adapters
api/proto/         Protobuf definitions
pkg/               Public Go packages
deploy/            Docker Compose, Kubernetes manifests, Dockerfiles
terraform/modules/ Terraform modules for AWS, Azure, and GCP
test/e2e/          End-to-end tests against live cloud accounts
docs/adr/          Architecture Decision Records
```

## Making Changes

### Branches

- Branch from `main`
- Name your branch: `feat/short-description`, `fix/short-description`, or `chore/short-description`

### Commits

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add GCP IAM provider stub
fix: correct token refresh logic in OIDC client
chore: update golangci-lint to v1.62
docs: clarify break-glass policy requirements
test: add contract tests for AWS provider
```

### Pull Requests

1. Ensure all tests pass: `make test`
2. Ensure the linter passes: `make lint`
3. If you changed `.proto` files, regenerate and commit: `make proto`
4. Fill in the PR template
5. Request review from a maintainer

## Running E2E tests

E2E tests connect to a real jitsudod instance and require live cloud credentials. They are excluded from `make test` and `make test-integration` by the `//go:build e2e` tag.

```bash
cp test/e2e/.env.e2e.example test/e2e/.env.e2e
# fill in test/e2e/.env.e2e with real values
source test/e2e/.env.e2e
E2E_ENABLED=true make test-e2e
```

See [test/e2e/README.md](test/e2e/README.md) for per-provider setup instructions and required environment variables.

## Adding a New Provider

New cloud provider adapters must:

1. Implement the `Provider` interface in `internal/providers/provider.go`
2. Pass the full contract test suite in `internal/providers/contract_test.go`
3. Include unit tests using the mock infrastructure
4. Include integration tests tagged `//go:build integration`
5. Add a documentation guide under `docs/providers/`

See `internal/providers/mock/` for a reference implementation.

## Architecture Decision Records

Significant design decisions are documented as ADRs in `docs/adr/`. If your contribution changes a previous architectural decision or introduces a significant new one, propose a new ADR in your PR.

## Reporting Bugs

Use the [bug report template](.github/ISSUE_TEMPLATE/bug_report.md). Include the output of `jitsudo server version` and `jitsudo --debug`.

## Requesting Features

Use the [feature request template](.github/ISSUE_TEMPLATE/feature_request.md). Check existing issues and the [ROADMAP](ROADMAP.md) first.

## Security Vulnerabilities

Do **not** open a public issue for security vulnerabilities. Follow the process in [SECURITY.md](SECURITY.md).
