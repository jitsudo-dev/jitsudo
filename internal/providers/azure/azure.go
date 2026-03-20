// Package azure implements the jitsudo Provider interface for Microsoft Azure.
// It grants temporary elevated access via Azure RBAC role assignments created
// through the ARM Authorization API. User principal IDs are resolved via the
// Microsoft Graph API. Role assignments use deterministic GUIDs derived from
// the RequestID to guarantee idempotency.
//
// License: Apache 2.0
package azure

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azpolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	"github.com/jitsudo-dev/jitsudo/pkg/types"
)

// Config holds Azure provider configuration.
type Config struct {
	// TenantID is the Azure Active Directory (Entra ID) tenant ID.
	TenantID string `yaml:"tenant_id"`

	// DefaultSubscriptionID is the subscription used when no resource scope is provided.
	DefaultSubscriptionID string `yaml:"default_subscription_id"`

	// ClientID is the service principal (or managed identity) client ID used by jitsudod.
	ClientID string `yaml:"client_id"`

	// CredentialsSource selects how jitsudod authenticates to Azure:
	// "workload_identity" (AKS workload identity / managed identity) or "client_secret".
	CredentialsSource string `yaml:"credentials_source"`

	// MaxDuration caps the elevation window the provider will honour.
	// If zero, no server-side cap is enforced beyond the Azure RBAC limit.
	MaxDuration types.Duration `yaml:"max_duration"`
}

// roleAssignAPI is the subset of armauthorization.RoleAssignmentsClient used here.
type roleAssignAPI interface {
	Create(ctx context.Context, scope, roleAssignmentName string, parameters armauthorization.RoleAssignmentCreateParameters, options *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error)
	Delete(ctx context.Context, scope, roleAssignmentName string, options *armauthorization.RoleAssignmentsClientDeleteOptions) (armauthorization.RoleAssignmentsClientDeleteResponse, error)
}

// roleDefLookupFn resolves a role name to its full Azure role definition ID within a scope.
type roleDefLookupFn func(ctx context.Context, scope, roleName string) (string, error)

// principalLookupFn resolves a UPN (user principal name) to its Azure object ID.
type principalLookupFn func(ctx context.Context, upn string) (string, error)

// Provider is the Azure implementation of providers.Provider.
type Provider struct {
	cfg           Config
	roleAssign    roleAssignAPI
	lookupRoleDef roleDefLookupFn
	lookupUser    principalLookupFn
}

// New returns a new Azure Provider using Default Azure Credentials.
func New(ctx context.Context, cfg Config) (*Provider, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure: get credentials: %w", err)
	}

	subID := cfg.DefaultSubscriptionID
	assignClient, err := armauthorization.NewRoleAssignmentsClient(subID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azure: create role assignments client: %w", err)
	}
	defClient, err := armauthorization.NewRoleDefinitionsClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azure: create role definitions client: %w", err)
	}

	cache := newSyncCache()
	return &Provider{
		cfg:           cfg,
		roleAssign:    assignClient,
		lookupRoleDef: makeRoleDefLookup(defClient, cache),
		lookupUser:    makeUserLookup(cred, cache),
	}, nil
}

// NewWithAPIs returns a Provider with injected dependencies — intended for tests.
func NewWithAPIs(cfg Config, roleAssign roleAssignAPI, lookupRoleDef roleDefLookupFn, lookupUser principalLookupFn) *Provider {
	return &Provider{
		cfg:           cfg,
		roleAssign:    roleAssign,
		lookupRoleDef: lookupRoleDef,
		lookupUser:    lookupUser,
	}
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

// Grant creates an Azure RBAC role assignment with a deterministic assignment ID derived
// from the RequestID. The assignment is idempotent: Azure returns the existing assignment
// if the same GUID is re-submitted with identical parameters.
// Note: Azure RBAC does not natively support time-bounded assignments without Azure AD PIM.
// Expiry is enforced by the jitsudo sweeper calling Revoke when ExpiresAt is reached.
func (p *Provider) Grant(ctx context.Context, req providers.ElevationRequest) (*providers.ElevationGrant, error) {
	scope := p.buildScope(req.ResourceScope)

	principalID, err := p.lookupUser(ctx, req.UserIdentity)
	if err != nil {
		return nil, fmt.Errorf("azure: resolve user %q: %w", req.UserIdentity, err)
	}

	roleDefID, err := p.lookupRoleDef(ctx, scope, req.RoleName)
	if err != nil {
		return nil, fmt.Errorf("azure: resolve role %q: %w", req.RoleName, err)
	}

	dur := req.Duration
	if p.cfg.MaxDuration.Duration > 0 && dur > p.cfg.MaxDuration.Duration {
		dur = p.cfg.MaxDuration.Duration
	}
	now := time.Now().UTC()
	expiresAt := now.Add(dur)

	assignID := deterministicAssignmentID(req.RequestID)
	_, err = p.roleAssign.Create(ctx, scope, assignID, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      &principalID,
			RoleDefinitionID: &roleDefID,
		},
	}, nil)
	if err != nil && !isAzureConflict(err) {
		return nil, fmt.Errorf("azure: create role assignment: %w", err)
	}

	token := azureRevokeToken{AssignmentID: assignID, Scope: scope}
	tokenJSON, _ := json.Marshal(token)

	return &providers.ElevationGrant{
		RequestID: req.RequestID,
		Credentials: map[string]string{
			"AZURE_SUBSCRIPTION_ID": req.ResourceScope,
		},
		IssuedAt:    now,
		ExpiresAt:   expiresAt,
		RevokeToken: string(tokenJSON),
	}, nil
}

