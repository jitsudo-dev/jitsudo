//go:build e2e

// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package aws_test

import (
	"encoding/json"
	"strings"
	"testing"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/test/e2e/helpers"
)

// TestAWSLifecycle exercises the full JIT elevation lifecycle against a live AWS account:
//
//  1. Seed eligibility + auto-approval policies scoped to the test role
//  2. Create an elevation request and wait for ACTIVE
//  3. Retrieve credentials and verify AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY are present
//  4. Run `aws sts get-caller-identity` and assert the assumed RoleArn matches
//  5. Revoke the request early and verify it reaches REVOKED
//
// Required env vars (any missing var skips the test):
//
//	E2E_JITSUDOD_URL     — gRPC address of a live jitsudod instance
//	E2E_JITSUDO_TOKEN    — bearer token for the test principal
//	E2E_AWS_ROLE_ARN     — ARN of the IAM role jitsudod should assume (e.g. arn:aws:iam::123:role/e2e)
//	E2E_AWS_SCOPE        — resource scope forwarded to jitsudod (e.g. arn:aws:iam::123456789012:root)
//	AWS_ACCESS_KEY_ID    — credentials that allow jitsudod to assume E2E_AWS_ROLE_ARN
//	AWS_SECRET_ACCESS_KEY
//	AWS_DEFAULT_REGION
func TestAWSLifecycle(t *testing.T) {
	roleARN := helpers.Env(t, "E2E_AWS_ROLE_ARN")
	scope := helpers.Env(t, "E2E_AWS_SCOPE")
	helpers.Env(t, "AWS_ACCESS_KEY_ID")
	helpers.Env(t, "AWS_SECRET_ACCESS_KEY")
	helpers.Env(t, "AWS_DEFAULT_REGION")

	c := helpers.NewClient(t)

	// Extract role name from ARN (last path component).
	parts := strings.Split(roleARN, "/")
	roleName := parts[len(parts)-1]

	// 1. Seed policies.
	cleanup := helpers.ApplyE2EPolicy(t, c, "aws", roleName)
	defer cleanup()

	// 2. Create request and wait for ACTIVE.
	requestID := helpers.CreateAndWaitActive(t, c, "aws", roleName, scope, 300)

	// 3. Retrieve credentials.
	creds := helpers.CredentialsMap(t, c, requestID)
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		if creds[key] == "" {
			t.Errorf("expected credential %s to be present, got empty", key)
		}
	}

	// 4. Verify by calling aws sts get-caller-identity with the JIT credentials.
	out := helpers.RunWithCreds(t, creds, "aws", "sts", "get-caller-identity")

	var identity struct {
		Arn string `json:"Arn"`
	}
	if err := json.Unmarshal([]byte(out), &identity); err != nil {
		t.Fatalf("aws sts get-caller-identity: failed to parse JSON: %v\noutput:\n%s", err, out)
	}
	// The assumed-role ARN looks like arn:aws:sts::ACCOUNT:assumed-role/ROLE/SESSION.
	if !strings.Contains(identity.Arn, roleName) {
		t.Errorf("expected assumed-role ARN to contain %q, got %q", roleName, identity.Arn)
	}

	// 5. Revoke and verify REVOKED.
	helpers.Revoke(t, c, requestID)
	helpers.WaitForState(t, c, requestID, jitsudov1alpha1.RequestState_REQUEST_STATE_REVOKED)
}
