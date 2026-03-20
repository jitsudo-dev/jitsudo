// Package workflow implements the elevation request state machine.
//
// States: PENDING -> APPROVED | REJECTED -> ACTIVE -> EXPIRED | REVOKED
//
// Every state transition writes an immutable audit log entry before updating
// the request state (write-ahead audit log pattern). Transitions use
// database row-level locking to prevent races in HA deployments.
//
// License: Elastic License 2.0 (ELv2)
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

// Engine drives elevation request lifecycle transitions.
type Engine struct {
	store         *store.Store
	audit         *audit.Logger
	policy        *policy.Engine
	registry      *providers.Registry
	notifications *notifications.Dispatcher
}

// NewEngine wires together the store, audit logger, policy engine, provider
// registry, and optional notification dispatcher.
// Passing nil for dispatcher disables notifications.
func NewEngine(s *store.Store, a *audit.Logger, p *policy.Engine, r *providers.Registry, n *notifications.Dispatcher) *Engine {
	return &Engine{store: s, audit: a, policy: p, registry: r, notifications: n}
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
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := e.store.CreateRequest(ctx, req); err != nil {
		return nil, fmt.Errorf("workflow: create request: %w", err)
	}

	_ = e.audit.Append(ctx, audit.Entry{
		ActorIdentity: identity.Email,
		Action:        audit.ActionRequestCreated,
		RequestID:     req.ID,
		Provider:      req.Provider,
		ResourceScope: req.ResourceScope,
		Outcome:       audit.OutcomeSuccess,
	})

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

	_ = e.audit.Append(ctx, audit.Entry{
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

	_ = e.audit.Append(ctx, audit.Entry{
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

	_ = e.audit.Append(ctx, audit.Entry{
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
		_, _ = e.store.TransitionRequest(ctx, requestID, store.StateApproved, store.StatePending, store.RequestUpdate{})
		return nil, fmt.Errorf("workflow: approval eval: %w", err)
	}
	if !allowed {
		if reason == "" {
			reason = "approval policy denied"
		}
		_, _ = e.store.TransitionRequest(ctx, requestID, store.StateApproved, store.StatePending, store.RequestUpdate{})
		return nil, fmt.Errorf("workflow: approval denied: %s", reason)
	}

	_ = e.audit.Append(ctx, audit.Entry{
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

	_ = e.audit.Append(ctx, audit.Entry{
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

	_ = e.audit.Append(ctx, audit.Entry{
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

	_ = e.audit.Append(ctx, audit.Entry{
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

		_ = e.audit.Append(ctx, audit.Entry{
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
