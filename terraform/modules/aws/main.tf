# Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
# SPDX-License-Identifier: Apache-2.0
#
# Creates an IAM role that jitsudo can elevate users into.
#
# jitsudod's own IAM principal (var.jitsudod_role_arn) must additionally have:
#   sts:AssumeRole on this role  — handled by the trust policy below
#   iam:PutRolePolicy on this role — required for session revocation
# Attach those permissions to jitsudod's role separately.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}

data "aws_iam_policy_document" "trust" {
  statement {
    effect = "Allow"

    principals {
      type        = "AWS"
      identifiers = [var.jitsudod_role_arn]
    }

    # sts:TagSession is required for jitsudo's session tagging audit trail.
    actions = [
      "sts:AssumeRole",
      "sts:TagSession",
    ]
  }
}

resource "aws_iam_role" "this" {
  name                 = var.role_name
  assume_role_policy   = data.aws_iam_policy_document.trust.json
  max_session_duration = var.max_session_duration

  tags = merge(var.tags, {
    "jitsudo:managed" = "true"
  })
}

resource "aws_iam_role_policy_attachment" "managed" {
  for_each   = toset(var.managed_policy_arns)
  role       = aws_iam_role.this.name
  policy_arn = each.value
}

resource "aws_iam_role_policy" "inline" {
  count  = var.inline_policy_json != "" ? 1 : 0
  role   = aws_iam_role.this.id
  name   = "${var.role_name}-inline"
  policy = var.inline_policy_json
}
