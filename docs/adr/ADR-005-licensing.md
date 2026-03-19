# ADR-005: ELv2 for Control Plane, Apache 2.0 for CLI/SDK/Providers

**Status:** Accepted
**Date:** 2026-03-18

## Context

jitsudo is designed as an open source project with a future commercial path. The licensing model must:
- Maximize community adoption and contribution
- Prevent cloud providers and competitors from offering jitsudo as a managed hosted service without a commercial license
- Not restrict self-hosted use (the primary use case of the target audience)
- Remain compatible with contributor expectations and OSI definitions

Options considered: MIT/Apache 2.0 (fully open), AGPL-3.0, Business Source License (BUSL), Elastic License v2 (ELv2), split licensing.

## Decision

Split open core licensing:
- **CLI (`jitsudo`), Provider SDK, OPA policy libraries**: Apache License 2.0
- **Control plane server (`jitsudod`)**: Elastic License v2 (ELv2)

## Consequences

**Positive:**
- The CLI and provider SDK are completely unrestricted (Apache 2.0) — maximizes adoption, contribution, and integration by third-party tooling
- ELv2 for the control plane prevents "rogue SaaS" — a cloud provider cannot offer Managed jitsudo without a commercial license
- Self-hosted use (the primary use case for the target audience) is fully unrestricted under ELv2
- ELv2 is narrow and specific — the restriction is only on providing the software as a hosted service to third parties
- Starting restrictive is a one-way door in the favorable direction: the project can always relicense to Apache 2.0 later (celebrated by the community), but cannot add restrictions to code that started as Apache 2.0 without breaking contributor trust

**Negative:**
- ELv2 is not an OSI-approved open source license; the project cannot call itself "open source" for the control plane under the OSI definition
- Some organizations have policies against using ELv2-licensed software; this may limit enterprise adoption in those contexts
- Contributor agreements must be clear about which component is being contributed to and under which license

**Why not AGPL:**
AGPL requires anyone who runs the software as a network service to publish their modifications. This could discourage enterprise adoption and is more complex to comply with than ELv2. ELv2's restriction is more targeted.

**Why not BUSL:**
BUSL has a time-based license conversion mechanism (to Apache 2.0 after N years). This is more complex to communicate and reason about than ELv2's simple service-restriction model.
