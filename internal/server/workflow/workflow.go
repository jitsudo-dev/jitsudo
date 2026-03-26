// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

// Package workflow implements the elevation request state machine.
//
// States: PENDING -> APPROVED | REJECTED -> ACTIVE -> EXPIRED | REVOKED
//
// Every state transition writes an immutable audit log entry before updating
// the request state (write-ahead audit log pattern). Transitions use
// database row-level locking to prevent races in HA deployments.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rs/zerolog/log"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/internal/providers"
	"github.com/jitsudo-dev/jitsudo/internal/server/audit"
	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
	"github.com/jitsudo-dev/jitsudo/internal/server/notifications"
	"github.com/jitsudo-dev/jitsudo/internal/server/policy"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// engineStore is the subset of *store.Store methods used by the Engine.
// Extracted as an interface so unit tests can substitute an in-memory stub
// without requiring a live database.
type engineStore interface {
	UpsertPrincipalLastSeen(ctx context.Context, identity string) error
	CreateRequest(ctx context.Context, req *store.RequestRow) error
	GetRequest(ctx context.Context, id string) (*store.RequestRow, error)
	TransitionRequest(ctx context.Context, id string, fromState, toState store.RequestState, u store.RequestUpdate) (*store.RequestRow, error)
	SetApproverTier(ctx context.Context, id string, tier string) error
	ListActiveExpired(ctx context.Context) ([]*store.RequestRow, error)
	TryAcquireSweepLock(ctx context.Context) (bool, func(), error)
	ListPendingTimedOut(ctx context.Context) ([]*store.RequestRow, error)
	TryAcquirePendingTimeoutLock(ctx context.Context) (bool, func(), error)
}

// auditAppender is the subset of *audit.Logger used by the Engine.
type auditAppender interface {
	Append(ctx context.Context, e audit.Entry) error
}

// Engine drives elevation request lifecycle transitions.
type Engine struct {
	store         engineStore
	audit         auditAppender
	policy        *policy.Engine
	registry      *providers.Registry
	notifications *notifications.Dispatcher
}

// NewEngine wires together the store, audit logger, policy engine, provider
// registry, and optional notification dispatcher.
// Passing nil for dispatcher disables notifications.
func NewEngine(s engineStore, a auditAppender, p *policy.Engine, r *providers.Registry, n *notifications.Dispatcher) *Engine {
	return &Engine{store: s, audit: a, policy: p, registry: r, notifications: n}
}

// appendAudit writes an audit entry and logs a warning if it fails.
// Audit failures are non-fatal but must be observable for compliance monitoring.
func (e *Engine) appendAudit(ctx context.Context, entry audit.Entry) {
	if err := e.audit.Append(ctx, entry); err != nil {
		log.Warn().Err(err).Str("request_id", entry.RequestID).Msg("audit: append failed")
	}
}

