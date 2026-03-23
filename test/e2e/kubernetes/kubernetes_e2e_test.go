//go:build e2e

// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package kubernetes_test

import (
	"strings"
	"testing"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/test/e2e/helpers"
)

// TestKubernetesLifecycle exercises the full JIT elevation lifecycle against a live Kubernetes cluster:
//
//  1. Seed eligibility + auto-approval policies scoped to the test ClusterRole
//  2. Create an elevation request and wait for ACTIVE
//  3. Retrieve credentials and verify JITSUDO_K8S_NAMESPACE is present
//  4. Run `kubectl auth can-i get pods` in the namespace and assert "yes"
//  5. Revoke the request early and verify it reaches REVOKED
//
// Required env vars (any missing var skips the test):
//
//	E2E_JITSUDOD_URL       — gRPC address of a live jitsudod instance
//	E2E_JITSUDO_TOKEN      — bearer token for the test principal
//	E2E_K8S_CLUSTER_ROLE   — Kubernetes ClusterRole to bind (e.g. view)
//	E2E_K8S_NAMESPACE      — namespace used as the resource scope
//	KUBECONFIG             — path to kubeconfig file, or use in-cluster config
func TestKubernetesLifecycle(t *testing.T) {
	clusterRole := helpers.Env(t, "E2E_K8S_CLUSTER_ROLE")
	namespace := helpers.Env(t, "E2E_K8S_NAMESPACE")
	helpers.Env(t, "KUBECONFIG")

	c := helpers.NewClient(t)

	// 1. Seed policies.
	cleanup := helpers.ApplyE2EPolicy(t, c, "kubernetes", clusterRole)
	defer cleanup()

	// 2. Create request and wait for ACTIVE.
	requestID := helpers.CreateAndWaitActive(t, c, "kubernetes", clusterRole, namespace, 300)

	// 3. Retrieve credentials and verify JITSUDO_K8S_NAMESPACE is present.
	creds := helpers.CredentialsMap(t, c, requestID)
	if creds["JITSUDO_K8S_NAMESPACE"] == "" {
		t.Errorf("expected credential JITSUDO_K8S_NAMESPACE to be present, got empty")
	}
	if creds["JITSUDO_K8S_NAMESPACE"] != namespace {
		t.Errorf("JITSUDO_K8S_NAMESPACE: got %q, want %q", creds["JITSUDO_K8S_NAMESPACE"], namespace)
	}
	if creds["JITSUDO_K8S_ROLE"] != clusterRole {
		t.Errorf("JITSUDO_K8S_ROLE: got %q, want %q", creds["JITSUDO_K8S_ROLE"], clusterRole)
	}

	// 4. Verify via kubectl that the binding grants the expected access.
	out := helpers.RunWithCreds(t, creds,
		"kubectl", "auth", "can-i", "get", "pods",
		"--namespace", namespace,
	)
	if !strings.Contains(strings.TrimSpace(out), "yes") {
		t.Errorf("expected kubectl auth can-i get pods to return \"yes\", got %q", out)
	}

	// 5. Revoke and verify REVOKED.
	helpers.Revoke(t, c, requestID)
	helpers.WaitForState(t, c, requestID, jitsudov1alpha1.RequestState_REQUEST_STATE_REVOKED)
}
