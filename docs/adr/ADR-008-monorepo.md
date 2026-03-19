# ADR-008: Monorepo Structure with Separate Repos for Helm and Terraform

**Status:** Accepted
**Date:** 2026-03-18

## Context

The project has multiple components: the CLI, the control plane, provider adapters, the public client library, and shared types. The repository structure affects how contributors navigate the codebase, how CI is structured, how Go modules are versioned, and how the licensing boundary is enforced.

Options considered: monorepo (all Go code in one repo), polyrepo (separate repos for CLI and server), polyrepo per component.

## Decision

All Go code lives in a single monorepo (`github.com/jitsudo-dev/jitsudo`). Helm charts and Terraform modules live in separate repos (`helm-charts`, `terraform-modules`) because they have different release cadences, different contributor audiences, and different CI requirements.

## Consequences

**Positive:**
- Cross-cutting changes (e.g., updating the Provider interface) can be made atomically in a single PR
- A single `go.mod` eliminates the complexity of managing inter-module version dependencies
- The licensing boundary (Apache 2.0 for `internal/cli/` and `internal/providers/`, ELv2 for `internal/server/`) is enforced through directory convention, not repo separation
- Single CI pipeline for all Go code
- Easier for contributors to understand the full system in one place

**Negative:**
- The monorepo grows over time; contributors need to navigate a larger directory tree
- A single `go.mod` means the CLI binary's dependency surface includes server-side dependencies (OPA, pgx, etc.) even if the CLI doesn't use them at runtime — mitigated by Go's dead code elimination during compilation

**Separate repos for Helm and Terraform:**
- Helm charts are released independently of the Go binary (a chart version bump may fix a deployment issue without a code change)
- Terraform modules have a different contributor profile (infrastructure engineers vs. Go developers)
- Both have their own release cycles and versioning conventions that are simpler to manage independently
