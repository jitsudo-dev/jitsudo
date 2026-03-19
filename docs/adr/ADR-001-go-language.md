# ADR-001: Go as the Implementation Language

**Status:** Accepted
**Date:** 2026-03-18

## Context

jitsudo needs an implementation language for both the CLI binary and the control plane daemon. The primary audience is SREs and platform engineers who work daily with tools like `kubectl`, `terraform`, `helm`, and `aws`. The language choice affects:

- Distribution model (single binary vs runtime dependency)
- Cross-platform compilation complexity
- Ecosystem fit with cloud SDKs (AWS, Azure, GCP)
- Contributor pool within the target community
- Performance for a control plane handling moderate write volume

## Decision

Go is the implementation language for both `jitsudo` (CLI) and `jitsudod` (control plane).

## Consequences

**Positive:**
- Single statically linked binary for the CLI — no runtime dependency, trivial distribution via curl, Homebrew, and package managers
- First-class AWS, Azure, and GCP SDKs in Go
- Native gRPC support and the ecosystem around buf.build
- Go is the lingua franca of the cloud-native infrastructure tooling community (Kubernetes, Terraform, Prometheus, etc.) — maximizes contributor pool
- Cross-compilation for Linux, macOS, and Windows with a single `GOOS`/`GOARCH` flag
- OPA (Open Policy Agent) is written in Go and has a well-maintained embedded library

**Negative:**
- Go's verbosity relative to Python or TypeScript means more boilerplate for some constructs
- Contributors from primarily Python/Ruby/Node backgrounds face a learning curve

**Neutral:**
- CGO is disabled (`CGO_ENABLED=0`) for the CLI to ensure fully static binaries. The control plane may use CGO if a dependency requires it, but the preference is to avoid it.
