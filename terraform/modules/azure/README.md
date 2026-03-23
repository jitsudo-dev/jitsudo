# terraform/modules/azure

Creates the Azure AD application, service principal, and RBAC role assignments that jitsudod needs to manage Azure role assignments on behalf of users.

## What this module provisions

- An Azure AD app registration and service principal for jitsudod
- Graph API `User.Read.All` application permission (with admin consent) — needed to resolve user principal names to Azure object IDs
- `User Access Administrator` role at the subscription (or resource group) scope — needed to create and delete role assignments
- A client secret (see note on workload identity below)

## Usage

```hcl
provider "azurerm" {
  features {}
  subscription_id = "00000000-0000-0000-0000-000000000000"
}

provider "azuread" {}

module "jitsudo_azure" {
  source = "github.com/jitsudo-dev/jitsudo//terraform/modules/azure"

  display_name    = "jitsudo"
  subscription_id = "00000000-0000-0000-0000-000000000000"
}

# Retrieve the client secret to configure jitsudod:
# terraform output -raw jitsudo_client_secret
output "jitsudo_client_secret" {
  value     = module.jitsudo_azure.client_secret
  sensitive = true
}
```

## jitsudod configuration

```yaml
providers:
  azure:
    tenant_id:               "<tenant_id output>"
    client_id:               "<client_id output>"
    default_subscription_id: "00000000-0000-0000-0000-000000000000"
    credentials_source:      client_secret   # or workload_identity
```

Set `AZURE_CLIENT_SECRET` in jitsudod's environment from the `client_secret` output.

## Production: workload identity federation

For AKS deployments, prefer workload identity over a client secret. After running this module, configure federated credentials on the app registration and set `credentials_source: workload_identity` in jitsudod's config. Remove the `azuread_service_principal_password` resource from your Terraform state once migrated.

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|----------|
| `display_name` | App registration display name | `string` | `"jitsudo"` | no |
| `subscription_id` | Azure subscription ID | `string` | — | yes |
| `grant_scope` | ARM scope for User Access Administrator | `string` | subscription | no |
| `client_secret_expiry` | Client secret relative expiry | `string` | `"8760h"` | no |

## Outputs

| Name | Description |
|------|-------------|
| `client_id` | Application (client) ID |
| `tenant_id` | Azure AD tenant ID |
| `object_id` | Service principal object ID |
| `client_secret` | Client secret (sensitive) |