// CreateRequest validates, checks eligibility, and creates a PENDING request.
// If input.BreakGlass is true, the policy eligibility check is skipped and
// the grant is issued immediately — the full PENDING→APPROVED→ACTIVE trail is
// still written to the audit log and a critical EventBreakGlass notification
// is fired.
func (e *Engine) CreateRequest(ctx context.Context, identity *auth.Identity, input *jitsudov1alpha1.CreateRequestInput) (*store.RequestRow, error) {
	// Resolve provider.
	p := e.registry.Get(input.GetProvider())
	if p == nil {
		return nil, fmt.Errorf("workflow: unknown provider %q", input.GetProvider())
	}

	// Provider-level validation (no side effects).
	provReq := providers.ElevationRequest{
		RequestID:     "validate",
		UserIdentity:  identity.Email,
		Provider:      input.GetProvider(),
		RoleName:      input.GetRole(),
		ResourceScope: input.GetResourceScope(),
		Duration:      time.Duration(input.GetDurationSeconds()) * time.Second,
		Reason:        input.GetReason(),
	}
	if err := p.ValidateRequest(ctx, provReq); err != nil {
		return nil, fmt.Errorf("workflow: validation: %w", err)
	}

	if input.GetBreakGlass() {
		return e.createBreakGlass(ctx, identity, input, p)
	}

	// Record principal activity (fire-and-forget; never blocks the request path).
	_ = e.store.UpsertPrincipalLastSeen(ctx, identity.Email)

	// Policy eligibility check.
	allowed, reason, err := e.policy.EvalEligibility(ctx, identity, input)
	if err != nil {
		return nil, fmt.Errorf("workflow: eligibility eval: %w", err)
	}
	if !allowed {
		if reason == "" {
			reason = "policy denied the request"
		}
		return nil, fmt.Errorf("workflow: not eligible: %s", reason)
	}

	// Determine approval tier from policy.
	tier, err := e.policy.EvalApprovalTier(ctx, identity, input)
	if err != nil {
		return nil, fmt.Errorf("workflow: approval tier eval: %w", err)
	}

	now := time.Now().UTC()
	req := &store.RequestRow{
		ID:                "req_" + ulid.Make().String(),
		State:             store.StatePending,
		RequesterIdentity: identity.Email,
		Provider:          input.GetProvider(),
		Role:              input.GetRole(),
		ResourceScope:     input.GetResourceScope(),
		DurationSeconds:   input.GetDurationSeconds(),
		Reason:            input.GetReason(),
		BreakGlass:        false,
		Metadata:          input.GetMetadata(),
		ApproverTier:      tier,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := e.store.CreateRequest(ctx, req); err != nil {
		return nil, fmt.Errorf("workflow: create request: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: identity.Email,
		Action:        audit.ActionRequestCreated,
		RequestID:     req.ID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
	})

	// Tier 1: policy-driven auto-approve — issue credentials synchronously.
	if tier == "auto" {
		return e.autoApproveRequest(ctx, identity, req, p)
	}

	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventRequestCreated,
		RequestID: req.ID,
		Actor:     identity.Email,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		Reason:    req.Reason,
	})

	return req, nil
}

// createBreakGlass handles the break-glass fast-path: it bypasses OPA eligibility,
// immediately issues credentials, and fires a critical alert notification.
// The full audit trail (PENDING → APPROVED → ACTIVE) is preserved.
func (e *Engine) createBreakGlass(ctx context.Context, identity *auth.Identity, input *jitsudov1alpha1.CreateRequestInput, p providers.Provider) (*store.RequestRow, error) {
	now := time.Now().UTC()
	req := &store.RequestRow{
		ID:                "req_" + ulid.Make().String(),
		State:             store.StatePending,
		RequesterIdentity: identity.Email,
		Provider:          input.GetProvider(),
		Role:              input.GetRole(),
		ResourceScope:     input.GetResourceScope(),
		DurationSeconds:   input.GetDurationSeconds(),
		Reason:            input.GetReason(),
		BreakGlass:        true,
		Metadata:          input.GetMetadata(),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := e.store.CreateRequest(ctx, req); err != nil {
		return nil, fmt.Errorf("workflow: break-glass create request: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: identity.Email,
		Action:        audit.ActionRequestCreated,
		RequestID:     req.ID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
		DetailsJSON:   `{"break_glass":true}`,
	})

	// Fire critical break-glass alert before issuing credentials.
	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventBreakGlass,
		RequestID: req.ID,
		Actor:     identity.Email,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		Reason:    req.Reason,
	})

	// PENDING → APPROVED (auto-approved, no human approver required).
	req, err := e.store.TransitionRequest(ctx, req.ID,
		store.StatePending, store.StateApproved,
		store.RequestUpdate{
			ApproverIdentity: "break-glass",
			ApproverComment:  "Break-glass auto-approved",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("workflow: break-glass approve transition: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: identity.Email,
		Action:        audit.ActionRequestApproved,
		RequestID:     req.ID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
		DetailsJSON:   `{"break_glass":true}`,
	})

	// Issue credentials via provider.
	provReq := providers.ElevationRequest{
		RequestID:     req.ID,
		UserIdentity:  req.RequesterIdentity,
		Provider:      req.Provider,
		RoleName:      req.Role,
		ResourceScope: req.ResourceScope,
		Duration:      time.Duration(req.DurationSeconds) * time.Second,
		Reason:        req.Reason,
	}
	grant, err := p.Grant(ctx, provReq)
	if err != nil {
		return nil, fmt.Errorf("workflow: break-glass provider grant: %w", err)
	}

	// APPROVED → ACTIVE.
	expiresAt := grant.ExpiresAt
	req, err = e.store.TransitionRequest(ctx, req.ID,
		store.StateApproved, store.StateActive,
		store.RequestUpdate{
			ExpiresAt:       &expiresAt,
			RevokeToken:     grant.RevokeToken,
			CredentialsJSON: grant.Credentials,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("workflow: break-glass active transition: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: identity.Email,
		Action:        audit.ActionGrantIssued,
		RequestID:     req.ID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
		DetailsJSON:   `{"break_glass":true}`,
	})

	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventApproved,
		RequestID: req.ID,
		Actor:     identity.Email,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		ExpiresAt: expiresAt,
	})

	return req, nil
}

