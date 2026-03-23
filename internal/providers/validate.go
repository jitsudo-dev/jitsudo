// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package providers

import "fmt"

// ValidateCommon checks the fields required by every provider implementation.
// Individual providers should call this first, then add provider-specific checks.
func ValidateCommon(req ElevationRequest) error {
	if req.RequestID == "" {
		return fmt.Errorf("providers: RequestID must not be empty")
	}
	if req.UserIdentity == "" {
		return fmt.Errorf("providers: UserIdentity must not be empty")
	}
	if req.Duration <= 0 {
		return fmt.Errorf("providers: Duration must be positive")
	}
	return nil
}
