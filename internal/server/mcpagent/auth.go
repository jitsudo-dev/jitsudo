// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package mcpagent

import (
	"context"
	"net/http"
	"strings"

	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
)

// authVerifier validates OIDC Bearer tokens and returns the caller's Identity.
// *auth.Verifier satisfies this interface.
type authVerifier interface {
	Verify(ctx context.Context, rawToken string) (*auth.Identity, error)
}

// authenticate extracts a Bearer token from r, verifies it via v, and sets
// id.PrincipalType = auth.PrincipalTypeAgent. Returns nil and writes a 401 on
// failure.
func authenticate(w http.ResponseWriter, r *http.Request, v authVerifier) *auth.Identity {
	raw := r.Header.Get("Authorization")
	token := strings.TrimPrefix(raw, "Bearer ")
	if token == raw || token == "" {
		http.Error(w, "Unauthorized: Bearer token required", http.StatusUnauthorized)
		return nil
	}

	id, err := v.Verify(r.Context(), token)
	if err != nil {
		http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
		return nil
	}

	id.PrincipalType = auth.PrincipalTypeAgent
	return id
}
