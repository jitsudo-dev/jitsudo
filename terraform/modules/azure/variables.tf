# Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
# SPDX-License-Identifier: Apache-2.0

variable "display_name" {
  description = "Display name for the Azure AD application and service principal."
  type        = string
  default     = "jitsudo"
}

variable "subscription_id" {
  description = "Azure subscription ID where jitsudod will create and delete role assignments."
  type        = string
}

variable "grant_scope" {
  description = "ARM scope at which jitsudod is granted User Access Administrator. Defaults to the full subscription scope (/subscriptions/<id>). Use a resource group scope to restrict coverage."
  type        = string
  default     = ""
}

variable "client_secret_expiry" {
  description = "Expiry for the generated client secret, as a relative duration (e.g., \"8760h\" for 1 year). Rotate before expiry or switch to workload identity federation for production."
  type        = string
  default     = "8760h"
}
