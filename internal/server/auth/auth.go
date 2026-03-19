// Package auth handles OIDC token validation and identity extraction for the
// jitsudod control plane. It validates Bearer tokens against the configured
// IdP's JWKS endpoint and extracts group membership from token claims.
//
// License: Elastic License 2.0 (ELv2)
package auth
