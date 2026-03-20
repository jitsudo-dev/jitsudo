// Package kubernetes implements the jitsudo Provider interface for Kubernetes.
// It grants temporary elevated access by creating a ClusterRoleBinding or
// RoleBinding with a TTL annotation, cleaned up by the jitsudod expiry sweeper.
//
// License: Apache 2.0
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	"github.com/jitsudo-dev/jitsudo/pkg/types"
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
	MaxDuration types.Duration `yaml:"max_duration"`

	// ManagedLabel is the label key applied to all jitsudo-created bindings.
	// The expiry sweeper uses this label to query and clean up expired bindings.
	// Defaults to "jitsudo.dev/managed" if empty.
	ManagedLabel string `yaml:"managed_label"`
}

// Provider is the Kubernetes implementation of providers.Provider.
type Provider struct {
	cfg       Config
	clientset kubernetes.Interface
}

// New returns a new Kubernetes Provider, building the client from KubeconfigPath
// or falling back to in-cluster service account credentials.
func New(cfg Config) (*Provider, error) {
	var restCfg *rest.Config
	var err error

	if cfg.KubeconfigPath != "" {
		restCfg, err = clientcmd.BuildConfigFromFlags("", cfg.KubeconfigPath)
	} else {
		restCfg, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("kubernetes: build config: %w", err)
	}

	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: new clientset: %w", err)
	}
	return &Provider{cfg: cfg, clientset: cs}, nil
}

// NewWithClientset returns a Provider using the given clientset — intended for tests.
func NewWithClientset(cfg Config, cs kubernetes.Interface) *Provider {
	return &Provider{cfg: cfg, clientset: cs}
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

// Grant creates a ClusterRoleBinding (when ResourceScope is empty or "*") or
// a namespaced RoleBinding (when ResourceScope is a namespace name).
// The binding is annotated with the expiry time and request metadata.
// Idempotent: if a binding with the same jitsudo.dev/request-id annotation
// already exists, the existing grant is reconstructed and returned.
func (p *Provider) Grant(ctx context.Context, req providers.ElevationRequest) (*providers.ElevationGrant, error) {
	if err := p.ValidateRequest(ctx, req); err != nil {
		return nil, err
	}

	dur := req.Duration
	if p.cfg.MaxDuration.Duration > 0 && dur > p.cfg.MaxDuration.Duration {
		dur = p.cfg.MaxDuration.Duration
	}

	now := time.Now().UTC()
	expiresAt := now.Add(dur)
	bindingName := p.bindingName(req.RequestID)
	namespace := p.namespace(req.ResourceScope)
	isNamespaced := namespace != ""

	annotations := map[string]string{
		"jitsudo.dev/expires-at": expiresAt.Format(time.RFC3339),
		"jitsudo.dev/request-id": req.RequestID,
		"jitsudo.dev/user":       req.UserIdentity,
	}
	labels := map[string]string{
		p.managedLabel(): "true",
	}
	subject := rbacv1.Subject{
		Kind:     "User",
		Name:     req.UserIdentity,
		APIGroup: "rbac.authorization.k8s.io",
	}
	roleRef := rbacv1.RoleRef{
		Kind:     "ClusterRole",
		Name:     req.RoleName,
		APIGroup: "rbac.authorization.k8s.io",
	}

	token := k8sRevokeToken{Name: bindingName, Namespace: namespace}
	credentials := map[string]string{
		"JITSUDO_K8S_ROLE":      req.RoleName,
		"JITSUDO_K8S_NAMESPACE": req.ResourceScope,
	}

	if isNamespaced {
		token.Kind = "RoleBinding"
		existing, err := p.clientset.RbacV1().RoleBindings(namespace).Get(ctx, bindingName, metav1.GetOptions{})
		if err == nil {
			// Binding exists — reconstruct grant from its annotations.
			tokenJSON, _ := json.Marshal(token)
			return reconstructGrant(req.RequestID, existing.Annotations, credentials, string(tokenJSON))
		}
		if !k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("kubernetes: get rolebinding: %w", err)
		}
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:        bindingName,
				Namespace:   namespace,
				Annotations: annotations,
				Labels:      labels,
			},
			Subjects: []rbacv1.Subject{subject},
			RoleRef:  roleRef,
		}
		if _, err := p.clientset.RbacV1().RoleBindings(namespace).Create(ctx, rb, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("kubernetes: create rolebinding: %w", err)
		}
	} else {
		token.Kind = "ClusterRoleBinding"
		existing, err := p.clientset.RbacV1().ClusterRoleBindings().Get(ctx, bindingName, metav1.GetOptions{})
		if err == nil {
			tokenJSON, _ := json.Marshal(token)
			return reconstructGrant(req.RequestID, existing.Annotations, credentials, string(tokenJSON))
		}
		if !k8serrors.IsNotFound(err) {
			return nil, fmt.Errorf("kubernetes: get clusterrolebinding: %w", err)
		}
		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:        bindingName,
				Annotations: annotations,
				Labels:      labels,
			},
			Subjects: []rbacv1.Subject{subject},
			RoleRef:  roleRef,
		}
		if _, err := p.clientset.RbacV1().ClusterRoleBindings().Create(ctx, crb, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("kubernetes: create clusterrolebinding: %w", err)
		}
	}

	tokenJSON, _ := json.Marshal(token)
	return &providers.ElevationGrant{
		RequestID:   req.RequestID,
		Credentials: credentials,
		IssuedAt:    now,
		ExpiresAt:   expiresAt,
		RevokeToken: string(tokenJSON),
	}, nil
}

