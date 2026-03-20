// License: Apache 2.0
package gcp

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/googleapi"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	"github.com/jitsudo-dev/jitsudo/pkg/types"
)

// ── Mock CRM ──────────────────────────────────────────────────────────────────

type mockCRM struct {
	mu        sync.Mutex
	getOut    *cloudresourcemanager.Policy
	getErr    error
	setOut    *cloudresourcemanager.Policy
	setErr    error
	setCalled int
}

func (m *mockCRM) GetIamPolicy(_ string, _ *cloudresourcemanager.GetIamPolicyRequest) (*cloudresourcemanager.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getOut, m.getErr
}

func (m *mockCRM) SetIamPolicy(_ string, _ *cloudresourcemanager.SetIamPolicyRequest) (*cloudresourcemanager.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setCalled++
	if m.setOut != nil {
		return m.setOut, m.setErr
	}
	return m.getOut, m.setErr
}

func emptyPolicy() *cloudresourcemanager.Policy {
	return &cloudresourcemanager.Policy{Version: 3}
}

// ── conditionTitle ────────────────────────────────────────────────────────────

func TestConditionTitle_DefaultPrefix(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	got := p.conditionTitle("req-abc")
	want := "jitsudo-req-abc"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConditionTitle_CustomPrefix(t *testing.T) {
	p := NewWithCRM(Config{ConditionTitlePrefix: "myorg"}, nil)
	got := p.conditionTitle("req-abc")
	want := "myorg-req-abc"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ── memberString ──────────────────────────────────────────────────────────────

func TestMemberString_UserEmail(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	got := p.memberString("user@example.com")
	want := "user:user@example.com"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMemberString_ServiceAccount(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	got := p.memberString("sa@project.iam.gserviceaccount.com")
	want := "serviceAccount:sa@project.iam.gserviceaccount.com"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ── roleString ────────────────────────────────────────────────────────────────

func TestRoleString_ShortName(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	got := p.roleString("viewer")
	want := "roles/viewer"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRoleString_AlreadyFullPath(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	got := p.roleString("roles/viewer")
	want := "roles/viewer"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRoleString_OrgCustomRole(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	got := p.roleString("organizations/123/roles/foo")
	want := "organizations/123/roles/foo"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ── gcpCredentials ────────────────────────────────────────────────────────────

func TestGCPCredentials_ContainsProjectKey(t *testing.T) {
	creds := gcpCredentials("my-project")
	val, ok := creds["GOOGLE_CLOUD_PROJECT"]
	if !ok {
		t.Fatal("missing GOOGLE_CLOUD_PROJECT key")
	}
	if val != "my-project" {
		t.Errorf("got %q, want %q", val, "my-project")
	}
}

// ── isHTTP409 ─────────────────────────────────────────────────────────────────

func TestIsHTTP409_True(t *testing.T) {
	err := &googleapi.Error{Code: http.StatusConflict}
	if !isHTTP409(err) {
		t.Error("expected true for 409 error")
	}
}

func TestIsHTTP409_NilError(t *testing.T) {
	if isHTTP409(nil) {
		t.Error("expected false for nil error")
	}
}

func TestIsHTTP409_OtherCode(t *testing.T) {
	err := &googleapi.Error{Code: http.StatusBadRequest}
	if isHTTP409(err) {
		t.Error("expected false for 400 error")
	}
}

// ── Name ──────────────────────────────────────────────────────────────────────

func TestGCPName(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	if got := p.Name(); got != "gcp" {
		t.Errorf("Name() = %q, want %q", got, "gcp")
	}
}

// ── ValidateRequest ───────────────────────────────────────────────────────────

func gcpValidRequest() providers.ElevationRequest {
	return providers.ElevationRequest{
		RequestID:     "req-001",
		UserIdentity:  "user@example.com",
		RoleName:      "viewer",
		ResourceScope: "my-project",
		Duration:      time.Hour,
	}
}

func TestGCPValidateRequest_AcceptsValid(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	if err := p.ValidateRequest(context.Background(), gcpValidRequest()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGCPValidateRequest_RejectsEmptyRequestID(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	req := gcpValidRequest()
	req.RequestID = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty RequestID")
	}
}

func TestGCPValidateRequest_RejectsEmptyUserIdentity(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	req := gcpValidRequest()
	req.UserIdentity = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty UserIdentity")
	}
}

func TestGCPValidateRequest_RejectsEmptyRoleName(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	req := gcpValidRequest()
	req.RoleName = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty RoleName")
	}
}

func TestGCPValidateRequest_RejectsEmptyResourceScope(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	req := gcpValidRequest()
	req.ResourceScope = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty ResourceScope")
	}
}

func TestGCPValidateRequest_RejectsZeroDuration(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	req := gcpValidRequest()
	req.Duration = 0
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for zero Duration")
	}
}

// ── Grant ─────────────────────────────────────────────────────────────────────

func TestGCPGrant_SuccessNewBinding(t *testing.T) {
	crm := &mockCRM{getOut: emptyPolicy(), setOut: emptyPolicy()}
	p := NewWithCRM(Config{}, crm)

	grant, err := p.Grant(context.Background(), gcpValidRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grant == nil {
		t.Fatal("expected non-nil grant")
	}
	if grant.RequestID != "req-001" {
		t.Errorf("RequestID = %q, want %q", grant.RequestID, "req-001")
	}
	if val, ok := grant.Credentials["GOOGLE_CLOUD_PROJECT"]; !ok || val != "my-project" {
		t.Errorf("expected GOOGLE_CLOUD_PROJECT=my-project, got %v", grant.Credentials)
	}
	if crm.setCalled == 0 {
		t.Error("expected SetIamPolicy to be called")
	}
}

func TestGCPGrant_Idempotent_ExistingBinding(t *testing.T) {
	// Policy already has the binding with the matching condition title and member.
	p := NewWithCRM(Config{}, nil)
	condTitle := p.conditionTitle("req-001")
	member := p.memberString("user@example.com")
	role := p.roleString("viewer")

	policy := &cloudresourcemanager.Policy{
		Version: 3,
		Bindings: []*cloudresourcemanager.Binding{
			{
				Role:    role,
				Members: []string{member},
				Condition: &cloudresourcemanager.Expr{
					Title: condTitle,
				},
			},
		},
	}
	crm := &mockCRM{getOut: policy}
	p = NewWithCRM(Config{}, crm)

	grant, err := p.Grant(context.Background(), gcpValidRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grant == nil {
		t.Fatal("expected non-nil grant")
	}
	if crm.setCalled != 0 {
		t.Errorf("expected SetIamPolicy NOT called for idempotent grant, got %d calls", crm.setCalled)
	}
}

func TestGCPGrant_MaxDurationCapped(t *testing.T) {
	crm := &mockCRM{getOut: emptyPolicy(), setOut: emptyPolicy()}
	maxDur := 30 * time.Minute
	p := NewWithCRM(Config{MaxDuration: types.Duration{Duration: maxDur}}, crm)

	req := gcpValidRequest()
	req.Duration = 2 * time.Hour

	before := time.Now()
	grant, err := p.Grant(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ExpiresAt should be at most maxDur after the grant was issued.
	if grant.ExpiresAt.After(before.Add(maxDur + time.Second)) {
		t.Errorf("ExpiresAt %v exceeds MaxDuration cap", grant.ExpiresAt)
	}
}

// ── Revoke ────────────────────────────────────────────────────────────────────

func gcpValidGrant(p *Provider) providers.ElevationGrant {
	token := gcpRevokeToken{
		Project:        "projects/my-project",
		Member:         p.memberString("user@example.com"),
		Role:           p.roleString("viewer"),
		ConditionTitle: p.conditionTitle("req-001"),
	}
	tokenJSON, _ := json.Marshal(token)
	return providers.ElevationGrant{
		RequestID:   "req-001",
		RevokeToken: string(tokenJSON),
		ExpiresAt:   time.Now().Add(time.Hour),
	}
}

func TestGCPRevoke_RemovesMember(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	grant := gcpValidGrant(p)

	var tok gcpRevokeToken
	_ = json.Unmarshal([]byte(grant.RevokeToken), &tok)

	policy := &cloudresourcemanager.Policy{
		Version: 3,
		Bindings: []*cloudresourcemanager.Binding{
			{
				Role:    tok.Role,
				Members: []string{tok.Member},
				Condition: &cloudresourcemanager.Expr{
					Title: tok.ConditionTitle,
				},
			},
		},
	}
	crm := &mockCRM{getOut: policy, setOut: emptyPolicy()}
	p = NewWithCRM(Config{}, crm)

	if err := p.Revoke(context.Background(), grant); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if crm.setCalled == 0 {
		t.Error("expected SetIamPolicy to be called on revoke")
	}
}

func TestGCPRevoke_Idempotent_MemberNotInPolicy(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	grant := gcpValidGrant(p)

	// Policy has no matching binding.
	crm := &mockCRM{getOut: emptyPolicy()}
	p = NewWithCRM(Config{}, crm)

	if err := p.Revoke(context.Background(), grant); err != nil {
		t.Errorf("expected nil for member not in policy, got: %v", err)
	}
	if crm.setCalled != 0 {
		t.Errorf("expected SetIamPolicy NOT called, got %d calls", crm.setCalled)
	}
}

func TestGCPRevoke_EmptyRevokeToken(t *testing.T) {
	crm := &mockCRM{}
	p := NewWithCRM(Config{}, crm)
	grant := providers.ElevationGrant{RevokeToken: ""}
	if err := p.Revoke(context.Background(), grant); err != nil {
		t.Errorf("expected nil for empty RevokeToken, got: %v", err)
	}
}

// ── IsActive ──────────────────────────────────────────────────────────────────

func TestGCPIsActive_BeforeExpiry(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	grant := providers.ElevationGrant{ExpiresAt: time.Now().Add(time.Hour)}
	active, err := p.IsActive(context.Background(), grant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !active {
		t.Error("expected active=true before expiry")
	}
}

func TestGCPIsActive_AfterExpiry(t *testing.T) {
	p := NewWithCRM(Config{}, nil)
	grant := providers.ElevationGrant{ExpiresAt: time.Now().Add(-time.Hour)}
	active, err := p.IsActive(context.Background(), grant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if active {
		t.Error("expected active=false after expiry")
	}
}
