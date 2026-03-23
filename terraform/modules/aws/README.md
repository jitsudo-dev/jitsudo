# terraform/modules/aws

Creates an IAM role that jitsudo can elevate users into via STS AssumeRole.

## What this module provisions

- An IAM role with a trust policy allowing jitsudod's identity to call `sts:AssumeRole` and `sts:TagSession`
- Optional managed policy attachments and/or an inline policy defining the elevated permissions

## jitsudod IAM requirements

jitsudod's own IAM principal (the role the server runs as) must separately have:

```json
{
  "Effect": "Allow",
  "Action": [
    "sts:AssumeRole",
    "iam:PutRolePolicy"
  ],
  "Resource": "arn:aws:iam::*:role/jitsudo-*"
}
```

`iam:PutRolePolicy` is required for session revocation (jitsudo attaches a time-bounded deny policy to invalidate a session early).

## Usage

```hcl
module "jitsudo_readonly" {
  source = "github.com/jitsudo-dev/jitsudo//terraform/modules/aws"

  role_name         = "jitsudo-readonly"
  jitsudod_role_arn = "arn:aws:iam::123456789012:role/jitsudod"

  managed_policy_arns = [
    "arn:aws:iam::aws:policy/ReadOnlyAccess",
  ]

  max_session_duration = 3600  # 1 hour

  tags = {
    environment = "production"
  }
}

output "role_arn" {
  value = module.jitsudo_readonly.role_arn
}
```

## jitsudod configuration

Set `role_arn_template` in your jitsudod provider config to match the naming convention used for your roles:

```yaml
providers:
  aws:
    mode: sts_assume_role
    region: us-east-1
    role_arn_template: "arn:aws:iam::{scope}:role/jitsudo-{role}"
```

With the example above, a request for `role=readonly` in `scope=123456789012` resolves to `arn:aws:iam::123456789012:role/jitsudo-readonly`.

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|----------|
| `role_name` | IAM role name | `string` | — | yes |
| `jitsudod_role_arn` | ARN of jitsudod's IAM principal | `string` | — | yes |
| `managed_policy_arns` | Managed policies to attach | `list(string)` | `[]` | no |
| `inline_policy_json` | Inline policy JSON | `string` | `""` | no |
| `max_session_duration` | Max session seconds (900–43200) | `number` | `3600` | no |
| `tags` | Additional resource tags | `map(string)` | `{}` | no |

## Outputs

| Name | Description |
|------|-------------|
| `role_arn` | ARN of the created IAM role |
| `role_name` | Name of the created IAM role |
