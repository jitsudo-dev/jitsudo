//go:build e2e

// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

// Package helpers provides shared utilities for jitsudo E2E tests.
// All functions connect to a live jitsudod instance; set E2E_JITSUDOD_URL and
// E2E_JITSUDO_TOKEN before running.
package helpers

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/pkg/client"
)

// Env returns the value of key. If the variable is empty, t.Skip is called with
// a descriptive message so the test is silently skipped rather than failing.
func Env(t testing.TB, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("skipping: %s is not set (see test/e2e/.env.e2e.example)", key)
	}
	return v
}

// NewClient creates an authenticated jitsudo gRPC client from E2E_JITSUDOD_URL
// and E2E_JITSUDO_TOKEN. The connection is closed via t.Cleanup.
// Set E2E_INSECURE=true to skip TLS verification (useful for local jitsudod).
func NewClient(t testing.TB) *client.Client {
	t.Helper()
	serverURL := Env(t, "E2E_JITSUDOD_URL")
	token := Env(t, "E2E_JITSUDO_TOKEN")

	c, err := client.New(context.Background(), client.Config{
		ServerURL: serverURL,
		Token:     token,
		Insecure:  os.Getenv("E2E_INSECURE") == "true",
	})
	if err != nil {
		t.Fatalf("helpers.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// ApplyE2EPolicy creates a scoped eligibility + auto-approval policy pair for the
// given provider and role. Returns a cleanup function that deletes both policies;
// register it with defer or t.Cleanup.
func ApplyE2EPolicy(t testing.TB, c *client.Client, provider, role string) func() {
	t.Helper()
	ctx := context.Background()
	svc := c.Service()

	eligName := "e2e-eligibility-" + provider + "-" + role
	approvalName := "e2e-approval-" + provider + "-" + role

	eligRego := `package jitsudo.eligibility
allow if {
    input.request.provider == "` + provider + `"
    input.request.role == "` + role + `"
}`

	approvalRego := `package jitsudo.approval
approver_tier := "auto" if {
    input.request.provider == "` + provider + `"
    input.request.role == "` + role + `"
}`

	eligResp, err := svc.ApplyPolicy(ctx, &jitsudov1alpha1.ApplyPolicyInput{
		Name:        eligName,
		Type:        jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY,
		Rego:        eligRego,
		Description: "E2E test eligibility policy — deleted after test run",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("ApplyE2EPolicy: eligibility: %v", err)
	}

	approvalResp, err := svc.ApplyPolicy(ctx, &jitsudov1alpha1.ApplyPolicyInput{
		Name:        approvalName,
		Type:        jitsudov1alpha1.PolicyType_POLICY_TYPE_APPROVAL,
		Rego:        approvalRego,
		Description: "E2E test approval policy — deleted after test run",
		Enabled:     true,
	})
	if err != nil {
		svc.DeletePolicy(ctx, &jitsudov1alpha1.DeletePolicyInput{Id: eligResp.Policy.Id}) //nolint:errcheck
		t.Fatalf("ApplyE2EPolicy: approval: %v", err)
	}

	return func() {
		svc.DeletePolicy(ctx, &jitsudov1alpha1.DeletePolicyInput{Id: eligResp.Policy.Id})     //nolint:errcheck
		svc.DeletePolicy(ctx, &jitsudov1alpha1.DeletePolicyInput{Id: approvalResp.Policy.Id}) //nolint:errcheck
	}
}

// CreateAndWaitActive submits an elevation request and polls until it reaches
// REQUEST_STATE_ACTIVE (up to 30 seconds). Returns the request ID.
func CreateAndWaitActive(t testing.TB, c *client.Client, provider, role, scope string, durationSecs int64) string {
	t.Helper()
	ctx := context.Background()
	svc := c.Service()

	resp, err := svc.CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
		Provider:        provider,
		Role:            role,
		ResourceScope:   scope,
		DurationSeconds: durationSecs,
		Reason:          "E2E automated test",
	})
	if err != nil {
		t.Fatalf("CreateAndWaitActive: CreateRequest: %v", err)
	}
	requestID := resp.Request.Id

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		getResp, err := svc.GetRequest(ctx, &jitsudov1alpha1.GetRequestInput{Id: requestID})
		if err != nil {
			t.Fatalf("CreateAndWaitActive: GetRequest %s: %v", requestID, err)
		}
		switch getResp.Request.State {
		case jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE:
			return requestID
		case jitsudov1alpha1.RequestState_REQUEST_STATE_REJECTED:
			t.Fatalf("CreateAndWaitActive: request %s was rejected — check eligibility/approval policies", requestID)
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("CreateAndWaitActive: request %s did not reach ACTIVE within 30s", requestID)
	return "" // unreachable
}

// CredentialsMap calls GetCredentials and returns a map of credential name → value.
func CredentialsMap(t testing.TB, c *client.Client, requestID string) map[string]string {
	t.Helper()
	resp, err := c.Service().GetCredentials(context.Background(), &jitsudov1alpha1.GetCredentialsInput{
		RequestId: requestID,
	})
	if err != nil {
		t.Fatalf("CredentialsMap: GetCredentials %s: %v", requestID, err)
	}
	m := make(map[string]string, len(resp.Grant.Credentials))
	for _, cred := range resp.Grant.Credentials {
		m[cred.Name] = cred.Value
	}
	return m
}

// Revoke issues an early revocation for the given request.
func Revoke(t testing.TB, c *client.Client, requestID string) {
	t.Helper()
	_, err := c.Service().RevokeRequest(context.Background(), &jitsudov1alpha1.RevokeRequestInput{
		RequestId: requestID,
		Reason:    "E2E test cleanup",
	})
	if err != nil {
		t.Errorf("Revoke: RevokeRequest %s: %v", requestID, err)
	}
}

// WaitForState polls until the request reaches the expected state (up to 15s).
func WaitForState(t testing.TB, c *client.Client, requestID string, want jitsudov1alpha1.RequestState) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := c.Service().GetRequest(ctx, &jitsudov1alpha1.GetRequestInput{Id: requestID})
		if err != nil {
			t.Fatalf("WaitForState: GetRequest %s: %v", requestID, err)
		}
		if resp.Request.State == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Errorf("WaitForState: request %s did not reach %v within 15s", requestID, want)
}

// RunWithCreds executes cmd with the current process environment supplemented
// by the given credential map (credentials override any existing env vars of
// the same name). Returns combined stdout+stderr output.
func RunWithCreds(t testing.TB, creds map[string]string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	for k, v := range creds {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("RunWithCreds: %s %v: %v\noutput:\n%s", name, args, err, out)
	}
	return string(out)
}