// autoApproveRequest handles Tier 1 policy-driven auto-approval.
// The request was already written as PENDING; this method transitions it through
// APPROVED → ACTIVE immediately, using "policy" as the approver identity.
// The full audit trail is preserved and an EventAutoApproved notification is fired.
func (e *Engine) autoApproveRequest(ctx context.Context, identity *auth.Identity, req *store.RequestRow, p providers.Provider) (*store.RequestRow, error) {
	// PENDING → APPROVED (policy-driven, no human approver).
	req, err := e.store.TransitionRequest(ctx, req.ID,
		store.StatePending, store.StateApproved,
		store.RequestUpdate{
			ApproverIdentity: "policy",
			ApproverComment:  "Auto-approved by policy (Tier 1)",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("workflow: auto-approve transition: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: identity.Email,
		Action:        audit.ActionRequestAutoApproved,
		RequestID:     req.ID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
	})

	// Issue credentials via provider.
	provReq := providers.ElevationRequest{
		RequestID:     req.ID,
		UserIdentity:  req.RequesterIdentity,
		Provider:      req.Provider,
		RoleName:      req.Role,
		ResourceScope: req.ResourceScope,
		Duration:      time.Duration(req.DurationSeconds) * time.Second,
		Reason:        req.Reason,
	}
	grant, err := p.Grant(ctx, provReq)
	if err != nil {
		return nil, fmt.Errorf("workflow: auto-approve provider grant: %w", err)
	}

	// APPROVED → ACTIVE.
	expiresAt := grant.ExpiresAt
	req, err = e.store.TransitionRequest(ctx, req.ID,
		store.StateApproved, store.StateActive,
		store.RequestUpdate{
			ExpiresAt:       &expiresAt,
			RevokeToken:     grant.RevokeToken,
			CredentialsJSON: grant.Credentials,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("workflow: auto-approve active transition: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: identity.Email,
		Action:        audit.ActionGrantIssued,
		RequestID:     req.ID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
	})

	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventAutoApproved,
		RequestID: req.ID,
		Actor:     identity.Email,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		ExpiresAt: expiresAt,
	})

	return req, nil
}

// AIApproveRequest is called by the MCP approver agent to approve a Tier 2 request.
// It transitions PENDING → APPROVED → ACTIVE, stores the AI reasoning in the audit
// log, and fires an EventAIApproved notification.
func (e *Engine) AIApproveRequest(ctx context.Context, requestID, agentIdentity, reasoning string) (*store.RequestRow, error) {
	req, err := e.store.TransitionRequest(ctx, requestID,
		store.StatePending, store.StateApproved,
		store.RequestUpdate{
			ApproverIdentity: agentIdentity,
			ApproverComment:  "Approved by AI agent (Tier 2)",
			AIReasoningJSON:  reasoning,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("workflow: AI approve transition: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: agentIdentity,
		Action:        audit.ActionRequestAIApproved,
		RequestID:     requestID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
		DetailsJSON:   reasoning,
	})

	p := e.registry.Get(req.Provider)
	if p == nil {
		return nil, fmt.Errorf("workflow: AI approve: unknown provider %q", req.Provider)
	}
	provReq := providers.ElevationRequest{
		RequestID:     req.ID,
		UserIdentity:  req.RequesterIdentity,
		Provider:      req.Provider,
		RoleName:      req.Role,
		ResourceScope: req.ResourceScope,
		Duration:      time.Duration(req.DurationSeconds) * time.Second,
		Reason:        req.Reason,
	}
	grant, err := p.Grant(ctx, provReq)
	if err != nil {
		return nil, fmt.Errorf("workflow: AI approve provider grant: %w", err)
	}

	expiresAt := grant.ExpiresAt
	req, err = e.store.TransitionRequest(ctx, req.ID,
		store.StateApproved, store.StateActive,
		store.RequestUpdate{
			ExpiresAt:       &expiresAt,
			RevokeToken:     grant.RevokeToken,
			CredentialsJSON: grant.Credentials,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("workflow: AI approve active transition: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: agentIdentity,
		Action:        audit.ActionGrantIssued,
		RequestID:     req.ID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
	})

	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventAIApproved,
		RequestID: requestID,
		Actor:     agentIdentity,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		ExpiresAt: expiresAt,
	})

	return req, nil
}

