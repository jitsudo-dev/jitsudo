// Package gcp implements the jitsudo Provider interface for Google Cloud Platform.
// It grants temporary elevated access via IAM conditional role bindings with
// an expiry condition written as a CEL expression.
//
// License: Apache 2.0
package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	"github.com/jitsudo-dev/jitsudo/pkg/types"
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
	MaxDuration types.Duration `yaml:"max_duration"`

	// ConditionTitlePrefix is prepended to the IAM condition title for all
	// jitsudo-managed bindings (e.g., "jitsudo" → "jitsudo-<requestID>").
	// Defaults to "jitsudo" if empty.
	ConditionTitlePrefix string `yaml:"condition_title_prefix"`
}

// crmAPI is the subset of the Cloud Resource Manager v3 API used by this provider.
type crmAPI interface {
	GetIamPolicy(project string, req *cloudresourcemanager.GetIamPolicyRequest) (*cloudresourcemanager.Policy, error)
	SetIamPolicy(project string, req *cloudresourcemanager.SetIamPolicyRequest) (*cloudresourcemanager.Policy, error)
}

// realCRM wraps the generated Projects service to implement crmAPI.
type realCRM struct {
	svc *cloudresourcemanager.ProjectsService
}

func (r *realCRM) GetIamPolicy(project string, req *cloudresourcemanager.GetIamPolicyRequest) (*cloudresourcemanager.Policy, error) {
	return r.svc.GetIamPolicy(project, req).Do()
}

func (r *realCRM) SetIamPolicy(project string, req *cloudresourcemanager.SetIamPolicyRequest) (*cloudresourcemanager.Policy, error) {
	return r.svc.SetIamPolicy(project, req).Do()
}

// Provider is the GCP implementation of providers.Provider.
type Provider struct {
	cfg Config
	crm crmAPI
}

// New returns a new GCP Provider using Application Default Credentials.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	svc, err := cloudresourcemanager.NewService(ctx, option.WithScopes(cloudresourcemanager.CloudPlatformScope))
	if err != nil {
		return nil, fmt.Errorf("gcp: create CRM service: %w", err)
	}
	return &Provider{cfg: cfg, crm: &realCRM{svc: svc.Projects}}, nil
}

// NewWithCRM returns a Provider with an injected crmAPI — intended for tests.
func NewWithCRM(cfg Config, crm crmAPI) *Provider {
	return &Provider{cfg: cfg, crm: crm}
}

// Name returns "gcp".
func (p *Provider) Name() string {
	return "gcp"
}

// ValidateRequest validates that the request is well-formed for GCP.
func (p *Provider) ValidateRequest(_ context.Context, req providers.ElevationRequest) error {
	if err := providers.ValidateCommon(req); err != nil {
		return err
	}
	if req.ResourceScope == "" {
		return fmt.Errorf("gcp: ResourceScope (GCP project ID) must not be empty")
	}
	if req.RoleName == "" {
		return fmt.Errorf("gcp: RoleName must not be empty")
	}
	return nil
}

// Grant creates a GCP IAM conditional role binding on the project with an
// expiry CEL expression. The binding is identified by a unique condition title.
// Idempotent: if a binding with the same condition title exists, it is returned.
// Uses optimistic concurrency (ETag) with up to 5 retries on HTTP 409.
func (p *Provider) Grant(ctx context.Context, req providers.ElevationRequest) (*providers.ElevationGrant, error) {
	dur := req.Duration
	if p.cfg.MaxDuration.Duration > 0 && dur > p.cfg.MaxDuration.Duration {
		dur = p.cfg.MaxDuration.Duration
	}

	now := time.Now().UTC()
	expiresAt := now.Add(dur)
	project := "projects/" + req.ResourceScope
	member := p.memberString(req.UserIdentity)
	role := p.roleString(req.RoleName)
	condTitle := p.conditionTitle(req.RequestID)

	token := gcpRevokeToken{
		Project:        project,
		Member:         member,
		Role:           role,
		ConditionTitle: condTitle,
	}

	const maxRetries = 5
	for i := 0; i < maxRetries; i++ {
		policy, err := p.crm.GetIamPolicy(project, &cloudresourcemanager.GetIamPolicyRequest{
			Options: &cloudresourcemanager.GetPolicyOptions{RequestedPolicyVersion: 3},
		})
		if err != nil {
			return nil, fmt.Errorf("gcp: GetIamPolicy: %w", err)
		}

		// Idempotency: check if our binding already exists.
		for _, b := range policy.Bindings {
			if b.Role == role && b.Condition != nil && b.Condition.Title == condTitle {
				for _, m := range b.Members {
					if m == member {
						tokenJSON, _ := json.Marshal(token)
						return &providers.ElevationGrant{
							RequestID:   req.RequestID,
							Credentials: gcpCredentials(req.ResourceScope),
							IssuedAt:    now,
							ExpiresAt:   expiresAt,
							RevokeToken: string(tokenJSON),
						}, nil
					}
				}
			}
		}

		// Add the new binding.
		// GCP CEL requires UTC time without timezone offset: "2026-03-19T10:00:00Z"
		celExpr := fmt.Sprintf(`request.time < timestamp("%s")`, expiresAt.UTC().Format("2006-01-02T15:04:05Z"))
		policy.Bindings = append(policy.Bindings, &cloudresourcemanager.Binding{
			Role:    role,
			Members: []string{member},
			Condition: &cloudresourcemanager.Expr{
				Title:      condTitle,
				Expression: celExpr,
			},
		})
		policy.Version = 3

		_, err = p.crm.SetIamPolicy(project, &cloudresourcemanager.SetIamPolicyRequest{
			Policy: policy,
		})
		if err == nil {
			break
		}
		if isHTTP409(err) && i < maxRetries-1 {
			// ETag conflict — another process modified the policy; retry.
			continue
		}
		return nil, fmt.Errorf("gcp: SetIamPolicy: %w", err)
	}

	tokenJSON, _ := json.Marshal(token)
	return &providers.ElevationGrant{
		RequestID:   req.RequestID,
		Credentials: gcpCredentials(req.ResourceScope),
		IssuedAt:    now,
		ExpiresAt:   expiresAt,
		RevokeToken: string(tokenJSON),
	}, nil
}

