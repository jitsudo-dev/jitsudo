# Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
# SPDX-License-Identifier: Apache-2.0

output "role_arn" {
  description = "ARN of the IAM elevation target role. Use this as the value for jitsudod's role_arn_template, e.g.: \"arn:aws:iam::{scope}:role/jitsudo-{role}\"."
  value       = aws_iam_role.this.arn
}

output "role_name" {
  description = "Name of the IAM elevation target role."
  value       = aws_iam_role.this.name
}