// AIDenyRequest is called by the MCP approver agent to deny a Tier 2 request.
// Transitions PENDING → REJECTED and stores the AI reasoning.
func (e *Engine) AIDenyRequest(ctx context.Context, requestID, agentIdentity, reasoning string) (*store.RequestRow, error) {
	req, err := e.store.TransitionRequest(ctx, requestID,
		store.StatePending, store.StateRejected,
		store.RequestUpdate{
			ApproverIdentity: agentIdentity,
			ApproverComment:  "Denied by AI agent (Tier 2)",
			AIReasoningJSON:  reasoning,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("workflow: AI deny transition: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: agentIdentity,
		Action:        audit.ActionRequestAIDenied,
		RequestID:     requestID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
		DetailsJSON:   reasoning,
	})

	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventAIDenied,
		RequestID: requestID,
		Actor:     agentIdentity,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		Reason:    reasoning,
	})

	return req, nil
}

// AIEscalateRequest is called by the MCP approver agent to escalate a Tier 2
// request to human review. Flips approver_tier to "human" so it enters the
// normal human approval queue. Does not change state (stays PENDING).
func (e *Engine) AIEscalateRequest(ctx context.Context, requestID, agentIdentity, reasoning string) (*store.RequestRow, error) {
	// Use a raw store update to flip the tier without a state change.
	if err := e.store.SetApproverTier(ctx, requestID, "human"); err != nil {
		return nil, fmt.Errorf("workflow: AI escalate set tier: %w", err)
	}

	req, err := e.store.GetRequest(ctx, requestID)
	if err != nil {
		return nil, fmt.Errorf("workflow: AI escalate get request: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: agentIdentity,
		Action:        audit.ActionRequestAIEscalated,
		RequestID:     requestID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
		DetailsJSON:   reasoning,
	})

	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventAIEscalated,
		RequestID: requestID,
		Actor:     agentIdentity,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		Reason:    reasoning,
	})

	return req, nil
}

