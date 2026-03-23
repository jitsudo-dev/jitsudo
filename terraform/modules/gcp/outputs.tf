# Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
# SPDX-License-Identifier: Apache-2.0

output "service_account_email" {
  description = "Email address of the GCP service account created for jitsudod. Use as credentials_source: application_default with Workload Identity, or export a key for other deployments."
  value       = google_service_account.jitsudod.email
}

output "service_account_id" {
  description = "Unique numeric ID of the GCP service account."
  value       = google_service_account.jitsudod.unique_id
}
