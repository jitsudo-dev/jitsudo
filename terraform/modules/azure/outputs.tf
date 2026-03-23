# Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
# SPDX-License-Identifier: Apache-2.0

output "client_id" {
  description = "Client (application) ID of the jitsudod service principal. Maps to client_id in jitsudod's Azure provider config."
  value       = azuread_application.jitsudod.client_id
}

output "tenant_id" {
  description = "Azure AD tenant ID. Maps to tenant_id in jitsudod's Azure provider config."
  value       = data.azuread_client_config.current.tenant_id
}

output "object_id" {
  description = "Object ID of the jitsudod service principal."
  value       = azuread_service_principal.jitsudod.object_id
}

output "client_secret" {
  description = "Client secret for the jitsudod service principal. Sensitive — store in a secrets manager, not in source control."
  value       = azuread_service_principal_password.jitsudod.value
  sensitive   = true
}