// Revoke deletes the ClusterRoleBinding or RoleBinding created by Grant.
// Idempotent: if the binding no longer exists, nil is returned.
func (p *Provider) Revoke(ctx context.Context, grant providers.ElevationGrant) error {
	if grant.RevokeToken == "" {
		return nil
	}
	var token k8sRevokeToken
	if err := json.Unmarshal([]byte(grant.RevokeToken), &token); err != nil {
		return fmt.Errorf("kubernetes: decode revoke token: %w", err)
	}

	var deleteErr error
	switch token.Kind {
	case "RoleBinding":
		deleteErr = p.clientset.RbacV1().RoleBindings(token.Namespace).Delete(ctx, token.Name, metav1.DeleteOptions{})
	default: // "ClusterRoleBinding"
		deleteErr = p.clientset.RbacV1().ClusterRoleBindings().Delete(ctx, token.Name, metav1.DeleteOptions{})
	}

	if deleteErr != nil && !k8serrors.IsNotFound(deleteErr) {
		return fmt.Errorf("kubernetes: delete binding: %w", deleteErr)
	}
	return nil
}

// IsActive returns true if the binding still exists in the cluster AND the
// grant has not expired. This provides provider-side liveness detection and
// catches out-of-band `kubectl delete` operations.
func (p *Provider) IsActive(ctx context.Context, grant providers.ElevationGrant) (bool, error) {
	if grant.RevokeToken == "" {
		return false, nil
	}
	var token k8sRevokeToken
	if err := json.Unmarshal([]byte(grant.RevokeToken), &token); err != nil {
		return false, fmt.Errorf("kubernetes: decode revoke token: %w", err)
	}

	var getErr error
	switch token.Kind {
	case "RoleBinding":
		_, getErr = p.clientset.RbacV1().RoleBindings(token.Namespace).Get(ctx, token.Name, metav1.GetOptions{})
	default:
		_, getErr = p.clientset.RbacV1().ClusterRoleBindings().Get(ctx, token.Name, metav1.GetOptions{})
	}

	if k8serrors.IsNotFound(getErr) {
		return false, nil
	}
	if getErr != nil {
		return false, fmt.Errorf("kubernetes: get binding: %w", getErr)
	}
	return grant.ExpiresAt.After(time.Now().UTC()), nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// k8sRevokeToken is the JSON payload stored in ElevationGrant.RevokeToken.
type k8sRevokeToken struct {
	Kind      string `json:"kind"` // "ClusterRoleBinding" or "RoleBinding"
	Name      string `json:"name"`
	Namespace string `json:"namespace"` // empty for ClusterRoleBinding
}

// bindingName returns the Kubernetes object name for this request.
// k8s object names must be lowercase alphanumeric or '-'.
func (p *Provider) bindingName(requestID string) string {
	return "jitsudo-" + strings.ToLower(requestID)
}

// namespace resolves the target namespace from ResourceScope.
// An empty scope or "*" means cluster-wide (ClusterRoleBinding).
func (p *Provider) namespace(resourceScope string) string {
	if resourceScope == "" || resourceScope == "*" {
		if p.cfg.DefaultNamespace != "" {
			return p.cfg.DefaultNamespace
		}
		return ""
	}
	return resourceScope
}

// managedLabel returns the label key used to mark jitsudo-managed bindings.
func (p *Provider) managedLabel() string {
	if p.cfg.ManagedLabel != "" {
		return p.cfg.ManagedLabel
	}
	return "jitsudo.dev/managed"
}

// reconstructGrant rebuilds an ElevationGrant from existing binding annotations.
func reconstructGrant(requestID string, annotations map[string]string, creds map[string]string, revokeToken string) (*providers.ElevationGrant, error) {
	expiresAt := time.Time{}
	if raw, ok := annotations["jitsudo.dev/expires-at"]; ok {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			expiresAt = t
		}
	}
	return &providers.ElevationGrant{
		RequestID:   requestID,
		Credentials: creds,
		ExpiresAt:   expiresAt,
		RevokeToken: revokeToken,
	}, nil
}
