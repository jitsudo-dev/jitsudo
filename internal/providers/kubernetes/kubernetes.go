// Package kubernetes implements the jitsudo Provider interface for Kubernetes.
// It grants temporary elevated access by creating a ClusterRoleBinding or
// RoleBinding with a TTL, cleaned up by a CronJob or admission webhook.
//
// License: Apache 2.0
package kubernetes

import (
	"context"
	"fmt"
	"time"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
)

// Config holds Kubernetes provider configuration.
type Config struct {
	// KubeconfigPath is the path to the kubeconfig file.
	// If empty, in-cluster service account credentials are used (recommended for production).
	KubeconfigPath string `yaml:"kubeconfig"`

	// DefaultNamespace is used for namespaced RoleBindings when the request's
	// ResourceScope is empty. If also empty, a ClusterRoleBinding is created.
	DefaultNamespace string `yaml:"default_namespace"`

	// MaxDuration caps the elevation window the provider will honour.
	// If zero, no server-side cap is enforced.
	MaxDuration time.Duration `yaml:"max_duration"`

	// ManagedLabel is the label key applied to all jitsudo-created bindings.
	// The expiry sweeper uses this label to query and clean up expired bindings.
	// Defaults to "jitsudo.dev/managed" if empty.
	ManagedLabel string `yaml:"managed_label"`
}

// Provider is the Kubernetes implementation of providers.Provider.
type Provider struct {
	cfg Config
}

// New returns a new Kubernetes Provider with the given configuration.
func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

// Name returns "kubernetes".
func (p *Provider) Name() string {
	return "kubernetes"
}

// ValidateRequest validates that the request is well-formed for Kubernetes.
func (p *Provider) ValidateRequest(_ context.Context, req providers.ElevationRequest) error {
	if req.RequestID == "" {
		return fmt.Errorf("kubernetes: RequestID must not be empty")
	}
	if req.UserIdentity == "" {
		return fmt.Errorf("kubernetes: UserIdentity must not be empty")
	}
	if req.Duration <= 0 {
		return fmt.Errorf("kubernetes: Duration must be positive")
	}
	if req.RoleName == "" {
		return fmt.Errorf("kubernetes: RoleName must not be empty")
	}
	return nil
}

// Grant creates a ClusterRoleBinding or RoleBinding for the requester.
// TODO: implement using k8s.io/client-go
func (p *Provider) Grant(_ context.Context, req providers.ElevationRequest) (*providers.ElevationGrant, error) {
	return nil, fmt.Errorf("kubernetes: Grant not yet implemented")
}

// Revoke deletes the ClusterRoleBinding or RoleBinding.
// TODO: implement using k8s.io/client-go
func (p *Provider) Revoke(_ context.Context, grant providers.ElevationGrant) error {
	return fmt.Errorf("kubernetes: Revoke not yet implemented")
}

// IsActive checks whether the binding still exists in the cluster.
// TODO: implement using k8s.io/client-go
func (p *Provider) IsActive(_ context.Context, grant providers.ElevationGrant) (bool, error) {
	return false, fmt.Errorf("kubernetes: IsActive not yet implemented")
}
