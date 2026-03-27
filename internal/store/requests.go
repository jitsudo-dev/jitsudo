// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RequestState mirrors the request_state DB enum.
type RequestState string

const (
	StatePending  RequestState = "PENDING"
	StateApproved RequestState = "APPROVED"
	StateRejected RequestState = "REJECTED"
	StateActive   RequestState = "ACTIVE"
	StateExpired  RequestState = "EXPIRED"
	StateRevoked  RequestState = "REVOKED"
)

// RequestRow is a row from the elevation_requests table.
type RequestRow struct {
	ID                string
	State             RequestState
	RequesterIdentity string
	Provider          string
	Role              string
	ResourceScope     string
	DurationSeconds   int64
	Reason            string
	BreakGlass        bool
	Metadata          map[string]string
	ApproverTier      string // "auto" | "ai_review" | "human"
	ApproverIdentity  string
	ApproverComment   string
	ExpiresAt         *time.Time
	RevokeToken       string
	CredentialsJSON   map[string]string // nil until ACTIVE
	AIReasoningJSON   string            // populated by MCP approver for Tier 2 decisions
	// PendingTimeoutAt and PendingTimeoutAction support policy-configured timeouts
	// for pending requests. These are general lifecycle fields — not MCP-specific.
	// NULL means no timeout applies (request waits indefinitely for approval).
	PendingTimeoutAt     *time.Time
	PendingTimeoutAction string // "deny" | "auto_approve" | "escalate" | ""
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// RequestUpdate holds the fields that can be set during a state transition.
type RequestUpdate struct {
	ApproverIdentity string
	ApproverComment  string
	ExpiresAt        *time.Time
	RevokeToken      string
	CredentialsJSON  map[string]string
	AIReasoningJSON  string
}

// ListFilter selects which requests to return.
type ListFilter struct {
	RequesterIdentity string
	State             RequestState
	Provider          string
}

// CreateRequest inserts a new PENDING request.
func (s *Store) CreateRequest(ctx context.Context, req *RequestRow) error {
	metaJSON, _ := json.Marshal(req.Metadata)
	tier := req.ApproverTier
	if tier == "" {
		tier = "human"
	}
	var timeoutAction *string
	if req.PendingTimeoutAction != "" {
		timeoutAction = &req.PendingTimeoutAction
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO elevation_requests
			(id, state, requester_identity, provider, role, resource_scope,
			 duration_seconds, reason, break_glass, metadata, approver_tier,
			 pending_timeout_at, pending_timeout_action,
			 created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		req.ID, string(req.State), req.RequesterIdentity, req.Provider,
		req.Role, req.ResourceScope, req.DurationSeconds, req.Reason,
		req.BreakGlass, metaJSON, tier,
		req.PendingTimeoutAt, timeoutAction,
		req.CreatedAt, req.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: CreateRequest: %w", err)
	}
	return nil
}

// GetRequest fetches a single request by ID.
func (s *Store) GetRequest(ctx context.Context, id string) (*RequestRow, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, state, requester_identity, provider, role, resource_scope,
		       duration_seconds, reason, break_glass, metadata,
		       COALESCE(approver_tier,'human'),
		       COALESCE(approver_identity,''), COALESCE(approver_comment,''),
		       expires_at, COALESCE(revoke_token,''), credentials_json,
		       COALESCE(ai_reasoning_json,''),
		       pending_timeout_at, COALESCE(pending_timeout_action,''),
		       created_at, updated_at
		FROM elevation_requests WHERE id = $1`, id)
	return scanRequest(row)
}

// ListRequests returns requests matching the filter.
func (s *Store) ListRequests(ctx context.Context, f ListFilter) ([]*RequestRow, error) {
	query := `
		SELECT id, state, requester_identity, provider, role, resource_scope,
		       duration_seconds, reason, break_glass, metadata,
		       COALESCE(approver_tier,'human'),
		       COALESCE(approver_identity,''), COALESCE(approver_comment,''),
		       expires_at, COALESCE(revoke_token,''), credentials_json,
		       COALESCE(ai_reasoning_json,''),
		       pending_timeout_at, COALESCE(pending_timeout_action,''),
		       created_at, updated_at
		FROM elevation_requests WHERE 1=1`
	args := []any{}
	n := 1
	if f.RequesterIdentity != "" {
		query += fmt.Sprintf(" AND requester_identity = $%d", n)
		args = append(args, f.RequesterIdentity)
		n++
	}
	if f.State != "" {
		query += fmt.Sprintf(" AND state = $%d", n)
		args = append(args, string(f.State))
		n++
	}
	if f.Provider != "" {
		query += fmt.Sprintf(" AND provider = $%d", n)
		args = append(args, f.Provider)
	}
	query += " ORDER BY created_at DESC LIMIT 200"

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: ListRequests: %w", err)
	}
	defer rows.Close()

	var out []*RequestRow
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetApproverTier updates the approver_tier column without changing state.
// Used by the MCP escalate tool to flip a Tier 2 request to human review.
func (s *Store) SetApproverTier(ctx context.Context, id string, tier string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE elevation_requests SET approver_tier = $2, updated_at = NOW()
		WHERE id = $1 AND state = 'PENDING'`, id, tier)
	if err != nil {
		return fmt.Errorf("store: SetApproverTier: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPendingAIReview returns PENDING requests with approver_tier = 'ai_review'.
// Used by the MCP approver interface to surface work for the AI agent.
func (s *Store) ListPendingAIReview(ctx context.Context) ([]*RequestRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, state, requester_identity, provider, role, resource_scope,
		       duration_seconds, reason, break_glass, metadata,
		       COALESCE(approver_tier,'human'),
		       COALESCE(approver_identity,''), COALESCE(approver_comment,''),
		       expires_at, COALESCE(revoke_token,''), credentials_json,
		       COALESCE(ai_reasoning_json,''),
		       pending_timeout_at, COALESCE(pending_timeout_action,''),
		       created_at, updated_at
		FROM elevation_requests
		WHERE state = 'PENDING' AND approver_tier = 'ai_review'
		ORDER BY created_at ASC LIMIT 100`)
	if err != nil {
		return nil, fmt.Errorf("store: ListPendingAIReview: %w", err)
	}
	defer rows.Close()

	var out []*RequestRow
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListActiveExpired returns ACTIVE requests whose expires_at is in the past.
// Used by the expiry sweeper.
func (s *Store) ListActiveExpired(ctx context.Context) ([]*RequestRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, state, requester_identity, provider, role, resource_scope,
		       duration_seconds, reason, break_glass, metadata,
		       COALESCE(approver_tier,'human'),
		       COALESCE(approver_identity,''), COALESCE(approver_comment,''),
		       expires_at, COALESCE(revoke_token,''), credentials_json,
		       COALESCE(ai_reasoning_json,''),
		       pending_timeout_at, COALESCE(pending_timeout_action,''),
		       created_at, updated_at
		FROM elevation_requests
		WHERE state = 'ACTIVE' AND expires_at <= NOW()`)
	if err != nil {
		return nil, fmt.Errorf("store: ListActiveExpired: %w", err)
	}
	defer rows.Close()

	var out []*RequestRow
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TransitionRequest atomically moves a request from one state to another.
// It uses SELECT FOR UPDATE to prevent concurrent transitions.
// Returns ErrNotFound if the request doesn't exist.
// Returns ErrWrongState if the current state is not fromState.
func (s *Store) TransitionRequest(ctx context.Context, id string, fromState, toState RequestState, u RequestUpdate) (*RequestRow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: TransitionRequest begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the row.
	var currentState RequestState
	err = tx.QueryRow(ctx,
		`SELECT state FROM elevation_requests WHERE id = $1 FOR UPDATE`, id,
	).Scan(&currentState)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: TransitionRequest lock: %w", err)
	}
	if currentState != fromState {
		return nil, fmt.Errorf("%w: expected %s, got %s", ErrWrongState, fromState, currentState)
	}

	// Build the UPDATE.
	now := time.Now().UTC()
	var credJSON []byte
	if u.CredentialsJSON != nil {
		var merr error
		credJSON, merr = json.Marshal(u.CredentialsJSON)
		if merr != nil {
			return nil, fmt.Errorf("store: marshal credentials: %w", merr)
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE elevation_requests SET
			state               = $2,
			approver_identity   = COALESCE(NULLIF($3,''), approver_identity),
			approver_comment    = COALESCE(NULLIF($4,''), approver_comment),
			expires_at          = COALESCE($5, expires_at),
			revoke_token        = COALESCE(NULLIF($6,''), revoke_token),
			credentials_json    = COALESCE($7::jsonb, credentials_json),
			ai_reasoning_json   = COALESCE(NULLIF($9,''), ai_reasoning_json),
			updated_at          = $8
		WHERE id = $1`,
		id, string(toState),
		u.ApproverIdentity, u.ApproverComment,
		u.ExpiresAt, u.RevokeToken,
		nullableJSON(credJSON), now,
		u.AIReasoningJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("store: TransitionRequest update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: TransitionRequest commit: %w", err)
	}

	return s.GetRequest(ctx, id)
}

// ListActiveGrantsByIdentity returns all ACTIVE grants for a given requester identity
// whose expiry is in the future (or have no expiry set).
func (s *Store) ListActiveGrantsByIdentity(ctx context.Context, identity string) ([]*RequestRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, state, requester_identity, provider, role, resource_scope,
		       duration_seconds, reason, break_glass, metadata,
		       COALESCE(approver_tier,'human'),
		       COALESCE(approver_identity,''), COALESCE(approver_comment,''),
		       expires_at, COALESCE(revoke_token,''), credentials_json,
		       COALESCE(ai_reasoning_json,''),
		       pending_timeout_at, COALESCE(pending_timeout_action,''),
		       created_at, updated_at
		FROM elevation_requests
		WHERE state = 'ACTIVE'
		  AND requester_identity = $1
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC LIMIT 50`, identity)
	if err != nil {
		return nil, fmt.Errorf("store: ListActiveGrantsByIdentity: %w", err)
	}
	defer rows.Close()

	var out []*RequestRow
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPendingTimedOut returns PENDING requests whose pending_timeout_at has passed.
func (s *Store) ListPendingTimedOut(ctx context.Context) ([]*RequestRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, state, requester_identity, provider, role, resource_scope,
		       duration_seconds, reason, break_glass, metadata,
		       COALESCE(approver_tier,'human'),
		       COALESCE(approver_identity,''), COALESCE(approver_comment,''),
		       expires_at, COALESCE(revoke_token,''), credentials_json,
		       COALESCE(ai_reasoning_json,''),
		       pending_timeout_at, COALESCE(pending_timeout_action,''),
		       created_at, updated_at
		FROM elevation_requests
		WHERE state = 'PENDING'
		  AND pending_timeout_at IS NOT NULL
		  AND pending_timeout_at <= NOW()
		ORDER BY pending_timeout_at ASC LIMIT 100`)
	if err != nil {
		return nil, fmt.Errorf("store: ListPendingTimedOut: %w", err)
	}
	defer rows.Close()

	var out []*RequestRow
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetPendingTimeout sets the timeout deadline and action for a PENDING request.
func (s *Store) SetPendingTimeout(ctx context.Context, id string, timeoutAt time.Time, action string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE elevation_requests
		SET pending_timeout_at = $2, pending_timeout_action = $3, updated_at = NOW()
		WHERE id = $1 AND state = 'PENDING'`, id, timeoutAt, action)
	if err != nil {
		return fmt.Errorf("store: SetPendingTimeout: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// sweepLockKey is the PostgreSQL advisory lock key reserved for the expiry sweeper.
// The value is fixed and must be identical across all jitsudod instances.
const sweepLockKey = int64(7278657000) // "jitsudo\0" packed, avoids collision with ad-hoc keys

// pendingTimeoutLockKey is the advisory lock key for the pending timeout sweeper.
// Distinct from sweepLockKey so the two sweepers do not block each other.
const pendingTimeoutLockKey = int64(7278657001)

// TryAcquireSweepLock attempts a non-blocking session-level PostgreSQL advisory lock.
// Only one jitsudod instance holds this lock at a time, ensuring the expiry sweeper
// (which calls provider.Revoke before the DB state transition) runs on exactly one
// instance — preventing duplicate provider calls in multi-instance deployments.
//
// Returns (true, release) when the lock is acquired; the caller MUST call release()
// when the sweep finishes. Returns (false, no-op) when another instance already holds
// the lock. Returns an error only on unexpected database failures.
func (s *Store) TryAcquireSweepLock(ctx context.Context) (bool, func(), error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return false, func() {}, fmt.Errorf("store: sweep lock acquire conn: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, sweepLockKey).Scan(&acquired); err != nil {
		conn.Release()
		return false, func() {}, fmt.Errorf("store: pg_try_advisory_lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return false, func() {}, nil
	}
	release := func() {
		_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, sweepLockKey)
		conn.Release()
	}
	return true, release, nil
}

// TryAcquirePendingTimeoutLock is the same pattern as TryAcquireSweepLock but
// uses pendingTimeoutLockKey so the pending timeout sweeper and the expiry
// sweeper can run concurrently on different instances without blocking each other.
func (s *Store) TryAcquirePendingTimeoutLock(ctx context.Context) (bool, func(), error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return false, func() {}, fmt.Errorf("store: pending timeout lock acquire conn: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, pendingTimeoutLockKey).Scan(&acquired); err != nil {
		conn.Release()
		return false, func() {}, fmt.Errorf("store: pg_try_advisory_lock (pending timeout): %w", err)
	}
	if !acquired {
		conn.Release()
		return false, func() {}, nil
	}
	release := func() {
		_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, pendingTimeoutLockKey)
		conn.Release()
	}
	return true, release, nil
}

// nullableJSON returns nil if the input is empty, otherwise the raw bytes.
func nullableJSON(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	return b
}

// scanRequest scans a single row from any query returning the standard request columns.
func scanRequest(row interface {
	Scan(...any) error
}) (*RequestRow, error) {
	r := &RequestRow{}
	var metaJSON []byte
	var credJSON []byte
	err := row.Scan(
		&r.ID, &r.State, &r.RequesterIdentity, &r.Provider,
		&r.Role, &r.ResourceScope, &r.DurationSeconds, &r.Reason,
		&r.BreakGlass, &metaJSON,
		&r.ApproverTier,
		&r.ApproverIdentity, &r.ApproverComment,
		&r.ExpiresAt, &r.RevokeToken, &credJSON,
		&r.AIReasoningJSON,
		&r.PendingTimeoutAt, &r.PendingTimeoutAction,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan request: %w", err)
	}
	if len(metaJSON) > 0 {
		if err := json.Unmarshal(metaJSON, &r.Metadata); err != nil {
			return nil, fmt.Errorf("store: unmarshal request metadata: %w", err)
		}
	}
	if len(credJSON) > 0 {
		if err := json.Unmarshal(credJSON, &r.CredentialsJSON); err != nil {
			return nil, fmt.Errorf("store: unmarshal request credentials: %w", err)
		}
	}
	return r, nil
}
