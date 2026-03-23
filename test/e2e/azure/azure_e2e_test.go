//go:build e2e

// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package azure_test

import (
	"testing"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/test/e2e/helpers"
)

// TestAzureLifecycle exercises the full JIT elevation lifecycle against a live Azure subscription:
//
//  1. Seed eligibility + auto-approval policies scoped to the test role
//  2. Create an elevation request and wait for ACTIVE
//  3. Retrieve credentials and verify AZURE_SUBSCRIPTION_ID is present
//  4. Revoke the request early and verify it reaches REVOKED
//
// Required env vars (any missing var skips the test):
//
//	E2E_JITSUDOD_URL           — gRPC address of a live jitsudod instance
//	E2E_JITSUDO_TOKEN          — bearer token for the test principal
//	E2E_AZURE_ROLE             — Azure built-in or custom role name (e.g. Reader)
//	E2E_AZURE_SCOPE            — resource scope for the role assignment (e.g. /subscriptions/SUBSCRIPTION_ID)
//	AZURE_CLIENT_ID            — service principal used by jitsudod
//	AZURE_CLIENT_SECRET
//	AZURE_TENANT_ID
//	AZURE_SUBSCRIPTION_ID
func TestAzureLifecycle(t *testing.T) {
	role := helpers.Env(t, "E2E_AZURE_ROLE")
	scope := helpers.Env(t, "E2E_AZURE_SCOPE")
	helpers.Env(t, "AZURE_CLIENT_ID")
	helpers.Env(t, "AZURE_CLIENT_SECRET")
	helpers.Env(t, "AZURE_TENANT_ID")
	helpers.Env(t, "AZURE_SUBSCRIPTION_ID")

	c := helpers.NewClient(t)

	// 1. Seed policies.
	cleanup := helpers.ApplyE2EPolicy(t, c, "azure", role)
	defer cleanup()

	// 2. Create request and wait for ACTIVE.
	requestID := helpers.CreateAndWaitActive(t, c, "azure", role, scope, 300)

	// 3. Retrieve credentials and verify the subscription ID is surfaced.
	creds := helpers.CredentialsMap(t, c, requestID)
	if creds["AZURE_SUBSCRIPTION_ID"] == "" {
		t.Errorf("expected credential AZURE_SUBSCRIPTION_ID to be present, got empty")
	}

	// 4. Revoke and verify REVOKED.
	helpers.Revoke(t, c, requestID)
	helpers.WaitForState(t, c, requestID, jitsudov1alpha1.RequestState_REQUEST_STATE_REVOKED)
}
