// License: Elastic License 2.0 (ELv2)
package policy

import (
	"context"
	"testing"

	"github.com/open-policy-agent/opa/v1/rego"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// newEngineWithTierPolicy creates an Engine whose approvalTierQuery is compiled
// from the provided Rego string. No database is required.
func newEngineWithTierPolicy(t *testing.T, regoStr string) *Engine {
	t.Helper()
	opts := []func(*rego.Rego){
		rego.Query("data.jitsudo.approval.approver_tier"),
		rego.Module("test.rego", regoStr),
	}
	pq, err := rego.New(opts...).PrepareForEval(context.Background())
	if err != nil {
		t.Fatalf("PrepareForEval: %v", err)
	}
	e := &Engine{}
	e.approvalTierQuery = &pq
	return e
}

func testIdentity() *auth.Identity {
	return &auth.Identity{
		Email:   "alice@example.com",
		Subject: "alice",
		Groups:  []string{"sre-team"},
	}
}

func testInput(role string) *jitsudov1alpha1.CreateRequestInput {
	return &jitsudov1alpha1.CreateRequestInput{
		Provider:        "aws",
		Role:            role,
		ResourceScope:   "123456789012",
		DurationSeconds: 3600,
	}
}

// TestEvalApprovalTier_Auto verifies that a policy returning "auto" is honored.
func TestEvalApprovalTier_Auto(t *testing.T) {
	e := newEngineWithTierPolicy(t, `
package jitsudo.approval
import rego.v1

default approver_tier := "human"

approver_tier := "auto" if {
    input.request.role == "readonly"
    input.request.duration_seconds <= 3600
    input.user.groups[_] == "sre-team"
}
`)
	tier, err := e.EvalApprovalTier(context.Background(), testIdentity(), testInput("readonly"))
	if err != nil {
		t.Fatalf("EvalApprovalTier: %v", err)
	}
	if tier != "auto" {
		t.Errorf("got tier %q, want %q", tier, "auto")
	}
}

// TestEvalApprovalTier_HumanDefault verifies that a policy without an
// approver_tier rule returns "human" (the safe default).
func TestEvalApprovalTier_HumanDefault(t *testing.T) {
	e := newEngineWithTierPolicy(t, `
package jitsudo.approval

default allow := true
`)
	tier, err := e.EvalApprovalTier(context.Background(), testIdentity(), testInput("admin"))
	if err != nil {
		t.Fatalf("EvalApprovalTier: %v", err)
	}
	if tier != "human" {
		t.Errorf("got tier %q, want %q", tier, "human")
	}
}

// TestEvalApprovalTier_AIReview verifies that "ai_review" is passed through.
func TestEvalApprovalTier_AIReview(t *testing.T) {
	e := newEngineWithTierPolicy(t, `
package jitsudo.approval
import rego.v1

default approver_tier := "human"

approver_tier := "ai_review" if {
    input.request.role == "ai-review-role"
}
`)
	tier, err := e.EvalApprovalTier(context.Background(), testIdentity(), testInput("ai-review-role"))
	if err != nil {
		t.Fatalf("EvalApprovalTier: %v", err)
	}
	if tier != "ai_review" {
		t.Errorf("got tier %q, want %q", tier, "ai_review")
	}
}

// TestEvalApprovalTier_InvalidValue verifies that an unrecognised tier string
// falls back to "human".
func TestEvalApprovalTier_InvalidValue(t *testing.T) {
	e := newEngineWithTierPolicy(t, `
package jitsudo.approval

approver_tier := "unknown_tier"
`)
	tier, err := e.EvalApprovalTier(context.Background(), testIdentity(), testInput("some-role"))
	if err != nil {
		t.Fatalf("EvalApprovalTier: %v", err)
	}
	if tier != "human" {
		t.Errorf("got tier %q, want %q (should sanitise unknown tier)", tier, "human")
	}
}

// TestEvalApprovalTier_NilQuery verifies that a nil tier query returns "human".
func TestEvalApprovalTier_NilQuery(t *testing.T) {
	e := &Engine{} // approvalTierQuery is nil
	tier, err := e.EvalApprovalTier(context.Background(), testIdentity(), testInput("admin"))
	if err != nil {
		t.Fatalf("EvalApprovalTier: %v", err)
	}
	if tier != "human" {
		t.Errorf("got tier %q, want %q", tier, "human")
	}
}

// TestEvalApprovalTier_ConditionNotMet verifies that a policy whose condition
// is not met falls back to "human" via the default rule.
func TestEvalApprovalTier_ConditionNotMet(t *testing.T) {
	e := newEngineWithTierPolicy(t, `
package jitsudo.approval
import rego.v1

default approver_tier := "human"

approver_tier := "auto" if {
    input.request.role == "readonly"
}
`)
	// Role is "admin" — condition not met, should get "human"
	tier, err := e.EvalApprovalTier(context.Background(), testIdentity(), testInput("admin"))
	if err != nil {
		t.Fatalf("EvalApprovalTier: %v", err)
	}
	if tier != "human" {
		t.Errorf("got tier %q, want %q", tier, "human")
	}
}

// TestEvalApprovalTier_TrustTierInInput verifies that input.context.trust_tier is
// wired into the OPA input document. When no store is configured, trust_tier defaults
// to 0, so a policy requiring trust_tier >= 2 should fall back to "human".
func TestEvalApprovalTier_TrustTierInInput(t *testing.T) {
	e := newEngineWithTierPolicy(t, `
package jitsudo.approval
import rego.v1

default approver_tier := "human"

approver_tier := "auto" if {
    input.context.trust_tier >= 2
    input.request.role == "readonly"
}
`)
	// Engine has no store → principalTrustTier returns 0, condition not met.
	tier, err := e.EvalApprovalTier(context.Background(), testIdentity(), testInput("readonly"))
	if err != nil {
		t.Fatalf("EvalApprovalTier: %v", err)
	}
	if tier != "human" {
		t.Errorf("got tier %q, want %q (trust_tier=0 should not satisfy >= 2)", tier, "human")
	}
}

// ── EvalEligibility tests ───────────────────────────────────────────────────

// newEngineWithEligibilityPolicy creates an Engine whose eligibilityQuery is
// compiled from the provided Rego string. No database is required.
func newEngineWithEligibilityPolicy(t *testing.T, regoStr string) *Engine {
	t.Helper()
	opts := []func(*rego.Rego){
		rego.Query("data.jitsudo.eligibility.allow"),
		rego.Module("test.rego", regoStr),
	}
	pq, err := rego.New(opts...).PrepareForEval(context.Background())
	if err != nil {
		t.Fatalf("PrepareForEval: %v", err)
	}
	e := &Engine{}
	e.eligibilityQuery = &pq
	return e
}

// TestEvalEligibility_NilQuery verifies that a nil eligibility query denies
// with the "policy engine not loaded" reason.
func TestEvalEligibility_NilQuery(t *testing.T) {
	e := &Engine{} // eligibilityQuery is nil
	allowed, reason, err := e.EvalEligibility(context.Background(), testIdentity(), testInput("admin"))
	if err != nil {
		t.Fatalf("EvalEligibility: %v", err)
	}
	if allowed {
		t.Error("expected denied, got allowed")
	}
	if reason != "policy engine not loaded" {
		t.Errorf("reason = %q, want %q", reason, "policy engine not loaded")
	}
}

// TestEvalEligibility_Allow verifies that a matching eligibility policy returns allowed=true.
func TestEvalEligibility_Allow(t *testing.T) {
	e := newEngineWithEligibilityPolicy(t, `
package jitsudo.eligibility
import rego.v1

default allow := false

allow if {
    input.user.groups[_] == "sre-team"
}
`)
	allowed, _, err := e.EvalEligibility(context.Background(), testIdentity(), testInput("readonly"))
	if err != nil {
		t.Fatalf("EvalEligibility: %v", err)
	}
	if !allowed {
		t.Error("expected allowed, got denied")
	}
}

// TestEvalEligibility_DenyWrongGroup verifies denial when the user lacks the required group.
func TestEvalEligibility_DenyWrongGroup(t *testing.T) {
	e := newEngineWithEligibilityPolicy(t, `
package jitsudo.eligibility
import rego.v1

default allow := false

allow if {
    input.user.groups[_] == "prod-admins"
}
`)
	// testIdentity() has groups=["sre-team"], not "prod-admins"
	allowed, _, err := e.EvalEligibility(context.Background(), testIdentity(), testInput("admin"))
	if err != nil {
		t.Fatalf("EvalEligibility: %v", err)
	}
	if allowed {
		t.Error("expected denied for wrong group, got allowed")
	}
}

// TestEvalEligibility_DenyDurationExceeded verifies denial when request duration
// exceeds the policy limit.
func TestEvalEligibility_DenyDurationExceeded(t *testing.T) {
	e := newEngineWithEligibilityPolicy(t, `
package jitsudo.eligibility
import rego.v1

default allow := false

allow if {
    input.request.duration_seconds <= 1800
}
`)
	// testInput sets DurationSeconds=3600, which exceeds the 1800s limit.
	allowed, _, err := e.EvalEligibility(context.Background(), testIdentity(), testInput("readonly"))
	if err != nil {
		t.Fatalf("EvalEligibility: %v", err)
	}
	if allowed {
		t.Error("expected denied for duration > 1800s, got allowed")
	}
}

// ── EvalApproval tests ──────────────────────────────────────────────────────

// newEngineWithApprovalPolicy creates an Engine whose approvalQuery is compiled
// from the provided Rego string. No database is required.
func newEngineWithApprovalPolicy(t *testing.T, regoStr string) *Engine {
	t.Helper()
	opts := []func(*rego.Rego){
		rego.Query("data.jitsudo.approval.allow"),
		rego.Module("test.rego", regoStr),
	}
	pq, err := rego.New(opts...).PrepareForEval(context.Background())
	if err != nil {
		t.Fatalf("PrepareForEval: %v", err)
	}
	e := &Engine{}
	e.approvalQuery = &pq
	return e
}

// TestEvalApproval_NilQuery verifies that a nil approval query denies with
// the "policy engine not loaded" reason.
func TestEvalApproval_NilQuery(t *testing.T) {
	e := &Engine{} // approvalQuery is nil
	allowed, reason, err := e.EvalApproval(context.Background(), testIdentity(), &store.RequestRow{
		Provider: "aws", Role: "admin", ResourceScope: "123456789012", DurationSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("EvalApproval: %v", err)
	}
	if allowed {
		t.Error("expected denied, got allowed")
	}
	if reason != "policy engine not loaded" {
		t.Errorf("reason = %q, want %q", reason, "policy engine not loaded")
	}
}

// TestEvalApproval_Allow verifies that a matching approval policy returns allowed=true.
func TestEvalApproval_Allow(t *testing.T) {
	e := newEngineWithApprovalPolicy(t, `
package jitsudo.approval
import rego.v1

default allow := false

allow if {
    input.user.groups[_] == "sre-team"
}
`)
	allowed, _, err := e.EvalApproval(context.Background(), testIdentity(), &store.RequestRow{
		Provider: "aws", Role: "readonly", ResourceScope: "123456789012", DurationSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("EvalApproval: %v", err)
	}
	if !allowed {
		t.Error("expected allowed, got denied")
	}
}

// TestEvalApproval_Deny verifies that a non-matching approval policy returns allowed=false.
func TestEvalApproval_Deny(t *testing.T) {
	e := newEngineWithApprovalPolicy(t, `
package jitsudo.approval
import rego.v1

default allow := false

allow if {
    input.user.groups[_] == "security-team"
}
`)
	// testIdentity() has groups=["sre-team"], not "security-team"
	allowed, _, err := e.EvalApproval(context.Background(), testIdentity(), &store.RequestRow{
		Provider: "aws", Role: "admin", ResourceScope: "123456789012", DurationSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("EvalApproval: %v", err)
	}
	if allowed {
		t.Error("expected denied for wrong group, got allowed")
	}
}
