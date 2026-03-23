// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package azure

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	"github.com/jitsudo-dev/jitsudo/pkg/types"
)

// ── Mock role assignment API ──────────────────────────────────────────────────

type mockRoleAssign struct {
	createErr    error
	deleteErr    error
	createCalled bool
	deleteCalled bool
}

func (m *mockRoleAssign) Create(_ context.Context, _, _ string, _ armauthorization.RoleAssignmentCreateParameters, _ *armauthorization.RoleAssignmentsClientCreateOptions) (armauthorization.RoleAssignmentsClientCreateResponse, error) {
	m.createCalled = true
	return armauthorization.RoleAssignmentsClientCreateResponse{}, m.createErr
}

func (m *mockRoleAssign) Delete(_ context.Context, _, _ string, _ *armauthorization.RoleAssignmentsClientDeleteOptions) (armauthorization.RoleAssignmentsClientDeleteResponse, error) {
	m.deleteCalled = true
	return armauthorization.RoleAssignmentsClientDeleteResponse{}, m.deleteErr
}

// ── Standard lookup fns ───────────────────────────────────────────────────────

var (
	okUser    = func(_ context.Context, _ string) (string, error) { return "obj-id-123", nil }
	okRoleDef = func(_ context.Context, _, _ string) (string, error) {
		return "/subscriptions/s/providers/Microsoft.Authorization/roleDefinitions/r", nil
	}
)

// ── buildScope ────────────────────────────────────────────────────────────────

func TestBuildScope_AlreadyAbsolutePath(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	got := p.buildScope("/subscriptions/sub123")
	want := "/subscriptions/sub123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildScope_SubscriptionID(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	got := p.buildScope("sub-abc")
	want := "/subscriptions/sub-abc"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ── deterministicAssignmentID ─────────────────────────────────────────────────

func TestDeterministicAssignmentID_SameInput(t *testing.T) {
	a := deterministicAssignmentID("req-001")
	b := deterministicAssignmentID("req-001")
	if a != b {
		t.Errorf("expected same output for same input, got %q and %q", a, b)
	}
}

func TestDeterministicAssignmentID_UUIDShape(t *testing.T) {
	id := deterministicAssignmentID("req-001")
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Errorf("expected UUID with 5 parts separated by '-', got %q (%d parts)", id, len(parts))
	}
}

func TestDeterministicAssignmentID_DifferentInputs(t *testing.T) {
	a := deterministicAssignmentID("req-001")
	b := deterministicAssignmentID("req-002")
	if a == b {
		t.Errorf("expected different outputs for different inputs, got %q for both", a)
	}
}

// ── syncCache ─────────────────────────────────────────────────────────────────

func TestSyncCache_SetAndGet(t *testing.T) {
	c := newSyncCache()
	c.set("key", "value", time.Minute)
	got, ok := c.get("key")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got != "value" {
		t.Errorf("got %q, want %q", got, "value")
	}
}

func TestSyncCache_MissingKey(t *testing.T) {
	c := newSyncCache()
	_, ok := c.get("nonexistent")
	if ok {
		t.Error("expected cache miss for nonexistent key")
	}
}

func TestSyncCache_ExpiredKey(t *testing.T) {
	c := newSyncCache()
	c.set("key", "value", -time.Second) // Already expired.
	_, ok := c.get("key")
	if ok {
		t.Error("expected cache miss for expired key")
	}
}

// ── Name ──────────────────────────────────────────────────────────────────────

func TestAzureName(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	if got := p.Name(); got != "azure" {
		t.Errorf("Name() = %q, want %q", got, "azure")
	}
}

// ── ValidateRequest ───────────────────────────────────────────────────────────

func azureValidRequest() providers.ElevationRequest {
	return providers.ElevationRequest{
		RequestID:     "req-001",
		UserIdentity:  "user@example.com",
		RoleName:      "Contributor",
		ResourceScope: "sub-abc",
		Duration:      time.Hour,
	}
}

func TestAzureValidateRequest_AcceptsValid(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	if err := p.ValidateRequest(context.Background(), azureValidRequest()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAzureValidateRequest_RejectsEmptyRequestID(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	req := azureValidRequest()
	req.RequestID = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty RequestID")
	}
}

func TestAzureValidateRequest_RejectsEmptyUserIdentity(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	req := azureValidRequest()
	req.UserIdentity = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty UserIdentity")
	}
}

func TestAzureValidateRequest_RejectsEmptyRoleName(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	req := azureValidRequest()
	req.RoleName = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty RoleName")
	}
}

func TestAzureValidateRequest_RejectsEmptyResourceScope(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	req := azureValidRequest()
	req.ResourceScope = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty ResourceScope")
	}
}

func TestAzureValidateRequest_RejectsZeroDuration(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	req := azureValidRequest()
	req.Duration = 0
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for zero Duration")
	}
}

// ── Grant ─────────────────────────────────────────────────────────────────────

