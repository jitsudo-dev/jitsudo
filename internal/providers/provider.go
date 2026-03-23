// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

// Package providers defines the interface all cloud provider adapters must implement.
// The Provider interface is the core abstraction enabling jitsudo to support multiple
// cloud platforms without modifying the control plane logic.
package providers

import (
	"context"
	"time"
)

// ElevationRequest contains all information needed to grant temporary elevated access.
type ElevationRequest struct {
	// RequestID is the globally unique request identifier.
	RequestID string

	// UserIdentity is the IdP subject identifier (e.g., email or sub claim).
	UserIdentity string

	// Provider is the canonical provider name (e.g., "aws", "azure", "gcp", "kubernetes").
	Provider string

	// RoleName is the role or permission set to grant.
	RoleName string

	// ResourceScope is the provider-specific resource boundary
	// (AWS account ID, GCP project ID, Azure subscription ID, K8s namespace).
	ResourceScope string

	// Duration is the requested elevation window.
	Duration time.Duration

	// Reason is the human-readable justification provided by the requester.
	Reason string

	// Metadata holds provider-specific additional parameters.
	Metadata map[string]string
}

// ElevationGrant represents an active or completed elevation.
type ElevationGrant struct {
	// RequestID links back to the originating ElevationRequest.
	RequestID string

	// Credentials maps environment variable names to their values.
	// These are injected into the subprocess environment by `jitsudo exec` and `jitsudo shell`.
	// Example: {"AWS_ACCESS_KEY_ID": "...", "AWS_SECRET_ACCESS_KEY": "...", "AWS_SESSION_TOKEN": "..."}
	Credentials map[string]string

	// IssuedAt is when the grant was created.
	IssuedAt time.Time

	// ExpiresAt is when the grant naturally expires (regardless of revocation).
	ExpiresAt time.Time

	// RevokeToken is an opaque token used to revoke the grant before natural expiry.
	// The interpretation is provider-specific.
	RevokeToken string
}

// Provider is the interface all cloud provider adapters must implement.
// Any new provider must also pass the full contract test suite in contract_test.go.
type Provider interface {
	// Name returns the canonical provider identifier (e.g., "aws", "azure", "gcp", "kubernetes").
	// Must be lowercase, URL-safe, and stable across versions.
	Name() string

	// ValidateRequest checks whether the requested role and scope are syntactically and
	// semantically valid before the request enters the approval workflow.
	// ValidateRequest must not modify provider-side state.
	ValidateRequest(ctx context.Context, req ElevationRequest) error

	// Grant issues temporary elevated credentials after the request has been approved.
	// Grant must be idempotent: calling Grant twice with the same RequestID must not
	// create duplicate role bindings or return an error.
	Grant(ctx context.Context, req ElevationRequest) (*ElevationGrant, error)

	// Revoke terminates an active grant before its natural expiry.
	// Revoke should be idempotent: revoking an already-expired or already-revoked
	// grant should not return an error.
	Revoke(ctx context.Context, grant ElevationGrant) error

	// IsActive checks whether a grant is still valid and active on the provider side.
	// Used by the expiry sweeper and `jitsudo status` to detect out-of-band revocations.
	IsActive(ctx context.Context, grant ElevationGrant) (bool, error)
}

// Registry holds registered provider implementations keyed by their canonical name.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds a provider to the registry. Panics if a provider with the same name
// is already registered (programming error, caught at startup).
func (r *Registry) Register(p Provider) {
	name := p.Name()
	if _, exists := r.providers[name]; exists {
		panic("providers: duplicate registration for provider " + name)
	}
	r.providers[name] = p
}

// Get returns the provider with the given name, or nil if not registered.
func (r *Registry) Get(name string) Provider {
	return r.providers[name]
}

// Names returns the names of all registered providers.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}
