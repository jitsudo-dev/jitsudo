// Package aws implements the jitsudo Provider interface for AWS.
// It grants temporary elevated access via STS AssumeRole and
// AWS IAM Identity Center permission set assignment.
//
// License: Apache 2.0
package aws

import (
	"context"
	"fmt"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
)

// Config holds AWS provider configuration.
type Config struct {
	// Region is the primary AWS region (e.g., "us-east-1").
	Region string

	// IdentityCenterInstanceARN is the ARN of the IAM Identity Center instance.
	// Required for Identity Center permission set assignment.
	IdentityCenterInstanceARN string
}

// Provider is the AWS implementation of providers.Provider.
type Provider struct {
	cfg Config
}

// New returns a new AWS Provider with the given configuration.
func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

// Name returns "aws".
func (p *Provider) Name() string {
	return "aws"
}

// ValidateRequest validates that the request is well-formed for AWS.
func (p *Provider) ValidateRequest(_ context.Context, req providers.ElevationRequest) error {
	if req.RequestID == "" {
		return fmt.Errorf("aws: RequestID must not be empty")
	}
	if req.UserIdentity == "" {
		return fmt.Errorf("aws: UserIdentity must not be empty")
	}
	if req.Duration <= 0 {
		return fmt.Errorf("aws: Duration must be positive")
	}
	if req.ResourceScope == "" {
		return fmt.Errorf("aws: ResourceScope (AWS account ID) must not be empty")
	}
	if req.RoleName == "" {
		return fmt.Errorf("aws: RoleName must not be empty")
	}
	return nil
}

// Grant issues temporary credentials via STS AssumeRole.
// TODO: implement using aws-sdk-go-v2
func (p *Provider) Grant(_ context.Context, req providers.ElevationRequest) (*providers.ElevationGrant, error) {
	return nil, fmt.Errorf("aws: Grant not yet implemented")
}

// Revoke terminates an active STS session.
// TODO: implement using aws-sdk-go-v2
func (p *Provider) Revoke(_ context.Context, grant providers.ElevationGrant) error {
	return fmt.Errorf("aws: Revoke not yet implemented")
}

// IsActive checks whether the STS session is still valid.
// TODO: implement using aws-sdk-go-v2
func (p *Provider) IsActive(_ context.Context, grant providers.ElevationGrant) (bool, error) {
	return false, fmt.Errorf("aws: IsActive not yet implemented")
}