// ApproveRequest transitions PENDING → APPROVED → ACTIVE and issues credentials.
func (e *Engine) ApproveRequest(ctx context.Context, approver *auth.Identity, requestID, comment string) (*store.RequestRow, error) {
	// Lock row and move PENDING → APPROVED.
	req, err := e.store.TransitionRequest(ctx, requestID,
		store.StatePending, store.StateApproved,
		store.RequestUpdate{
			ApproverIdentity: approver.Email,
			ApproverComment:  comment,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("workflow: approve transition: %w", err)
	}

	// Policy approval check (confirms the approver is authorised).
	allowed, reason, err := e.policy.EvalApproval(ctx, approver, req)
	if err != nil {
		// Roll back to PENDING on policy eval error.
		if _, rbErr := e.store.TransitionRequest(ctx, requestID, store.StateApproved, store.StatePending, store.RequestUpdate{}); rbErr != nil {
			log.Error().Err(rbErr).Str("request_id", requestID).Msg("workflow: rollback to PENDING failed after policy eval error")
		}
		return nil, fmt.Errorf("workflow: approval eval: %w", err)
	}
	if !allowed {
		if reason == "" {
			reason = "approval policy denied"
		}
		if _, rbErr := e.store.TransitionRequest(ctx, requestID, store.StateApproved, store.StatePending, store.RequestUpdate{}); rbErr != nil {
			log.Error().Err(rbErr).Str("request_id", requestID).Msg("workflow: rollback to PENDING failed after policy denial")
		}
		return nil, fmt.Errorf("workflow: approval denied: %s", reason)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: approver.Email,
		Action:        audit.ActionRequestApproved,
		RequestID:     requestID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
	})

	// Call the provider to issue credentials.
	provReq := providers.ElevationRequest{
		RequestID:     req.ID,
		UserIdentity:  req.RequesterIdentity,
		Provider:      req.Provider,
		RoleName:      req.Role,
		ResourceScope: req.ResourceScope,
		Duration:      time.Duration(req.DurationSeconds) * time.Second,
		Reason:        req.Reason,
	}
	p := e.registry.Get(req.Provider)
	if p == nil {
		return nil, fmt.Errorf("workflow: provider %q not registered", req.Provider)
	}
	grant, err := p.Grant(ctx, provReq)
	if err != nil {
		return nil, fmt.Errorf("workflow: provider grant: %w", err)
	}

	expiresAt := grant.ExpiresAt
	req, err = e.store.TransitionRequest(ctx, requestID,
		store.StateApproved, store.StateActive,
		store.RequestUpdate{
			ExpiresAt:       &expiresAt,
			RevokeToken:     grant.RevokeToken,
			CredentialsJSON: grant.Credentials,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("workflow: active transition: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: approver.Email,
		Action:        audit.ActionGrantIssued,
		RequestID:     requestID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
	})

	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventApproved,
		RequestID: requestID,
		Actor:     approver.Email,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		ExpiresAt: expiresAt,
	})

	return req, nil
}

// DenyRequest transitions PENDING → REJECTED.
func (e *Engine) DenyRequest(ctx context.Context, denier *auth.Identity, requestID, reason string) (*store.RequestRow, error) {
	req, err := e.store.TransitionRequest(ctx, requestID,
		store.StatePending, store.StateRejected,
		store.RequestUpdate{
			ApproverIdentity: denier.Email,
			ApproverComment:  reason,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("workflow: deny transition: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: denier.Email,
		Action:        audit.ActionRequestDenied,
		RequestID:     requestID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
	})

	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventDenied,
		RequestID: requestID,
		Actor:     denier.Email,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		Reason:    reason,
	})

	return req, nil
}

// RevokeRequest transitions ACTIVE → REVOKED and calls provider.Revoke().
// The original requester may always revoke their own request. Anyone in the
// "jitsudo-admins" group may revoke any request. (Policy-driven enforcement
// is planned for Milestone 3.)
func (e *Engine) RevokeRequest(ctx context.Context, actor *auth.Identity, requestID, reason string) (*store.RequestRow, error) {
	req, err := e.store.GetRequest(ctx, requestID)
	if err != nil {
		return nil, fmt.Errorf("workflow: revoke: %w", err)
	}

	// Authorization: requester can always self-revoke; admins can revoke any.
	isRequester := req.RequesterIdentity == actor.Email
	isAdmin := slices.Contains(actor.Groups, "jitsudo-admins")
	if !isRequester && !isAdmin {
		return nil, fmt.Errorf("workflow: revoke: access denied")
	}

	// If already in a terminal state, return current row without error.
	if req.State == store.StateRevoked || req.State == store.StateExpired || req.State == store.StateRejected {
		return req, nil
	}

	req, err = e.store.TransitionRequest(ctx, requestID,
		store.StateActive, store.StateRevoked,
		store.RequestUpdate{ApproverComment: reason},
	)
	if err != nil {
		if errors.Is(err, store.ErrWrongState) {
			// Race: state changed between Get and Transition — re-fetch and return.
			return e.store.GetRequest(ctx, requestID)
		}
		return nil, fmt.Errorf("workflow: revoke transition: %w", err)
	}

	// Best-effort provider revocation — log on failure but don't undo the transition.
	p := e.registry.Get(req.Provider)
	if p != nil {
		var expiresAt time.Time
		if req.ExpiresAt != nil {
			expiresAt = *req.ExpiresAt
		}
		grant := providers.ElevationGrant{
			RequestID:   req.ID,
			RevokeToken: req.RevokeToken,
			ExpiresAt:   expiresAt,
		}
		if err := p.Revoke(ctx, grant); err != nil {
			log.Warn().Err(err).Str("request_id", req.ID).Msg("workflow: provider revoke failed (transition already committed)")
		}
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: actor.Email,
		Action:        audit.ActionGrantRevoked,
		RequestID:     requestID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
	})

	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventRevoked,
		RequestID: requestID,
		Actor:     actor.Email,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		Reason:    reason,
	})

	return req, nil
}

