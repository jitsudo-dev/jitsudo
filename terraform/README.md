# Terraform Modules

Reusable Terraform modules for provisioning the cloud resources that jitsudo needs.

## Modules

| Module | Purpose |
|--------|---------|
| [`modules/aws`](modules/aws/) | IAM role with trust policy for STS elevation targets |
| [`modules/azure`](modules/azure/) | Azure AD service principal with RBAC and Graph permissions |
| [`modules/gcp`](modules/gcp/) | GCP service account with IAM admin on a project |

## License

Apache 2.0 — see [LICENSE](../LICENSE).