// Revoke deletes the Azure RBAC role assignment identified by the revoke token.
// Idempotent: if the assignment no longer exists, nil is returned.
func (p *Provider) Revoke(ctx context.Context, grant providers.ElevationGrant) error {
	if grant.RevokeToken == "" {
		return nil
	}
	var token azureRevokeToken
	if err := json.Unmarshal([]byte(grant.RevokeToken), &token); err != nil {
		return fmt.Errorf("azure: decode revoke token: %w", err)
	}

	_, err := p.roleAssign.Delete(ctx, token.Scope, token.AssignmentID, nil)
	if err != nil && !isAzureHTTP404(err) {
		return fmt.Errorf("azure: delete role assignment: %w", err)
	}
	return nil
}

// IsActive returns true if the grant has not yet expired.
// A full assignment-existence check is deferred to avoid per-request latency.
func (p *Provider) IsActive(_ context.Context, grant providers.ElevationGrant) (bool, error) {
	return grant.ExpiresAt.After(time.Now().UTC()), nil
}

// ── Internal types ────────────────────────────────────────────────────────────

// azureRevokeToken is the JSON payload stored in ElevationGrant.RevokeToken.
type azureRevokeToken struct {
	AssignmentID string `json:"assignment_id"`
	Scope        string `json:"scope"`
}

// ── Scope helper ─────────────────────────────────────────────────────────────

// buildScope converts a ResourceScope to an ARM scope URI.
// If scope already starts with "/" it is returned as-is.
// Otherwise it is treated as a subscription ID.
func (p *Provider) buildScope(resourceScope string) string {
	if strings.HasPrefix(resourceScope, "/") {
		return resourceScope
	}
	return "/subscriptions/" + resourceScope
}

// ── Deterministic assignment ID ───────────────────────────────────────────────

// deterministicAssignmentID returns a UUID-shaped string derived from requestID via SHA-256.
// Azure role assignment names must be GUIDs; using a deterministic value enables idempotency.
func deterministicAssignmentID(requestID string) string {
	h := sha256.Sum256([]byte("jitsudo-assignment:" + requestID))
	// UUID format: 8-4-4-4-12 hex characters.
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// ── Simple TTL cache ──────────────────────────────────────────────────────────

type syncCache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	value   string
	expires time.Time
}

func newSyncCache() *syncCache {
	return &syncCache{entries: make(map[string]cacheEntry)}
}

func (c *syncCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expires) {
		return "", false
	}
	return e.value, true
}

func (c *syncCache) set(key, value string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{value: value, expires: time.Now().Add(ttl)}
}

// ── Real lookup constructors ──────────────────────────────────────────────────

// makeRoleDefLookup returns a roleDefLookupFn that searches for a role definition by name
// within the given scope, caching results for 5 minutes.
func makeRoleDefLookup(client *armauthorization.RoleDefinitionsClient, cache *syncCache) roleDefLookupFn {
	return func(ctx context.Context, scope, roleName string) (string, error) {
		cacheKey := "roledef|" + scope + "|" + roleName
		if id, ok := cache.get(cacheKey); ok {
			return id, nil
		}
		filter := fmt.Sprintf("roleName eq '%s'", roleName)
		pager := client.NewListPager(scope, &armauthorization.RoleDefinitionsClientListOptions{
			Filter: &filter,
		})
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return "", fmt.Errorf("list role definitions: %w", err)
			}
			for _, def := range page.Value {
				if def.ID != nil {
					cache.set(cacheKey, *def.ID, 5*time.Minute)
					return *def.ID, nil
				}
			}
		}
		return "", fmt.Errorf("role definition %q not found in scope %s", roleName, scope)
	}
}

// makeUserLookup returns a principalLookupFn that resolves a UPN to an Azure object ID
// via the Microsoft Graph API, caching results for 5 minutes.
func makeUserLookup(cred azcore.TokenCredential, cache *syncCache) principalLookupFn {
	return func(ctx context.Context, upn string) (string, error) {
		cacheKey := "user|" + upn
		if id, ok := cache.get(cacheKey); ok {
			return id, nil
		}
		tok, err := cred.GetToken(ctx, azpolicy.TokenRequestOptions{
			Scopes: []string{"https://graph.microsoft.com/.default"},
		})
		if err != nil {
			return "", fmt.Errorf("get graph token: %w", err)
		}
		url := "https://graph.microsoft.com/v1.0/users/" + upn + "?$select=id"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", fmt.Errorf("create graph request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("graph request: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("graph API returned %d: %s", resp.StatusCode, string(body))
		}
		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return "", fmt.Errorf("parse graph response: %w", err)
		}
		if result.ID == "" {
			return "", fmt.Errorf("user %q not found in directory", upn)
		}
		cache.set(cacheKey, result.ID, 5*time.Minute)
		return result.ID, nil
	}
}

// ── Error helpers ─────────────────────────────────────────────────────────────

// isAzureHTTP404 returns true if err is an Azure ResponseError with HTTP 404.
func isAzureHTTP404(err error) bool {
	var re *azcore.ResponseError
	return errors.As(err, &re) && re.StatusCode == http.StatusNotFound
}

// isAzureConflict returns true if err indicates the role assignment already exists.
func isAzureConflict(err error) bool {
	var re *azcore.ResponseError
	if !errors.As(err, &re) {
		return false
	}
	return re.StatusCode == http.StatusConflict ||
		re.ErrorCode == "RoleAssignmentExists"
}
