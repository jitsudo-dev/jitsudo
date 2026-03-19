# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Previous minor release | Yes (critical fixes only) |
| Older versions | No |

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

To report a vulnerability, please email **security@jitsudo.dev** with:

- A description of the vulnerability
- Steps to reproduce
- Potential impact assessment
- Any suggested mitigations (optional)

You will receive an acknowledgement within 48 hours. We aim to provide a fix or mitigation within 90 days for critical issues.

## Disclosure Policy

We follow coordinated disclosure. We will:

1. Acknowledge your report within 48 hours
2. Investigate and reproduce the issue
3. Develop and test a fix
4. Release the fix and publish a security advisory
5. Credit you in the advisory (unless you prefer anonymity)

## Security Design Principles

jitsudo is a privileged access management tool. Security is foundational, not an afterthought:

- **No standing credentials** — jitsudo grants time-limited credentials only; it does not store long-lived secrets
- **Tamper-evident audit log** — every action is logged with a SHA-256 hash chain
- **OIDC-only authentication** — no custom auth; delegates entirely to the operator's IdP
- **Least-privilege service accounts** — the `jitsudod` service account is granted only the minimum IAM permissions required per provider
- **mTLS for server-to-server** — all internal communication uses mutual TLS in HA deployments
- **Write-ahead audit logging** — audit log entries are written before state transitions to prevent gaps

## Known Limitations (OSS Tier)

- Session recording is not available in the open source tier
- SIEM integration connectors are enterprise features
- The break-glass mode bypasses approval workflows by design — ensure break-glass eligibility policies are appropriately restrictive
