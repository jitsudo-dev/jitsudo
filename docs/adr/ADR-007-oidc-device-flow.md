# ADR-007: OIDC Device Flow for CLI Authentication

**Status:** Accepted
**Date:** 2026-03-18

## Context

The jitsudo CLI needs to authenticate users against the configured IdP. SREs frequently work in headless environments: SSH sessions, CI/CD pipelines, Docker containers, remote servers without a browser. The authentication flow must work in all these contexts.

Options considered: browser-based OIDC Authorization Code flow with redirect, OIDC Device Authorization Flow (RFC 8628), API key / long-lived token, username/password (Basic Auth).

## Decision

The CLI uses the OIDC Device Authorization Flow (RFC 8628) for `jitsudo login`.

## Consequences

**Positive:**
- Works in any environment — no browser required on the authenticating machine; the user opens a URL on any device (phone, laptop) to complete authentication
- Fully delegated to the IdP — no passwords or secrets stored by jitsudo
- Supports all major IdPs: Okta, Microsoft Entra ID, Google Workspace, Keycloak
- Short-lived ID tokens (JWTs) are used for API authentication; refresh tokens allow transparent renewal without re-login
- Compatible with MFA — the IdP handles MFA challenges as part of the device flow

**Negative:**
- Requires the IdP to support device flow; most major IdPs do, but some older enterprise IdPs may not
- Requires a public client registration in the IdP (no client secret for the CLI)
- The user must complete the device flow on a separate device or browser, which adds friction compared to a browser redirect

**Why not browser redirect (Authorization Code flow):**
Browser redirect requires a local HTTP server to receive the OAuth callback. This doesn't work in headless/SSH environments without port forwarding and is fragile in corporate network environments with proxies and firewalls.

**Why not API keys:**
API keys are long-lived credentials that accumulate and are rarely rotated. They are the opposite of jitsudo's core philosophy (time-limited, audited access). Device flow tokens integrate naturally with the IdP's session management and MFA policies.
