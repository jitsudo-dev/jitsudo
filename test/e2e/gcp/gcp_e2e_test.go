//go:build e2e

// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package gcp_test

import (
	"testing"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/test/e2e/helpers"
)

// TestGCPLifecycle exercises the full JIT elevation lifecycle against a live GCP project:
//
//  1. Seed eligibility + auto-approval policies scoped to the test role
//  2. Create an elevation request and wait for ACTIVE
//  3. Retrieve credentials and verify GOOGLE_CLOUD_PROJECT is present
//  4. Run `gcloud projects get-iam-policy` to confirm the conditional binding was created
//  5. Revoke the request early and verify it reaches REVOKED
//
// Required env vars (any missing var skips the test):
//
//	E2E_JITSUDOD_URL               — gRPC address of a live jitsudod instance
//	E2E_JITSUDO_TOKEN              — bearer token for the test principal
//	E2E_GCP_ROLE                   — GCP IAM role to grant (e.g. roles/viewer)
//	E2E_GCP_PROJECT                — GCP project ID used as the resource scope
//	GOOGLE_APPLICATION_CREDENTIALS — path to a service account key file, or use ADC
func TestGCPLifecycle(t *testing.T) {
	role := helpers.Env(t, "E2E_GCP_ROLE")
	project := helpers.Env(t, "E2E_GCP_PROJECT")
	helpers.Env(t, "GOOGLE_APPLICATION_CREDENTIALS")

	c := helpers.NewClient(t)

	// 1. Seed policies.
	cleanup := helpers.ApplyE2EPolicy(t, c, "gcp", role)
	defer cleanup()

	// 2. Create request and wait for ACTIVE.
	requestID := helpers.CreateAndWaitActive(t, c, "gcp", role, project, 300)

	// 3. Retrieve credentials and verify GOOGLE_CLOUD_PROJECT is surfaced.
	creds := helpers.CredentialsMap(t, c, requestID)
	if creds["GOOGLE_CLOUD_PROJECT"] == "" {
		t.Errorf("expected credential GOOGLE_CLOUD_PROJECT to be present, got empty")
	}
	if creds["GOOGLE_CLOUD_PROJECT"] != project {
		t.Errorf("GOOGLE_CLOUD_PROJECT: got %q, want %q", creds["GOOGLE_CLOUD_PROJECT"], project)
	}

	// 4. Verify via gcloud that the conditional IAM binding exists.
	out := helpers.RunWithCreds(t, creds,
		"gcloud", "projects", "get-iam-policy", project,
		"--flatten=bindings[].members",
		"--format=value(bindings.role)",
		"--filter=bindings.condition.title=jitsudo-"+requestID,
	)
	if out == "" {
		t.Errorf("expected gcloud to return a binding with title jitsudo-%s, got empty output", requestID)
	}

	// 5. Revoke and verify REVOKED.
	helpers.Revoke(t, c, requestID)
	helpers.WaitForState(t, c, requestID, jitsudov1alpha1.RequestState_REQUEST_STATE_REVOKED)
}
