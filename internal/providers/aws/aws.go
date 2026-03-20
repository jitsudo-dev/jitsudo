// Package aws implements the jitsudo Provider interface for AWS.
// It grants temporary elevated access via STS AssumeRole and optionally
// revokes sessions by attaching an inline IAM deny policy.
//
// License: Apache 2.0
package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	"github.com/jitsudo-dev/jitsudo/pkg/types"
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
	// Use {scope} for the account ID and {role} for the role name.
	// Example: "arn:aws:iam::{scope}:role/jitsudo-{role}"
	RoleARNTemplate string `yaml:"role_arn_template"`

	// MaxDuration caps the elevation window the provider will honour.
	// If zero, no server-side cap is enforced beyond the STS maximum (12h).
	MaxDuration types.Duration `yaml:"max_duration"`
}

// stsAPI is the subset of sts.Client used by this provider (enables test injection).
type stsAPI interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// iamAPI is the subset of iam.Client used by this provider (enables test injection).
type iamAPI interface {
	PutRolePolicy(ctx context.Context, params *iam.PutRolePolicyInput, optFns ...func(*iam.Options)) (*iam.PutRolePolicyOutput, error)
}

// Provider is the AWS implementation of providers.Provider.
type Provider struct {
	cfg       Config
	stsClient stsAPI
	iamClient iamAPI
}

// New returns a new AWS Provider, loading credentials from the ambient credential
// chain (env vars, ~/.aws/credentials, EC2 instance role, ECS task role, etc.).
func New(ctx context.Context, cfg Config) (*Provider, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if cfg.Region != "" {
		opts = append(opts, awsconfig.WithRegion(cfg.Region))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("aws: load config: %w", err)
	}
	return &Provider{
		cfg:       cfg,
		stsClient: sts.NewFromConfig(awsCfg),
		iamClient: iam.NewFromConfig(awsCfg),
	}, nil
}

// NewWithClients returns a Provider using the given STS and IAM clients — intended for tests.
func NewWithClients(cfg Config, stsClient stsAPI, iamClient iamAPI) *Provider {
	return &Provider{cfg: cfg, stsClient: stsClient, iamClient: iamClient}
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
// The session name is derived from the RequestID to ensure traceability.
// Session tags include RequestID, UserIdentity, and Reason for auditing.
func (p *Provider) Grant(ctx context.Context, req providers.ElevationRequest) (*providers.ElevationGrant, error) {
	roleARN, err := p.buildRoleARN(req)
	if err != nil {
		return nil, err
	}

	sessionName := p.sessionName(req.RequestID)
	durationSecs := int32(p.capDuration(req.Duration).Seconds())
	now := time.Now().UTC()

	out, err := p.stsClient.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         &roleARN,
		RoleSessionName: &sessionName,
		DurationSeconds: &durationSecs,
		Tags: []ststypes.Tag{
			{Key: strPtr("jitsudo:RequestID"), Value: strPtr(req.RequestID)},
			{Key: strPtr("jitsudo:UserIdentity"), Value: strPtr(req.UserIdentity)},
			{Key: strPtr("jitsudo:Reason"), Value: strPtr(truncate(req.Reason, 256))},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("aws: AssumeRole: %w", err)
	}

	creds := out.Credentials
	token := awsRevokeToken{
		RoleARN:     roleARN,
		SessionName: sessionName,
		IssuedAt:    now,
	}
	tokenJSON, _ := json.Marshal(token)

	region := p.cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	return &providers.ElevationGrant{
		RequestID: req.RequestID,
		Credentials: map[string]string{
			"AWS_ACCESS_KEY_ID":     *creds.AccessKeyId,
			"AWS_SECRET_ACCESS_KEY": *creds.SecretAccessKey,
			"AWS_SESSION_TOKEN":     *creds.SessionToken,
			"AWS_DEFAULT_REGION":    region,
		},
		IssuedAt:    now,
		ExpiresAt:   *creds.Expiration,
		RevokeToken: string(tokenJSON),
	}, nil
}

// Revoke invalidates the STS session by attaching an inline deny policy to the
// role with a DateLessThanEquals condition on aws:TokenIssueTime. This prevents
// all API calls from the session (and older ones) while leaving newer sessions
// unaffected. The policy is named after the session to allow idempotent updates.
//
// Note: this requires the jitsudod IAM role to have iam:PutRolePolicy permission
// on the target role.
func (p *Provider) Revoke(ctx context.Context, grant providers.ElevationGrant) error {
	if grant.RevokeToken == "" {
		return nil
	}
	var token awsRevokeToken
	if err := json.Unmarshal([]byte(grant.RevokeToken), &token); err != nil {
		return fmt.Errorf("aws: decode revoke token: %w", err)
	}

	// Extract the role name from the ARN (last path segment).
	// ARN format: arn:aws:iam::<account>:role/<name>
	roleName := token.RoleARN
	if idx := strings.LastIndex(roleName, "/"); idx >= 0 {
		roleName = roleName[idx+1:]
	}

	policyDoc := denyPolicyDocument(token.IssuedAt)
	policyName := "jitsudo-deny-" + token.SessionName

	_, err := p.iamClient.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       &roleName,
		PolicyName:     &policyName,
		PolicyDocument: &policyDoc,
	})
	if err != nil {
		// If the role doesn't exist (e.g. already deleted), treat as success.
		var noSuchEntity *iamtypes.NoSuchEntityException
		if isAWSError(err, &noSuchEntity) {
			return nil
		}
		return fmt.Errorf("aws: PutRolePolicy: %w", err)
	}
	return nil
}

