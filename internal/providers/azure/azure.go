// Package azure implements the jitsudo Provider interface for Microsoft Azure.
// It grants temporary elevated access via Azure RBAC role assignment
// through the Microsoft Graph API.
//
// License: Apache 2.0
package azure

import (
	"context"
	"fmt"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
)

// Config holds Azure provider configuration.
type Config struct {
	// TenantID is the Azure Active Directory (Entra ID) tenant ID.
	TenantID string

	// SubscriptionID is the default Azure subscription ID.
	SubscriptionID string
}

// Provider is the Azure implementation of providers.Provider.
type Provider struct {
	cfg Config
}

// New returns a new Azure Provider with the given configuration.
func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

// Name returns "azure".
func (p *Provider) Name() string {
	return "azure"
}

// ValidateRequest validates that the request is well-formed for Azure.
func (p *Provider) ValidateRequest(_ context.Context, req providers.ElevationRequest) error {
	if req.RequestID == "" {
		return fmt.Errorf("azure: RequestID must not be empty")
	}
	if req.UserIdentity == "" {
		return fmt.Errorf("azure: UserIdentity must not be empty")
	}
	if req.Duration <= 0 {
		return fmt.Errorf("azure: Duration must be positive")
	}
	if req.ResourceScope == "" {
		return fmt.Errorf("azure: ResourceScope (subscription or resource group) must not be empty")
	}
	if req.RoleName == "" {
		return fmt.Errorf("azure: RoleName must not be empty")
	}
	return nil
}

// Grant creates a time-bound Azure RBAC role assignment.
// TODO: implement using Azure SDK for Go
func (p *Provider) Grant(_ context.Context, req providers.ElevationRequest) (*providers.ElevationGrant, error) {
	return nil, fmt.Errorf("azure: Grant not yet implemented")
}

// Revoke removes the Azure RBAC role assignment.
// TODO: implement using Azure SDK for Go
func (p *Provider) Revoke(_ context.Context, grant providers.ElevationGrant) error {
	return fmt.Errorf("azure: Revoke not yet implemented")
}

// IsActive checks whether the RBAC role assignment still exists.
// TODO: implement using Azure SDK for Go
func (p *Provider) IsActive(_ context.Context, grant providers.ElevationGrant) (bool, error) {
	return false, fmt.Errorf("azure: IsActive not yet implemented")
}
