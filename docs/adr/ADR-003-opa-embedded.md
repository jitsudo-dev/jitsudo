# ADR-003: OPA Embedded as the Policy Engine

**Status:** Accepted
**Date:** 2026-03-18

## Context

jitsudo needs a policy engine to evaluate two classes of rules:
1. **Eligibility policies**: Is user X eligible to request role Y in scope Z?
2. **Approval policies**: Who must approve this request? Can it be auto-approved?

The engine must be expressive enough to handle group-based access, time-based constraints, and conditional logic — but must also be auditable (policy-as-code stored in the database) and testable independently of the running system.

Options considered: OPA (embedded), OPA (sidecar), custom rule engine, Cedar (AWS), Polar (Oso).

## Decision

OPA (Open Policy Agent) is embedded as a Go library (`github.com/open-policy-agent/opa`), not deployed as a sidecar.

## Consequences

**Positive:**
- Rego is a well-known, declarative policy language in the cloud-native community — lowers the barrier for operators writing their own policies
- Embedding OPA as a library eliminates a network hop and a separate process to manage; policy evaluation happens in-process at sub-millisecond latency
- OPA has excellent tooling: `opa eval` for dry-run testing, `opa test` for unit testing policies, and native support in VS Code
- Policies stored in PostgreSQL are versioned and auditable; every change is a recorded event
- The `jitsudo policy eval` command exposes dry-run evaluation to operators for debugging

**Negative:**
- Rego has a learning curve for operators unfamiliar with datalog-style evaluation
- Embedding OPA increases the binary size of jitsudod
- OPA upgrades require recompiling jitsudod (vs. upgrading a sidecar independently)

**Why not OPA sidecar:**
A sidecar introduces a network dependency on the critical path of every request evaluation. If the sidecar is unavailable, jitsudo cannot evaluate policies. Embedding eliminates this failure mode and reduces operational complexity.

**Why not Cedar or Polar:**
Cedar (AWS) is AWS-centric and not yet widely adopted outside of AWS services. Polar (Oso) has a less mature community and Go support. OPA is CNCF-graduated, widely deployed, and the de-facto standard for cloud-native policy-as-code.