// GetCredentials returns the active credentials for an ACTIVE request.
// Only the original requester may retrieve credentials.
func (e *Engine) GetCredentials(ctx context.Context, requester *auth.Identity, requestID string) (map[string]string, error) {
	req, err := e.store.GetRequest(ctx, requestID)
	if err != nil {
		return nil, fmt.Errorf("workflow: get credentials: %w", err)
	}
	if req.RequesterIdentity != requester.Email {
		return nil, fmt.Errorf("workflow: access denied")
	}
	if req.State != store.StateActive {
		return nil, fmt.Errorf("workflow: request is not active (state: %s)", req.State)
	}
	if req.ExpiresAt != nil && time.Now().UTC().After(*req.ExpiresAt) {
		return nil, fmt.Errorf("workflow: grant has expired")
	}
	return req.CredentialsJSON, nil
}

// RunExpirySweeper runs in a goroutine and expires ACTIVE grants past their deadline.
// It exits when ctx is cancelled.
func (e *Engine) RunExpirySweeper(ctx context.Context, interval time.Duration) {
	log.Info().Dur("interval", interval).Msg("expiry sweeper started")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.sweepExpired(ctx)
		}
	}
}

func (e *Engine) sweepExpired(ctx context.Context) {
	// Acquire a non-blocking advisory lock so only one jitsudod instance runs the
	// sweep at a time. provider.Revoke is called before the DB state transition, so
	// without this lock multiple instances would issue duplicate Revoke calls for the
	// same grant in HA deployments.
	acquired, release, err := e.store.TryAcquireSweepLock(ctx)
	if err != nil {
		log.Error().Err(err).Msg("expiry sweeper: advisory lock error")
		return
	}
	if !acquired {
		return // another instance is already sweeping
	}
	defer release()

	expired, err := e.store.ListActiveExpired(ctx)
	if err != nil {
		log.Error().Err(err).Msg("expiry sweeper: list failed")
		return
	}

	for _, req := range expired {
		p := e.registry.Get(req.Provider)
		if p != nil {
			var expiresAt time.Time
			if req.ExpiresAt != nil {
				expiresAt = *req.ExpiresAt
			}
			grant := providers.ElevationGrant{
				RequestID:   req.ID,
				RevokeToken: req.RevokeToken,
				ExpiresAt:   expiresAt,
			}
			if err := p.Revoke(ctx, grant); err != nil {
				log.Warn().Err(err).Str("request_id", req.ID).Msg("expiry sweeper: revoke failed")
			}
		}

		if _, err := e.store.TransitionRequest(ctx, req.ID, store.StateActive, store.StateExpired, store.RequestUpdate{}); err != nil {
			log.Error().Err(err).Str("request_id", req.ID).Msg("expiry sweeper: transition failed")
			continue
		}

		e.appendAudit(ctx, audit.Entry{
			ActorIdentity: "system",
			Action:        audit.ActionGrantExpired,
			RequestID:     req.ID,
			Provider:      req.Provider,
			ResourceScope: req.ResourceScope,
			Outcome:       audit.OutcomeSuccess,
		})

		e.notifications.Notify(ctx, notifications.Event{
			Type:      notifications.EventExpired,
			RequestID: req.ID,
			Actor:     "system",
			Provider:  req.Provider,
			Role:      req.Role,
			Scope:     req.ResourceScope,
		})

		log.Info().Str("request_id", req.ID).Msg("grant expired")
	}
}

