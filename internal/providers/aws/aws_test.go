// License: Apache 2.0
package aws

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	"github.com/jitsudo-dev/jitsudo/pkg/types"
)

// ── Mock implementations ──────────────────────────────────────────────────────

type mockSTS struct {
	out *sts.AssumeRoleOutput
	err error
}

func (m *mockSTS) AssumeRole(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	return m.out, m.err
}

type mockIAM struct {
	err error
}

func (m *mockIAM) PutRolePolicy(_ context.Context, _ *iam.PutRolePolicyInput, _ ...func(*iam.Options)) (*iam.PutRolePolicyOutput, error) {
	return &iam.PutRolePolicyOutput{}, m.err
}

// ── buildRoleARN ──────────────────────────────────────────────────────────────

func TestBuildRoleARN_ValidTemplate(t *testing.T) {
	p := NewWithClients(Config{
		RoleARNTemplate: "arn:aws:iam::{scope}:role/jitsudo-{role}",
	}, nil, nil)

	req := providers.ElevationRequest{ResourceScope: "123456789012", RoleName: "admin"}
	got, err := p.buildRoleARN(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "arn:aws:iam::123456789012:role/jitsudo-admin"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRoleARN_EmptyTemplate_ReturnsError(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	req := providers.ElevationRequest{ResourceScope: "123456789012", RoleName: "admin"}
	_, err := p.buildRoleARN(req)
	if err == nil {
		t.Fatal("expected error for empty template, got nil")
	}
}

// ── sessionName ───────────────────────────────────────────────────────────────

func TestSessionName_PrependPrefix(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	got := p.sessionName("req-123")
	want := "jitsudo-req-123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSessionName_LongInputTruncated(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	// Build a request ID that will produce a name longer than 64 chars after "jitsudo-".
	long := "12345678901234567890123456789012345678901234567890123456789" // 59 chars
	got := p.sessionName(long)
	if len(got) > 64 {
		t.Errorf("session name length %d exceeds 64", len(got))
	}
	if len(got) != 64 {
		t.Errorf("expected exactly 64 chars, got %d", len(got))
	}
}

func TestSessionName_SpecialCharsReplaced(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	got := p.sessionName("req/with spaces!and#chars")
	for _, c := range got {
		valid := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '+' || c == '=' || c == ',' || c == '.' || c == '@' || c == '-' || c == '_'
		if !valid {
			t.Errorf("invalid character %q in session name %q", c, got)
		}
	}
}

// ── capDuration ───────────────────────────────────────────────────────────────

func TestCapDuration_RespectsMaxDuration(t *testing.T) {
	p := NewWithClients(Config{
		MaxDuration: types.Duration{Duration: 2 * time.Hour},
	}, nil, nil)
	got := p.capDuration(4 * time.Hour)
	if got != 2*time.Hour {
		t.Errorf("expected 2h, got %v", got)
	}
}

func TestCapDuration_CapsAt12h(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	got := p.capDuration(24 * time.Hour)
	if got != 12*time.Hour {
		t.Errorf("expected 12h, got %v", got)
	}
}

func TestCapDuration_FloorsAt15min(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	got := p.capDuration(5 * time.Minute)
	if got != 15*time.Minute {
		t.Errorf("expected 15m, got %v", got)
	}
}

func TestCapDuration_WithinBoundsUnchanged(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	got := p.capDuration(time.Hour)
	if got != time.Hour {
		t.Errorf("expected 1h, got %v", got)
	}
}

// ── denyPolicyDocument ────────────────────────────────────────────────────────

func TestDenyPolicyDocument_ValidJSON(t *testing.T) {
	issuedAt := time.Now().UTC()
	doc := denyPolicyDocument(issuedAt)

	var policy struct {
		Version   string `json:"Version"`
		Statement []struct {
			Effect    string          `json:"Effect"`
			Action    string          `json:"Action"`
			Condition json.RawMessage `json:"Condition"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(doc), &policy); err != nil {
		t.Fatalf("invalid JSON: %v\ndoc: %s", err, doc)
	}
	if len(policy.Statement) == 0 {
		t.Fatal("expected at least one statement")
	}
	stmt := policy.Statement[0]
	if stmt.Effect != "Deny" {
		t.Errorf("expected Effect=Deny, got %q", stmt.Effect)
	}
	if stmt.Action != "*" {
		t.Errorf("expected Action=*, got %q", stmt.Action)
	}

	// Verify Condition contains DateLessThanEquals.
	var cond map[string]json.RawMessage
	if err := json.Unmarshal(stmt.Condition, &cond); err != nil {
		t.Fatalf("parse Condition: %v", err)
	}
	if _, ok := cond["DateLessThanEquals"]; !ok {
		t.Errorf("expected DateLessThanEquals in Condition, got keys: %v", cond)
	}
}

// ── Name ──────────────────────────────────────────────────────────────────────

func TestName(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	if got := p.Name(); got != "aws" {
		t.Errorf("Name() = %q, want %q", got, "aws")
	}
}

// ── ValidateRequest ───────────────────────────────────────────────────────────

func validRequest() providers.ElevationRequest {
	return providers.ElevationRequest{
		RequestID:     "req-001",
		UserIdentity:  "user@example.com",
		RoleName:      "admin",
		ResourceScope: "123456789012",
		Duration:      time.Hour,
	}
}

func TestValidateRequest_AcceptsValid(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	if err := p.ValidateRequest(context.Background(), validRequest()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRequest_RejectsEmptyRequestID(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	req := validRequest()
	req.RequestID = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty RequestID")
	}
}

func TestValidateRequest_RejectsEmptyUserIdentity(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	req := validRequest()
	req.UserIdentity = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty UserIdentity")
	}
}

func TestValidateRequest_RejectsEmptyRoleName(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	req := validRequest()
	req.RoleName = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty RoleName")
	}
}

func TestValidateRequest_RejectsEmptyResourceScope(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	req := validRequest()
	req.ResourceScope = ""
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for empty ResourceScope")
	}
}

func TestValidateRequest_RejectsZeroDuration(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	req := validRequest()
	req.Duration = 0
	if err := p.ValidateRequest(context.Background(), req); err == nil {
		t.Error("expected error for zero Duration")
	}
}

// ── Grant ─────────────────────────────────────────────────────────────────────

func makeSTSOutput() *sts.AssumeRoleOutput {
	keyID := "AKIAIOSFODNN7EXAMPLE"
	secret := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	token := "AQoDYXdzEJr"
	exp := time.Now().Add(time.Hour)
	return &sts.AssumeRoleOutput{
		Credentials: &ststypes.Credentials{
			AccessKeyId:     &keyID,
			SecretAccessKey: &secret,
			SessionToken:    &token,
			Expiration:      &exp,
		},
	}
}

func TestGrant_SuccessPath(t *testing.T) {
	out := makeSTSOutput()
	p := NewWithClients(Config{
		RoleARNTemplate: "arn:aws:iam::{scope}:role/jitsudo-{role}",
		Region:          "us-west-2",
	}, &mockSTS{out: out}, &mockIAM{})

	grant, err := p.Grant(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grant == nil {
		t.Fatal("expected non-nil grant")
	}
	if grant.RequestID != "req-001" {
		t.Errorf("RequestID = %q, want %q", grant.RequestID, "req-001")
	}

	// Verify all 4 credential keys are present.
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_DEFAULT_REGION"} {
		if _, ok := grant.Credentials[key]; !ok {
			t.Errorf("missing credential key %q", key)
		}
	}
	if grant.Credentials["AWS_ACCESS_KEY_ID"] != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("unexpected access key ID: %q", grant.Credentials["AWS_ACCESS_KEY_ID"])
	}
	if grant.Credentials["AWS_DEFAULT_REGION"] != "us-west-2" {
		t.Errorf("unexpected region: %q", grant.Credentials["AWS_DEFAULT_REGION"])
	}
}

func TestGrant_DefaultRegion(t *testing.T) {
	out := makeSTSOutput()
	p := NewWithClients(Config{
		RoleARNTemplate: "arn:aws:iam::{scope}:role/jitsudo-{role}",
	}, &mockSTS{out: out}, &mockIAM{})

	grant, err := p.Grant(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grant.Credentials["AWS_DEFAULT_REGION"] != "us-east-1" {
		t.Errorf("expected default region us-east-1, got %q", grant.Credentials["AWS_DEFAULT_REGION"])
	}
}

func TestGrant_STSError(t *testing.T) {
	p := NewWithClients(Config{
		RoleARNTemplate: "arn:aws:iam::{scope}:role/jitsudo-{role}",
	}, &mockSTS{err: errTest("sts: access denied")}, &mockIAM{})

	_, err := p.Grant(context.Background(), validRequest())
	if err == nil {
		t.Fatal("expected error when STS fails")
	}
}

// ── Revoke ────────────────────────────────────────────────────────────────────

func validGrant() providers.ElevationGrant {
	token := awsRevokeToken{
		RoleARN:     "arn:aws:iam::123456789012:role/jitsudo-admin",
		SessionName: "jitsudo-req-001",
		IssuedAt:    time.Now().UTC(),
	}
	tokenJSON, _ := json.Marshal(token)
	return providers.ElevationGrant{
		RequestID:   "req-001",
		RevokeToken: string(tokenJSON),
		ExpiresAt:   time.Now().Add(time.Hour),
	}
}

func TestRevoke_SuccessPath(t *testing.T) {
	p := NewWithClients(Config{}, nil, &mockIAM{})
	if err := p.Revoke(context.Background(), validGrant()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRevoke_NoSuchEntityException(t *testing.T) {
	noSuchEntity := &iamtypes.NoSuchEntityException{Message: strPtr("role not found")}
	p := NewWithClients(Config{}, nil, &mockIAM{err: noSuchEntity})
	if err := p.Revoke(context.Background(), validGrant()); err != nil {
		t.Errorf("expected nil for NoSuchEntityException, got: %v", err)
	}
}

func TestRevoke_EmptyRevokeToken(t *testing.T) {
	p := NewWithClients(Config{}, nil, &mockIAM{})
	grant := providers.ElevationGrant{RevokeToken: ""}
	if err := p.Revoke(context.Background(), grant); err != nil {
		t.Errorf("expected nil for empty RevokeToken, got: %v", err)
	}
}

// ── IsActive ──────────────────────────────────────────────────────────────────

func TestIsActive_BeforeExpiry(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	grant := providers.ElevationGrant{ExpiresAt: time.Now().Add(time.Hour)}
	active, err := p.IsActive(context.Background(), grant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !active {
		t.Error("expected active=true before expiry")
	}
}

func TestIsActive_AfterExpiry(t *testing.T) {
	p := NewWithClients(Config{}, nil, nil)
	grant := providers.ElevationGrant{ExpiresAt: time.Now().Add(-time.Hour)}
	active, err := p.IsActive(context.Background(), grant)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if active {
		t.Error("expected active=false after expiry")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

type errTest string

func (e errTest) Error() string { return string(e) }
