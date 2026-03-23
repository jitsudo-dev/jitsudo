// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

// Package policy wraps the embedded OPA engine for eligibility and approval
// policy evaluation. Policies are written in Rego and stored in PostgreSQL.
package policy

import (
	"context"
	"fmt"
	"sync"

	"github.com/open-policy-agent/opa/v1/rego"
	"github.com/rs/zerolog/log"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// Engine evaluates OPA eligibility and approval policies loaded from PostgreSQL.
type Engine struct {
	store *store.Store

	mu                sync.RWMutex
	eligibilityQuery  *rego.PreparedEvalQuery
	approvalQuery     *rego.PreparedEvalQuery
	approvalTierQuery *rego.PreparedEvalQuery
}

// NewEngine returns an Engine. Call Reload before first use.
func NewEngine(s *store.Store) *Engine {
	return &Engine{store: s}
}

// Reload fetches all enabled policies from the DB and recompiles the OPA queries.
// Call at startup and after every ApplyPolicy / DeletePolicy RPC.
func (e *Engine) Reload(ctx context.Context) error {
	eliq, err := e.buildQuery(ctx, store.PolicyTypeEligibility, "data.jitsudo.eligibility.allow")
	if err != nil {
		return fmt.Errorf("policy: reload eligibility: %w", err)
	}
	appq, err := e.buildQuery(ctx, store.PolicyTypeApproval, "data.jitsudo.approval.allow")
	if err != nil {
		return fmt.Errorf("policy: reload approval: %w", err)
	}
	tierq, err := e.buildQuery(ctx, store.PolicyTypeApproval, "data.jitsudo.approval.approver_tier")
	if err != nil {
		return fmt.Errorf("policy: reload approver_tier: %w", err)
	}

	e.mu.Lock()
	e.eligibilityQuery = eliq
	e.approvalQuery = appq
	e.approvalTierQuery = tierq
	e.mu.Unlock()

	log.Info().Msg("policy engine reloaded")
	return nil
}

// EvalEligibility checks whether an identity is eligible to submit the request.
// Returns (allowed, reason, error).
func (e *Engine) EvalEligibility(ctx context.Context, identity *auth.Identity, input *jitsudov1alpha1.CreateRequestInput) (bool, string, error) {
	e.mu.RLock()
	q := e.eligibilityQuery
	e.mu.RUnlock()
	if q == nil {
		return false, "policy engine not loaded", nil
	}
	tier := e.principalTrustTier(ctx, identity.Email)
	return e.eval(ctx, q, buildInput(identity, input.GetProvider(), input.GetRole(), input.GetResourceScope(), input.GetDurationSeconds(), tier))
}

// EvalApprovalTier evaluates data.jitsudo.approval.approver_tier to determine
// which approval path the request should follow.
// Returns "auto", "ai_review", or "human" (the safe default when the rule is absent).
func (e *Engine) EvalApprovalTier(ctx context.Context, identity *auth.Identity, input *jitsudov1alpha1.CreateRequestInput) (string, error) {
	e.mu.RLock()
	q := e.approvalTierQuery
	e.mu.RUnlock()
	if q == nil {
		return "human", nil
	}
	tier := e.principalTrustTier(ctx, identity.Email)
	return e.evalTier(ctx, q, buildInput(identity, input.GetProvider(), input.GetRole(), input.GetResourceScope(), input.GetDurationSeconds(), tier))
}

// EvalApproval checks whether the approver is authorised to approve the request.
// Returns (allowed, reason, error).
func (e *Engine) EvalApproval(ctx context.Context, approver *auth.Identity, req *store.RequestRow) (bool, string, error) {
	e.mu.RLock()
	q := e.approvalQuery
	e.mu.RUnlock()
	if q == nil {
		return false, "policy engine not loaded", nil
	}
	tier := e.principalTrustTier(ctx, approver.Email)
	return e.eval(ctx, q, buildInput(approver, req.Provider, req.Role, req.ResourceScope, req.DurationSeconds, tier))
}

// EvalRaw evaluates the given policy type against a pre-built OPA input map.
// This is used by the EvalPolicy RPC for dry-run evaluation.
func (e *Engine) EvalRaw(ctx context.Context, ptype store.PolicyType, input map[string]any) (bool, string, error) {
	e.mu.RLock()
	var q *rego.PreparedEvalQuery
	if ptype == store.PolicyTypeApproval {
		q = e.approvalQuery
	} else {
		q = e.eligibilityQuery
	}
	e.mu.RUnlock()
	if q == nil {
		return false, "policy engine not loaded", nil
	}
	return e.eval(ctx, q, input)
}

// buildQuery compiles all enabled policies of ptype into a PreparedEvalQuery.
func (e *Engine) buildQuery(ctx context.Context, ptype store.PolicyType, query string) (*rego.PreparedEvalQuery, error) {
	policies, err := e.store.ListEnabledPoliciesByType(ctx, ptype)
	if err != nil {
		return nil, err
	}

	opts := []func(*rego.Rego){rego.Query(query)}
	for _, p := range policies {
		opts = append(opts, rego.Module(p.Name+".rego", p.Rego))
	}

	// If no policies are loaded, use a deny-all default so the system is safe.
	if len(policies) == 0 {
		var pkg string
		if ptype == store.PolicyTypeEligibility {
			pkg = "jitsudo.eligibility"
		} else {
			pkg = "jitsudo.approval"
		}
		opts = append(opts, rego.Module("default_deny.rego",
			fmt.Sprintf("package %s\ndefault allow := false", pkg)))
	}

	pq, err := rego.New(opts...).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("policy: compile %s: %w", ptype, err)
	}
	return &pq, nil
}