// MCPRequestInput carries the parameters from the MCP agent's request_access tool call.
// Action maps to the role column (e.g. "s3:GetObject"); Resource maps to resource_scope
// (e.g. "arn:aws:s3:::my-bucket/*"). Additional MCP-specific fields are stored in Metadata.
type MCPRequestInput struct {
	Action          string
	Resource        string
	DurationSeconds int64
	Reason          string
	TicketRef       string
}

func inferProviderFromResource(_ string) string {
	return "mock" // TODO: route by ARN prefix
}

// CreateMCPRequest creates a PENDING elevation request from the MCP agent requestor surface.
// It maps action→role and resource→resource_scope using the unified request model, stores
// additional MCP fields in the metadata JSONB column, and sets pending_timeout_at /
// pending_timeout_action from policy evaluation.
func (e *Engine) CreateMCPRequest(ctx context.Context, identity *auth.Identity, input MCPRequestInput) (*store.RequestRow, error) {
	provider := inferProviderFromResource(input.Resource)

	p := e.registry.Get(provider)
	if p == nil {
		return nil, fmt.Errorf("workflow: unknown provider %q (inferred from resource)", provider)
	}

	_ = e.store.UpsertPrincipalLastSeen(ctx, identity.Email)

	// Build a wrapper so we can call the existing policy evaluation methods.
	policyInput := &jitsudov1alpha1.CreateRequestInput{
		Provider:        provider,
		Role:            input.Action,
		ResourceScope:   input.Resource,
		DurationSeconds: input.DurationSeconds,
	}

	allowed, reason, err := e.policy.EvalEligibility(ctx, identity, policyInput)
	if err != nil {
		return nil, fmt.Errorf("workflow: mcp eligibility eval: %w", err)
	}
	if !allowed {
		if reason == "" {
			reason = "policy denied the request"
		}
		return nil, fmt.Errorf("workflow: not eligible: %s", reason)
	}

	tier, err := e.policy.EvalApprovalTier(ctx, identity, policyInput)
	if err != nil {
		return nil, fmt.Errorf("workflow: mcp approval tier eval: %w", err)
	}

	timeoutSecs, err := e.policy.EvalTimeoutSeconds(ctx, identity, provider, input.Action, input.Resource, input.DurationSeconds)
	if err != nil {
		return nil, fmt.Errorf("workflow: mcp timeout_seconds eval: %w", err)
	}
	timeoutAction, err := e.policy.EvalTimeoutAction(ctx, identity, provider, input.Action, input.Resource, input.DurationSeconds)
	if err != nil {
		return nil, fmt.Errorf("workflow: mcp timeout_action eval: %w", err)
	}

	now := time.Now().UTC()
	meta := map[string]string{"action": input.Action}
	if input.TicketRef != "" {
		meta["ticket_ref"] = input.TicketRef
	}

	req := &store.RequestRow{
		ID:                "req_" + ulid.Make().String(),
		State:             store.StatePending,
		RequesterIdentity: identity.Email,
		Provider:          provider,
		Role:              input.Action,
		ResourceScope:     input.Resource,
		DurationSeconds:   input.DurationSeconds,
		Reason:            input.Reason,
		BreakGlass:        false,
		Metadata:          meta,
		ApproverTier:      tier,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if timeoutSecs > 0 {
		t := now.Add(time.Duration(timeoutSecs) * time.Second)
		req.PendingTimeoutAt = &t
		req.PendingTimeoutAction = timeoutAction
	}

	if err := e.store.CreateRequest(ctx, req); err != nil {
		return nil, fmt.Errorf("workflow: mcp create request: %w", err)
	}

	e.appendAudit(ctx, audit.Entry{
		ActorIdentity: identity.Email,
		Action:        audit.ActionRequestCreated,
		RequestID:     req.ID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
		DetailsJSON:   `{"requestor_surface":"mcp","principal":{"type":"agent"}}`,
	})

	if tier == "auto" {
		return e.autoApproveRequest(ctx, identity, req, p)
	}

	e.notifications.Notify(ctx, notifications.Event{
		Type:      notifications.EventRequestCreated,
		RequestID: req.ID,
		Actor:     identity.Email,
		Provider:  req.Provider,
		Role:      req.Role,
		Scope:     req.ResourceScope,
		Reason:    req.Reason,
	})

	return req, nil
}

// RunPendingTimeoutSweeper runs in a goroutine and handles PENDING requests whose
// pending_timeout_at deadline has passed. Mirrors RunExpirySweeper.
// It exits when ctx is cancelled.
func (e *Engine) RunPendingTimeoutSweeper(ctx context.Context, interval time.Duration) {
	log.Info().Dur("interval", interval).Msg("pending timeout sweeper started")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.sweepPendingTimedOut(ctx)
		}
	}
}

