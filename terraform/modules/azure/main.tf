# Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
# SPDX-License-Identifier: Apache-2.0
#
# Creates the Azure AD application, service principal, and role assignments
# that jitsudod needs to manage RBAC role assignments on behalf of users.

terraform {
  required_version = ">= 1.5"
  required_providers {
    azuread = {
      source  = "hashicorp/azuread"
      version = ">= 2.50"
    }
    azurerm = {
      source  = "hashicorp/azurerm"
      version = ">= 3.100"
    }
  }
}

locals {
  grant_scope = var.grant_scope != "" ? var.grant_scope : "/subscriptions/${var.subscription_id}"
}

data "azuread_client_config" "current" {}

# App registration for jitsudod.
resource "azuread_application" "jitsudod" {
  display_name = var.display_name

  # Microsoft Graph: User.Read.All (Application permission)
  # Required for resolving user principal names to Azure object IDs.
  required_resource_access {
    resource_app_id = "00000003-0000-0000-c000-000000000000" # Microsoft Graph

    resource_access {
      id   = "df021288-bdef-4463-88db-98f22de89214" # User.Read.All
      type = "Role"
    }
  }
}

resource "azuread_service_principal" "jitsudod" {
  client_id = azuread_application.jitsudod.client_id
}

# Client secret. For production, prefer workload identity federation.
# Note: this value is stored in Terraform state. Protect state storage accordingly.
resource "azuread_service_principal_password" "jitsudod" {
  service_principal_id = azuread_service_principal.jitsudod.id
  end_date_relative    = var.client_secret_expiry
}

# Grant admin consent for User.Read.All so jitsudod can look up user object IDs.
data "azuread_service_principal" "graph" {
  client_id = "00000003-0000-0000-c000-000000000000" # Microsoft Graph
}

resource "azuread_app_role_assignment" "graph_user_read_all" {
  app_role_id         = "df021288-bdef-4463-88db-98f22de89214" # User.Read.All
  principal_object_id = azuread_service_principal.jitsudod.object_id
  resource_object_id  = data.azuread_service_principal.graph.object_id
}

# User Access Administrator grants Microsoft.Authorization/roleAssignments/write
# and /delete, which are the only ARM permissions jitsudod requires.
data "azurerm_role_definition" "user_access_administrator" {
  name = "User Access Administrator"
}

resource "azurerm_role_assignment" "jitsudod" {
  scope              = local.grant_scope
  role_definition_id = "${local.grant_scope}${data.azurerm_role_definition.user_access_administrator.id}"
  principal_id       = azuread_service_principal.jitsudod.object_id
}
