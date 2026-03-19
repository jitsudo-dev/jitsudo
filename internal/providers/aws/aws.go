// Package aws implements the jitsudo Provider interface for AWS.
// It grants temporary elevated access via STS AssumeRole and
// AWS IAM Identity Center permission set assignment.
//
// License: Apache 2.0
package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
)

// Config holds AWS provider configuration.
type Config struct {
	// Mode selects the grant mechanism: "sts_assume_role" or "identity_center".
	Mode string `yaml:"mode"`

	// Region is the primary AWS region (e.g., "us-east-1").
	Region string `yaml:"region"`

	// IdentityCenterInstanceARN is the ARN of the IAM Identity Center instance.
	// Required when Mode is "identity_center".
	IdentityCenterInstanceARN string `yaml:"identity_center_instance_arn"`

	// IdentityCenterStoreID is the IAM Identity Center identity store ID (e.g., "d-xxxxxxxxxx").
	// Required when Mode is "identity_center".
	IdentityCenterStoreID string `yaml:"identity_center_store_id"`

	// RoleARNTemplate is a Go template for the IAM role ARN to assume.
	// Required when Mode is "sts_assume_role".
	// Example: "arn:aws:iam::{scope}:role/jitsudo-{role}"
	RoleARNTemplate string `yaml:"role_arn_template"`

	// MaxDuration caps the elevation window the provider will honour.
	// If zero, no server-side cap is enforced beyond the IAM/IC limit.
	MaxDuration time.Duration `yaml:"max_duration"`
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