// eval runs a prepared query and extracts the allow + reason values.
func (e *Engine) eval(ctx context.Context, q *rego.PreparedEvalQuery, input map[string]any) (bool, string, error) {
	results, err := q.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return false, "", fmt.Errorf("policy: eval: %w", err)
	}
	if len(results) == 0 || len(results[0].Expressions) == 0 {
		return false, "no policy result", nil
	}

	allowed, _ := results[0].Expressions[0].Value.(bool)

	// Extract reason from bindings if the policy sets it.
	var reason string
	if b, ok := results[0].Bindings["reason"]; ok {
		reason, _ = b.(string)
	}
	return allowed, reason, nil
}

// principalTrustTier looks up the trust tier for identity from the DB.
// Returns 0 if the principal is not found or the store is unavailable.
func (e *Engine) principalTrustTier(ctx context.Context, identity string) int {
	if e.store == nil {
		return 0
	}
	p, err := e.store.GetPrincipal(ctx, identity)
	if err != nil {
		return 0 // unknown/new principals default to tier 0
	}
	return p.TrustTier
}

// evalTier runs a prepared query and extracts the approver_tier string value.
// Returns "human" if the rule is undefined or not a string.
func (e *Engine) evalTier(ctx context.Context, q *rego.PreparedEvalQuery, input map[string]any) (string, error) {
	results, err := q.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return "human", fmt.Errorf("policy: eval tier: %w", err)
	}
	if len(results) == 0 || len(results[0].Expressions) == 0 {
		return "human", nil
	}
	tier, _ := results[0].Expressions[0].Value.(string)
	switch tier {
	case "auto", "ai_review", "human":
		return tier, nil
	default:
		return "human", nil
	}
}

// buildInput constructs the OPA input document.
// trustTier is the principal's current trust tier (0–4) from the principals table.
func buildInput(identity *auth.Identity, provider, role, resourceScope string, durationSeconds int64, trustTier int) map[string]any {
	return map[string]any{
		"user": map[string]any{
			"email":   identity.Email,
			"subject": identity.Subject,
			"groups":  identity.Groups,
		},
		"request": map[string]any{
			"provider":         provider,
			"role":             role,
			"resource_scope":   resourceScope,
			"duration_seconds": durationSeconds,
		},
		"context": map[string]any{
			"trust_tier": trustTier,
		},
	}
}
