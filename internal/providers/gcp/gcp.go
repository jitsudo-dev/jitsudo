// Package gcp implements the jitsudo Provider interface for Google Cloud Platform.
// It grants temporary elevated access via IAM conditional role bindings with
// an expiry condition.
//
// License: Apache 2.0
package gcp

import (
	"context"
	"fmt"
	"time"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
)

// Config holds GCP provider configuration.
type Config struct {
	// OrganizationID is the GCP organization ID (numeric string, e.g., "123456789").
	// Used when resource_scope targets the organization level.
	OrganizationID string `yaml:"organization_id"`

	// CredentialsSource selects how jitsudod authenticates to GCP:
	// "workload_identity_federation", "service_account_key", or "application_default".
	CredentialsSource string `yaml:"credentials_source"`

	// MaxDuration caps the elevation window the provider will honour.
	// If zero, no server-side cap is enforced beyond GCP's IAM limit.
	MaxDuration time.Duration `yaml:"max_duration"`

	// ConditionTitlePrefix is prepended to the IAM condition title for all
	// jitsudo-managed bindings (e.g., "jitsudo" → "jitsudo-<requestID>").
	// Defaults to "jitsudo" if empty.
	ConditionTitlePrefix string `yaml:"condition_title_prefix"`
}

// Provider is the GCP implementation of providers.Provider.
type Provider struct {
	cfg Config
}

// New returns a new GCP Provider with the given configuration.
func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

// Name returns "gcp".
func (p *Provider) Name() string {
	return "gcp"
}

// ValidateRequest validates that the request is well-formed for GCP.
func (p *Provider) ValidateRequest(_ context.Context, req providers.ElevationRequest) error {
	if req.RequestID == "" {
		return fmt.Errorf("gcp: RequestID must not be empty")
	}
	if req.UserIdentity == "" {
		return fmt.Errorf("gcp: UserIdentity must not be empty")
	}
	if req.Duration <= 0 {
		return fmt.Errorf("gcp: Duration must be positive")
	}
	if req.ResourceScope == "" {
		return fmt.Errorf("gcp: ResourceScope (GCP project ID) must not be empty")
	}
	if req.RoleName == "" {
		return fmt.Errorf("gcp: RoleName must not be empty")
	}
	return nil
}

// Grant creates a GCP IAM conditional role binding with an expiry condition.
// TODO: implement using google.golang.org/api
func (p *Provider) Grant(_ context.Context, req providers.ElevationRequest) (*providers.ElevationGrant, error) {
	return nil, fmt.Errorf("gcp: Grant not yet implemented")
}

// Revoke removes the GCP IAM conditional role binding.
// TODO: implement using google.golang.org/api
func (p *Provider) Revoke(_ context.Context, grant providers.ElevationGrant) error {
	return fmt.Errorf("gcp: Revoke not yet implemented")
}

// IsActive checks whether the IAM conditional binding is still present.
// TODO: implement using google.golang.org/api
func (p *Provider) IsActive(_ context.Context, grant providers.ElevationGrant) (bool, error) {
	return false, fmt.Errorf("gcp: IsActive not yet implemented")
}
