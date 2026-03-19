# ADR-011: Provider Configuration Schemas

**Status:** Accepted
**Date:** 2026-03-19

## Context

Each provider adapter (`aws`, `azure`, `gcp`, `kubernetes`) requires its own configuration to authenticate jitsudod to the cloud control plane and to select the correct grant mechanism. The initial scaffolding contained minimal Config structs. Before implementation begins, the full config schemas must be specified so that:

1. The `jitsudod` config file format is stable (YAML tags are part of the public operator interface)
2. Each provider's IAM/RBAC prerequisites are documented in one place
3. The multi-mode AWS design (STS vs. Identity Center) is explicitly decided

## Decision

### General principles

- All Config structs carry `yaml:"..."` tags matching the YAML key in the operator config file.
- A `MaxDuration` field on every provider caps the elevation window server-side, independent of what the requester asks for. Zero means "no cap beyond the platform limit".
- Credentials for jitsudod itself (service principal, instance profile, etc.) are **not** stored in the config struct — they follow each platform's ambient credential convention (env vars, instance metadata, workload identity). The `CredentialsSource` field selects the mechanism; secrets are never in config.

### AWS

Two sub-modes selected by `Mode`:

- **`sts_assume_role`** — jitsudod calls `sts:AssumeRole` on an IAM role whose ARN is derived from `RoleARNTemplate`. Simpler; no IAM Identity Center dependency.
- **`identity_center`** — jitsudod calls `sso:CreateAccountAssignment` / `sso:DeleteAccountAssignment` to assign a permission set to the user in a specific account. Recommended for organisations that already use IAM IC.

IAM permissions required for jitsudod:
- `sts_assume_role` mode: `sts:AssumeRole`
- `identity_center` mode: `sso:CreateAccountAssignment`, `sso:DeleteAccountAssignment`, `sso:ListAccountAssignments`, `identitystore:GetUserId`

### Azure

jitsudod creates and deletes time-bound Azure RBAC role assignments via the Azure ARM API using a service principal or managed identity (`CredentialsSource`). The client secret, if used, is read from the `JITSUDOD_AZURE_CLIENT_SECRET` environment variable — never from the config file.

Azure RBAC permissions required for the service principal:
- `Microsoft.Authorization/roleAssignments/write`
- `Microsoft.Authorization/roleAssignments/delete`
- `Microsoft.Authorization/roleAssignments/read`
- `Microsoft.Graph/User.Read.All` (for UPN → object ID resolution)

### GCP

jitsudod modifies the IAM policy of the target project/folder/org by adding or removing a conditional role binding. The condition uses `request.time < timestamp(...)` to express expiry natively in GCP IAM.

**Key constraint:** GCP IAM policies have a limit of 100 conditions per policy. The expiry sweeper must proactively clean up expired bindings to avoid hitting this limit on busy projects. Implementations must delete expired bindings rather than accumulating them.

GCP roles required for the jitsudod service account:
- `roles/resourcemanager.projectIamAdmin` (project level) or `roles/iam.securityAdmin` (broader)

### Kubernetes

jitsudod creates a `RoleBinding` (namespaced) or `ClusterRoleBinding` (cluster-wide) referencing an existing `ClusterRole`. The binding carries two annotations:
- `jitsudo.dev/expires-at: <RFC3339>` — when the grant expires
- `jitsudo.dev/request-id: <uuid>` — links back to the ElevationRequest

The expiry sweeper lists all bindings with the `ManagedLabel` label and deletes those past their `expires-at` annotation. No admission webhook or CRD is required in the OSS tier.

Kubernetes RBAC required for the jitsudod service account:
```yaml
rules:
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["rolebindings", "clusterrolebindings"]
    verbs: ["create", "delete", "get", "list", "watch"]
```

## Consequences

**Positive:**
- Config schemas are finalised before any SDK calls are written — no config churn mid-implementation
- YAML tags are locked; operator configs written today will load without change after implementation
- The `CredentialsSource` field makes the credential convention explicit without storing secrets in config
- GCP's 100-condition limit is documented here so the sweeper implementation does not discover it as a surprise

**Negative:**
- AWS's dual-mode design adds a runtime `switch` on `Mode` in `Grant` and `Revoke` — two code paths to test per operation
- `time.Duration` marshals as nanoseconds in default Go YAML libraries; operators must use string form (`8h`) which requires custom unmarshalling or a YAML library that handles it (e.g., `gopkg.in/yaml.v3` with a custom type)