// IsActive returns true if the grant has not yet reached its expiry time.
// STS session validity cannot be queried directly without attempting to use
// the credentials, so expiry-time comparison is the primary signal.
func (p *Provider) IsActive(_ context.Context, grant providers.ElevationGrant) (bool, error) {
	return grant.ExpiresAt.After(time.Now().UTC()), nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// awsRevokeToken is the JSON payload stored in ElevationGrant.RevokeToken.
type awsRevokeToken struct {
	RoleARN     string    `json:"role_arn"`
	SessionName string    `json:"session_name"`
	IssuedAt    time.Time `json:"issued_at"`
}

func (p *Provider) buildRoleARN(req providers.ElevationRequest) (string, error) {
	if p.cfg.RoleARNTemplate == "" {
		return "", fmt.Errorf("aws: RoleARNTemplate is not configured")
	}
	arn := strings.NewReplacer(
		"{scope}", req.ResourceScope,
		"{role}", req.RoleName,
	).Replace(p.cfg.RoleARNTemplate)
	return arn, nil
}

// sessionName returns a valid STS session name (1–64 chars, [\w+=,.@-]).
func (p *Provider) sessionName(requestID string) string {
	name := "jitsudo-" + requestID
	// Replace any characters not in the allowed set with '-'.
	var b strings.Builder
	for _, c := range name {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '+' || c == '=' || c == ',' || c == '.' || c == '@' || c == '-' || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteRune('-')
		}
	}
	s := b.String()
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// capDuration applies MaxDuration and the STS 12-hour maximum, with a 15-minute floor.
func (p *Provider) capDuration(d time.Duration) time.Duration {
	const (
		stsMin = 15 * time.Minute
		stsMax = 12 * time.Hour
	)
	if p.cfg.MaxDuration.Duration > 0 && d > p.cfg.MaxDuration.Duration {
		d = p.cfg.MaxDuration.Duration
	}
	if d > stsMax {
		d = stsMax
	}
	if d < stsMin {
		d = stsMin
	}
	return d
}

// denyPolicyDocument returns an IAM policy JSON that denies all actions for
// sessions issued at or before the given time.
func denyPolicyDocument(issuedAt time.Time) string {
	type statement struct {
		Effect    string `json:"Effect"`
		Action    string `json:"Action"`
		Resource  string `json:"Resource"`
		Condition any    `json:"Condition"`
	}
	type policy struct {
		Version   string      `json:"Version"`
		Statement []statement `json:"Statement"`
	}
	p := policy{
		Version: "2012-10-17",
		Statement: []statement{{
			Effect:   "Deny",
			Action:   "*",
			Resource: "*",
			Condition: map[string]any{
				"DateLessThanEquals": map[string]string{
					"aws:TokenIssueTime": issuedAt.UTC().Format(time.RFC3339),
				},
			},
		}},
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func strPtr(s string) *string { return &s }

// truncate caps a string at maxLen runes.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}

// isAWSError checks if err matches a specific AWS SDK error type.
func isAWSError[T error](err error, target *T) bool {
	if err == nil {
		return false
	}
	// Use errors.As-style matching via type assertion
	type errorUnwrapper interface{ Unwrap() error }
	for e := err; e != nil; {
		if t, ok := e.(T); ok {
			*target = t
			return true
		}
		if u, ok := e.(errorUnwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