func TestAzureGrant_SuccessPath(t *testing.T) {
	mock := &mockRoleAssign{}
	p := NewWithAPIs(Config{}, mock, okRoleDef, okUser)

	grant, err := p.Grant(context.Background(), azureValidRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grant == nil {
		t.Fatal("expected non-nil grant")
	}
	if !mock.createCalled {
		t.Error("expected Create to be called")
	}
	if grant.RequestID != "req-001" {
		t.Errorf("RequestID = %q, want %q", grant.RequestID, "req-001")
	}
	subID, ok := grant.Credentials["AZURE_SUBSCRIPTION_ID"]
	if !ok {
		t.Fatal("missing AZURE_SUBSCRIPTION_ID credential")
	}
	if subID != "sub-abc" {
		t.Errorf("AZURE_SUBSCRIPTION_ID = %q, want %q", subID, "sub-abc")
	}
	// RevokeToken should contain assignment_id and scope.
	var revokeToken azureRevokeToken
	if err := json.Unmarshal([]byte(grant.RevokeToken), &revokeToken); err != nil {
		t.Fatalf("invalid RevokeToken JSON: %v", err)
	}
	if revokeToken.AssignmentID == "" {
		t.Error("expected non-empty AssignmentID in RevokeToken")
	}
	if revokeToken.Scope == "" {
		t.Error("expected non-empty Scope in RevokeToken")
	}
}

func TestAzureGrant_MaxDurationCapped(t *testing.T) {
	mock := &mockRoleAssign{}
	maxDur := 30 * time.Minute
	p := NewWithAPIs(Config{MaxDuration: types.Duration{Duration: maxDur}}, mock, okRoleDef, okUser)

	req := azureValidRequest()
	req.Duration = 2 * time.Hour

	before := time.Now()
	grant, err := p.Grant(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grant.ExpiresAt.After(before.Add(maxDur + time.Second)) {
		t.Errorf("ExpiresAt %v exceeds MaxDuration cap", grant.ExpiresAt)
	}
}

func TestAzureGrant_ConflictTreatedAsSuccess(t *testing.T) {
	conflictErr := &azcore.ResponseError{StatusCode: http.StatusConflict}
	mock := &mockRoleAssign{createErr: conflictErr}
	p := NewWithAPIs(Config{}, mock, okRoleDef, okUser)

	grant, err := p.Grant(context.Background(), azureValidRequest())
	if err != nil {
		t.Fatalf("expected no error on conflict (idempotent), got: %v", err)
	}
	if grant == nil {
		t.Fatal("expected non-nil grant on conflict")
	}
}

// ── Revoke ────────────────────────────────────────────────────────────────────

func azureValidGrant() providers.ElevationGrant {
	token := azureRevokeToken{
		AssignmentID: deterministicAssignmentID("req-001"),
		Scope:        "/subscriptions/sub-abc",
	}
	tokenJSON, _ := json.Marshal(token)
	return providers.ElevationGrant{
		RequestID:   "req-001",
		RevokeToken: string(tokenJSON),
		ExpiresAt:   time.Now().Add(time.Hour),
	}
}

func TestAzureRevoke_SuccessPath(t *testing.T) {
	mock := &mockRoleAssign{}
	p := NewWithAPIs(Config{}, mock, okRoleDef, okUser)

	if err := p.Revoke(context.Background(), azureValidGrant()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !mock.deleteCalled {
		t.Error("expected Delete to be called")
	}
}

func TestAzureRevoke_NotFound_ReturnsNil(t *testing.T) {
	notFoundErr := &azcore.ResponseError{StatusCode: http.StatusNotFound}
	mock := &mockRoleAssign{deleteErr: notFoundErr}
	p := NewWithAPIs(Config{}, mock, okRoleDef, okUser)

	if err := p.Revoke(context.Background(), azureValidGrant()); err != nil {
		t.Errorf("expected nil for 404, got: %v", err)
	}
}

func TestAzureRevoke_EmptyRevokeToken(t *testing.T) {
	mock := &mockRoleAssign{}
	p := NewWithAPIs(Config{}, mock, okRoleDef, okUser)
	grant := providers.ElevationGrant{RevokeToken: ""}
	if err := p.Revoke(context.Background(), grant); err != nil {
		t.Errorf("expected nil for empty RevokeToken, got: %v", err)
	}
	if mock.deleteCalled {
		t.Error("expected Delete NOT to be called for empty RevokeToken")
	}
}

// ── IsActive ──────────────────────────────────────────────────────────────────

func TestAzureIsActive_BeforeExpiry(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	grant := providers.ElevationGrant{ExpiresAt: time.Now().Add(time.Hour)}
	active, err := p.IsActive(context.Background(), grant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !active {
		t.Error("expected active=true before expiry")
	}
}

func TestAzureIsActive_AfterExpiry(t *testing.T) {
	p := NewWithAPIs(Config{}, nil, okRoleDef, okUser)
	grant := providers.ElevationGrant{ExpiresAt: time.Now().Add(-time.Hour)}
	active, err := p.IsActive(context.Background(), grant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if active {
		t.Error("expected active=false after expiry")
	}
}
