# ADR-006: Provider Interface as the Core Abstraction

**Status:** Accepted
**Date:** 2026-03-18

## Context

jitsudo must support multiple cloud providers (AWS, Azure, GCP, Kubernetes) from day one, with a clear path for community-contributed providers. The architecture must avoid duplicating approval workflow logic across providers and make it easy to add new providers without modifying core control plane logic.

## Decision

The `Provider` interface (defined in `internal/providers/provider.go`) is the central abstraction. All cloud-specific logic is encapsulated behind this interface. The control plane interacts exclusively with the `Provider` interface; it has no knowledge of any specific cloud provider.

```go
type Provider interface {
    Name() string
    ValidateRequest(ctx context.Context, req ElevationRequest) error
    Grant(ctx context.Context, req ElevationRequest) (*ElevationGrant, error)
    Revoke(ctx context.Context, grant ElevationGrant) error
    IsActive(ctx context.Context, grant ElevationGrant) (bool, error)
}
```

A shared contract test suite (`internal/providers/contract_test.go`) defines behavioral expectations all providers must satisfy.

## Consequences

**Positive:**
- Adding a new provider requires implementing five methods and passing the contract tests — no changes to the workflow, policy, or audit subsystems
- The mock provider enables comprehensive unit testing of the entire control plane without any cloud credentials
- Contract tests ensure behavioral consistency across providers (idempotency of `Grant`, behavior of `IsActive` after `Revoke`, etc.)
- The provider registry enables runtime configuration of which providers are enabled
- The interface is Apache 2.0 licensed, enabling community-contributed providers without ELv2 restrictions

**Negative:**
- The interface may not perfectly accommodate every provider's nuances (e.g., GCP's IAM conditional bindings vs. AWS's STS token model). The `Metadata` map provides an escape hatch for provider-specific parameters.
- Interface evolution requires backward-compatible changes or a new interface version

**Pattern reference:**
This is the same pattern used by `database/sql` drivers in Go and Kubernetes CSI drivers — a stable interface with a shared contract test suite ensures behavioral consistency across implementations contributed by different parties.
