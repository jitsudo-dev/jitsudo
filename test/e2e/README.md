# E2E Tests

End-to-end tests for jitsudo that exercise the full JIT elevation lifecycle against live cloud accounts.

## Overview

Each provider test:
1. Seeds a scoped eligibility + auto-approval policy pair via the jitsudo API
2. Creates an elevation request and waits for `ACTIVE` state
3. Retrieves the JIT credentials and verifies they are correct
4. For AWS, runs `aws sts get-caller-identity` to validate the assumed role ARN
5. For GCP, runs `gcloud projects get-iam-policy` to validate the conditional binding
6. For Kubernetes, runs `kubectl auth can-i` to validate the binding
7. Revokes the request and waits for `REVOKED` state
8. Cleans up the test policies on exit

All tests carry the `//go:build e2e` build tag and **never run** during `make test` or `make test-integration`.

## Prerequisites

- A running jitsudod instance reachable from your machine
- Cloud credentials with sufficient permissions (see per-provider requirements below)
- The relevant cloud CLIs installed: `aws`, `az`, `gcloud`, `kubectl`

## Setup

```sh
cp test/e2e/.env.e2e.example test/e2e/.env.e2e
# Edit .env.e2e and fill in real values — this file is gitignored
source test/e2e/.env.e2e
```

## Running

Run all providers:

```sh
E2E_ENABLED=true make test-e2e
```

Run a single provider:

```sh
E2E_ENABLED=true go test -tags e2e -v ./test/e2e/aws/...
E2E_ENABLED=true go test -tags e2e -v ./test/e2e/azure/...
E2E_ENABLED=true go test -tags e2e -v ./test/e2e/gcp/...
E2E_ENABLED=true go test -tags e2e -v ./test/e2e/kubernetes/...
```

Any provider whose required env vars are absent is **skipped** (not failed).

## Required environment variables

### Core (all providers)

| Variable | Description |
|---|---|
| `E2E_JITSUDOD_URL` | gRPC address of a live jitsudod instance (e.g. `https://jitsudo.example.com:8443`) |
| `E2E_JITSUDO_TOKEN` | Bearer token for the test principal |
| `E2E_INSECURE` | Set to `true` to skip TLS verification (local dev only) |

### AWS

| Variable | Description |
|---|---|
| `E2E_AWS_ROLE_ARN` | ARN of the IAM role jitsudod should assume |
| `E2E_AWS_SCOPE` | Resource scope forwarded in the elevation request |
| `AWS_ACCESS_KEY_ID` | IAM user/role credentials for jitsudod |
| `AWS_SECRET_ACCESS_KEY` | |
| `AWS_DEFAULT_REGION` | |

Use the [terraform/modules/aws](../../terraform/modules/aws/) module to provision the test IAM role and output its ARN.

### Azure

| Variable | Description |
|---|---|
| `E2E_AZURE_ROLE` | Azure built-in or custom role name (e.g. `Reader`) |
| `E2E_AZURE_SCOPE` | Resource scope for the assignment (e.g. `/subscriptions/UUID`) |
| `AZURE_CLIENT_ID` | Service principal client ID |
| `AZURE_CLIENT_SECRET` | Service principal secret |
| `AZURE_TENANT_ID` | Azure AD tenant ID |
| `AZURE_SUBSCRIPTION_ID` | Target subscription |

Use the [terraform/modules/azure](../../terraform/modules/azure/) module to provision the service principal.

### GCP

| Variable | Description |
|---|---|
| `E2E_GCP_ROLE` | IAM role to grant (e.g. `roles/viewer`) |
| `E2E_GCP_PROJECT` | GCP project ID (used as the resource scope) |
| `GOOGLE_APPLICATION_CREDENTIALS` | Path to service account key file, or use ADC |

Use the [terraform/modules/gcp](../../terraform/modules/gcp/) module to provision the service account.

### Kubernetes

| Variable | Description |
|---|---|
| `E2E_K8S_CLUSTER_ROLE` | ClusterRole to bind (e.g. `view`) |
| `E2E_K8S_NAMESPACE` | Namespace used as the resource scope |
| `KUBECONFIG` | Path to kubeconfig (omit for in-cluster or `~/.kube/config`) |

## Credential safety

- **Never commit `.env.e2e`** — it is listed in `.gitignore`
- **Never commit `test/e2e/testdata/`** — also gitignored
- All secrets flow via environment variables; no credentials appear in source code
- Tests skip gracefully with a descriptive message when required env vars are absent
- The `//go:build e2e` tag ensures E2E files are excluded from standard builds and `go test ./...`
