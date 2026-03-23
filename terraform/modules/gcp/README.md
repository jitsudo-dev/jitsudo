# terraform/modules/gcp

Creates a GCP service account for jitsudod and grants it permission to manage IAM conditional role bindings on a GCP project.

## What this module provisions

- A GCP service account for jitsudod
- `roles/resourcemanager.projectIamAdmin` on the specified project — covers `resourcemanager.projects.getIamPolicy` and `resourcemanager.projects.setIamPolicy`, the only permissions jitsudod requires

## Usage

```hcl
provider "google" {
  project = "my-gcp-project"
}

module "jitsudo_gcp" {
  source = "github.com/jitsudo-dev/jitsudo//terraform/modules/gcp"

  project_id         = "my-gcp-project"
  service_account_id = "jitsudo"
  display_name       = "jitsudo control plane"
}

output "service_account_email" {
  value = module.jitsudo_gcp.service_account_email
}
```

## jitsudod configuration

```yaml
providers:
  gcp:
    credentials_source: application_default  # or service_account_key
```

For GKE deployments, use Workload Identity Federation to bind the service account to jitsudod's Kubernetes service account — no key file needed.

For non-GKE deployments, export a key:

```sh
gcloud iam service-accounts keys create jitsudod-key.json \
  --iam-account="$(terraform output -raw service_account_email)"
```

Then set `GOOGLE_APPLICATION_CREDENTIALS=/path/to/jitsudod-key.json` in jitsudod's environment.

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|----------|
| `project_id` | GCP project ID | `string` | — | yes |
| `service_account_id` | Service account ID | `string` | `"jitsudo"` | no |
| `display_name` | Service account display name | `string` | `"jitsudo control plane"` | no |

## Outputs

| Name | Description |
|------|-------------|
| `service_account_email` | Service account email address |
| `service_account_id` | Service account numeric ID |
