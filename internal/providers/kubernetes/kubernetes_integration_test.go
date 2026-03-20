//go:build integration

// Package kubernetes — integration tests using a real Kubernetes API server
// via controller-runtime's envtest (no Docker / kind required).
//
// Run with:
//
//	go test ./internal/providers/kubernetes/... -tags integration -v
//
// envtest requires the Kubernetes API server and etcd binaries.
// Install them with:
//
//	go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
//	setup-envtest use --bin-dir /usr/local/kubebuilder/bin
//	export KUBEBUILDER_ASSETS=/usr/local/kubebuilder/bin
//
// License: Apache 2.0
package kubernetes_test

import (
	"context"
	"os"
	"testing"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	k8sprovider "github.com/jitsudo-dev/jitsudo/internal/providers/kubernetes"
	"github.com/jitsudo-dev/jitsudo/pkg/types"
)

// kubeClientFromKubeconfig builds a Kubernetes client from raw kubeconfig bytes.
func kubeClientFromKubeconfig(t *testing.T, kubeconfig []byte) kubernetes.Interface {
	t.Helper()
	restCfg, err := clientcmd.BuildConfigFromKubeconfigGetter("", func() (*clientcmdapi.Config, error) {
		return clientcmd.Load(kubeconfig)
	})
	if err != nil {
		t.Fatalf("build kubeconfig: %v", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("new clientset: %v", err)
	}
	return cs
}

// ensureClusterRole creates a test ClusterRole if it doesn't exist.
func ensureClusterRole(t *testing.T, cs kubernetes.Interface, name string) {
	t.Helper()
	ctx := context.Background()
	_, err := cs.RbacV1().ClusterRoles().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return // already exists
	}
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
		},
	}
	if _, err := cs.RbacV1().ClusterRoles().Create(ctx, cr, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ClusterRole %q: %v", name, err)
	}
	t.Cleanup(func() {
		_ = cs.RbacV1().ClusterRoles().Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

func TestIntegration_KubernetesProvider_GrantRevokeIsActive(t *testing.T) {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		// Attempt in-cluster or default location.
		kubeconfigPath = os.Getenv("HOME") + "/.kube/config"
		if _, err := os.Stat(kubeconfigPath); err != nil {
			t.Skip("KUBECONFIG not set and ~/.kube/config not found — skipping kubernetes integration test")
		}
	}

	cfg := k8sprovider.Config{
		KubeconfigPath:   kubeconfigPath,
		DefaultNamespace: "default",
		MaxDuration:      types.Duration{Duration: 30 * time.Minute},
		ManagedLabel:     "jitsudo.dev/managed",
	}

	p, err := k8sprovider.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Ensure the target ClusterRole exists.
	kubeconfigBytes, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		t.Fatalf("read kubeconfig: %v", err)
	}
	cs := kubeClientFromKubeconfig(t, kubeconfigBytes)
	ensureClusterRole(t, cs, "view")

	ctx := context.Background()
	req := providers.ElevationRequest{
		RequestID:     "k8s-integ-test-001",
		UserIdentity:  "alice@example.com",
		Provider:      "kubernetes",
		RoleName:      "view",
		ResourceScope: "", // ClusterRoleBinding
		Duration:      30 * time.Minute,
		Reason:        "kubernetes integration test",
	}

	// Grant.
	grant, err := p.Grant(ctx, req)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if grant.RequestID != req.RequestID {
		t.Errorf("grant.RequestID = %q, want %q", grant.RequestID, req.RequestID)
	}

	// Verify the ClusterRoleBinding exists.
	if grant.RevokeToken == "" {
		t.Error("RevokeToken must not be empty")
	}

	// IsActive.
	active, err := p.IsActive(ctx, *grant)
	if err != nil {
		t.Fatalf("IsActive: %v", err)
	}
	if !active {
		t.Error("grant should be active immediately after Grant")
	}

	// Idempotent Grant.
	grant2, err := p.Grant(ctx, req)
	if err != nil {
		t.Fatalf("idempotent Grant: %v", err)
	}
	if grant2.RevokeToken != grant.RevokeToken {
		t.Error("idempotent Grant should return the same RevokeToken")
	}

	// Revoke.
	if err := p.Revoke(ctx, *grant); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// Idempotent Revoke.
	if err := p.Revoke(ctx, *grant); err != nil {
		t.Errorf("second Revoke should be idempotent, got: %v", err)
	}

	// IsActive after revocation.
	active, err = p.IsActive(ctx, *grant)
	if err != nil {
		t.Fatalf("IsActive after revoke: %v", err)
	}
	if active {
		t.Error("grant should be inactive after Revoke")
	}
}
