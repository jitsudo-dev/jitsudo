# Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
# SPDX-License-Identifier: Apache-2.0

variable "role_name" {
  description = "Name of the IAM role to create (e.g., \"jitsudo-readonly\")."
  type        = string
}

variable "jitsudod_role_arn" {
  description = "ARN of the IAM principal (role or user) that jitsudod runs as. This principal will be granted sts:AssumeRole and sts:TagSession on the elevation target role."
  type        = string
}

variable "managed_policy_arns" {
  description = "Managed IAM policy ARNs to attach to the elevation target role."
  type        = list(string)
  default     = []
}

variable "inline_policy_json" {
  description = "Optional inline IAM policy document (JSON string) granting the permissions users receive when elevated. Leave empty if using managed_policy_arns only."
  type        = string
  default     = ""
}

variable "max_session_duration" {
  description = "Maximum STS session duration in seconds (900–43200). Should be at least as long as your longest allowed jitsudo request duration."
  type        = number
  default     = 3600

  validation {
    condition     = var.max_session_duration >= 900 && var.max_session_duration <= 43200
    error_message = "max_session_duration must be between 900 (15 min) and 43200 (12 h)."
  }
}

variable "tags" {
  description = "Additional tags to apply to the IAM role."
  type        = map(string)
  default     = {}
}