func (e *Engine) sweepPendingTimedOut(ctx context.Context) {
	acquired, release, err := e.store.TryAcquirePendingTimeoutLock(ctx)
	if err != nil {
		log.Error().Err(err).Msg("pending timeout sweeper: advisory lock error")
		return
	}
	if !acquired {
		return
	}
	defer release()

	timedOut, err := e.store.ListPendingTimedOut(ctx)
	if err != nil {
		log.Error().Err(err).Msg("pending timeout sweeper: list failed")
		return
	}

	for _, req := range timedOut {
		switch req.PendingTimeoutAction {
		case "auto_approve":
			p := e.registry.Get(req.Provider)
			if p == nil {
				log.Error().Str("request_id", req.ID).Str("provider", req.Provider).
					Msg("pending timeout sweeper: provider not found for auto_approve")
				continue
			}
			syntheticIdentity := &auth.Identity{Email: req.RequesterIdentity}
			if _, err := e.autoApproveRequest(ctx, syntheticIdentity, req, p); err != nil {
				log.Error().Err(err).Str("request_id", req.ID).Msg("pending timeout sweeper: auto_approve failed")
			} else {
				log.Info().Str("request_id", req.ID).Msg("pending timeout: auto-approved")
			}

		case "escalate":
			e.appendAudit(ctx, audit.Entry{
				ActorIdentity: "system",
				Action:        audit.ActionRequestAIEscalated,
				RequestID:     req.ID,
				Provider:      req.Provider,
				ResourceScope: req.ResourceScope,
				Outcome:       audit.OutcomeSuccess,
				DetailsJSON:   `{"reason":"pending_timeout_escalate"}`,
			})
			if err := e.store.SetApproverTier(ctx, req.ID, "human"); err != nil {
				log.Error().Err(err).Str("request_id", req.ID).Msg("pending timeout sweeper: escalate set tier failed")
				continue
			}
			e.notifications.Notify(ctx, notifications.Event{
				Type:      notifications.EventAIEscalated,
				RequestID: req.ID,
				Actor:     "system",
				Provider:  req.Provider,
				Role:      req.Role,
				Scope:     req.ResourceScope,
				Reason:    "pending_timeout_escalate",
			})
			log.Info().Str("request_id", req.ID).Msg("pending timeout: escalated to human")

		default: // "deny" or any unrecognised value — safe default is deny
			e.appendAudit(ctx, audit.Entry{
				ActorIdentity: "system",
				Action:        audit.ActionRequestDenied,
				RequestID:     req.ID,
				Provider:      req.Provider,
				ResourceScope: req.ResourceScope,
				Outcome:       audit.OutcomeSuccess,
				DetailsJSON:   `{"reason":"pending_timeout_deny"}`,
			})
			if _, err := e.store.TransitionRequest(ctx, req.ID,
				store.StatePending, store.StateRejected,
				store.RequestUpdate{ApproverComment: "Denied by pending timeout policy"},
			); err != nil {
				log.Error().Err(err).Str("request_id", req.ID).Msg("pending timeout sweeper: deny transition failed")
				continue
			}
			e.notifications.Notify(ctx, notifications.Event{
				Type:      notifications.EventDenied,
				RequestID: req.ID,
				Actor:     "system",
				Provider:  req.Provider,
				Role:      req.Role,
				Scope:     req.ResourceScope,
				Reason:    "pending timeout expired",
			})
			log.Info().Str("request_id", req.ID).Msg("pending timeout: denied")
		}
	}
}
