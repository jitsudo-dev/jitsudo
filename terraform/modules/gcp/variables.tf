# Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
# SPDX-License-Identifier: Apache-2.0

variable "project_id" {
  description = "GCP project ID where jitsudod will manage IAM bindings."
  type        = string
}

variable "service_account_id" {
  description = "Service account ID for jitsudod. The full email will be <id>@<project>.iam.gserviceaccount.com."
  type        = string
  default     = "jitsudo"
}

variable "display_name" {
  description = "Human-readable display name for the service account."
  type        = string
  default     = "jitsudo control plane"
}