// Revoke removes the IAM conditional role binding identified by the revoke token.
// Idempotent: if the binding is already gone, nil is returned.
// Uses the same optimistic concurrency retry loop as Grant.
func (p *Provider) Revoke(ctx context.Context, grant providers.ElevationGrant) error {
	if grant.RevokeToken == "" {
		return nil
	}
	var token gcpRevokeToken
	if err := json.Unmarshal([]byte(grant.RevokeToken), &token); err != nil {
		return fmt.Errorf("gcp: decode revoke token: %w", err)
	}

	const maxRetries = 5
	for i := 0; i < maxRetries; i++ {
		policy, err := p.crm.GetIamPolicy(token.Project, &cloudresourcemanager.GetIamPolicyRequest{
			Options: &cloudresourcemanager.GetPolicyOptions{RequestedPolicyVersion: 3},
		})
		if err != nil {
			return fmt.Errorf("gcp: GetIamPolicy (revoke): %w", err)
		}

		found := false
		newBindings := make([]*cloudresourcemanager.Binding, 0, len(policy.Bindings))
		for _, b := range policy.Bindings {
			if b.Role == token.Role && b.Condition != nil && b.Condition.Title == token.ConditionTitle {
				// Remove this member from the binding.
				newMembers := make([]string, 0, len(b.Members))
				for _, m := range b.Members {
					if m != token.Member {
						newMembers = append(newMembers, m)
					} else {
						found = true
					}
				}
				if len(newMembers) > 0 {
					b.Members = newMembers
					newBindings = append(newBindings, b)
				}
				// If no members remain, drop the binding entirely.
			} else {
				newBindings = append(newBindings, b)
			}
		}

		if !found {
			return nil // Already removed — idempotent.
		}

		policy.Bindings = newBindings
		_, err = p.crm.SetIamPolicy(token.Project, &cloudresourcemanager.SetIamPolicyRequest{
			Policy: policy,
		})
		if err == nil {
			return nil
		}
		if isHTTP409(err) && i < maxRetries-1 {
			continue
		}
		return fmt.Errorf("gcp: SetIamPolicy (revoke): %w", err)
	}
	return nil
}

// IsActive returns true if the grant has not yet expired.
// A full binding-existence check is available via the GCP API but is deferred
// to avoid the per-request latency cost during high-frequency sweeper runs.
func (p *Provider) IsActive(_ context.Context, grant providers.ElevationGrant) (bool, error) {
	return grant.ExpiresAt.After(time.Now().UTC()), nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// gcpRevokeToken is the JSON payload stored in ElevationGrant.RevokeToken.
type gcpRevokeToken struct {
	Project        string `json:"project"`
	Member         string `json:"member"`
	Role           string `json:"role"`
	ConditionTitle string `json:"condition_title"`
}

func (p *Provider) conditionTitle(requestID string) string {
	prefix := p.cfg.ConditionTitlePrefix
	if prefix == "" {
		prefix = "jitsudo"
	}
	return prefix + "-" + requestID
}

func (p *Provider) memberString(userIdentity string) string {
	if strings.HasSuffix(userIdentity, ".gserviceaccount.com") {
		return "serviceAccount:" + userIdentity
	}
	return "user:" + userIdentity
}

func (p *Provider) roleString(roleName string) string {
	if strings.Contains(roleName, "/") {
		return roleName
	}
	return "roles/" + roleName
}

func gcpCredentials(projectID string) map[string]string {
	return map[string]string{
		"GOOGLE_CLOUD_PROJECT": projectID,
	}
}

// isHTTP409 returns true if err is a Google API error with status 409 Conflict.
func isHTTP409(err error) bool {
	var gerr *googleapi.Error
	if gErr, ok := err.(*googleapi.Error); ok {
		gerr = gErr
	}
	return gerr != nil && gerr.Code == http.StatusConflict
}
