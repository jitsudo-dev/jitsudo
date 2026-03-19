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
	ApproverIdentity  string
	ApproverComment   string
	ExpiresAt         *time.Time
	RevokeToken       string
	CredentialsJSON   map[string]string // nil until ACTIVE
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// RequestUpdate holds the fields that can be set during a state transition.
type RequestUpdate struct {
	ApproverIdentity string
	ApproverComment  string
	ExpiresAt        *time.Time
	RevokeToken      string
	CredentialsJSON  map[string]string
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
	_, err := s.db.Exec(ctx, `
		INSERT INTO elevation_requests
			(id, state, requester_identity, provider, role, resource_scope,
			 duration_seconds, reason, break_glass, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		req.ID, string(req.State), req.RequesterIdentity, req.Provider,
		req.Role, req.ResourceScope, req.DurationSeconds, req.Reason,
		req.BreakGlass, metaJSON, req.CreatedAt, req.UpdatedAt,
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
		       COALESCE(approver_identity,''), COALESCE(approver_comment,''),
		       expires_at, COALESCE(revoke_token,''), credentials_json,
		       created_at, updated_at
		FROM elevation_requests WHERE id = $1`, id)
	return scanRequest(row)
}

// ListRequests returns requests matching the filter.
func (s *Store) ListRequests(ctx context.Context, f ListFilter) ([]*RequestRow, error) {
	query := `
		SELECT id, state, requester_identity, provider, role, resource_scope,
		       duration_seconds, reason, break_glass, metadata,
		       COALESCE(approver_identity,''), COALESCE(approver_comment,''),
		       expires_at, COALESCE(revoke_token,''), credentials_json,
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
		n++
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

// ListActiveExpired returns ACTIVE requests whose expires_at is in the past.
// Used by the expiry sweeper.
func (s *Store) ListActiveExpired(ctx context.Context) ([]*RequestRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, state, requester_identity, provider, role, resource_scope,
		       duration_seconds, reason, break_glass, metadata,
		       COALESCE(approver_identity,''), COALESCE(approver_comment,''),
		       expires_at, COALESCE(revoke_token,''), credentials_json,
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
		credJSON, _ = json.Marshal(u.CredentialsJSON)
	}
	_, err = tx.Exec(ctx, `
		UPDATE elevation_requests SET
			state             = $2,
			approver_identity = COALESCE(NULLIF($3,''), approver_identity),
			approver_comment  = COALESCE(NULLIF($4,''), approver_comment),
			expires_at        = COALESCE($5, expires_at),
			revoke_token      = COALESCE(NULLIF($6,''), revoke_token),
			credentials_json  = COALESCE($7::jsonb, credentials_json),
			updated_at        = $8
		WHERE id = $1`,
		id, string(toState),
		u.ApproverIdentity, u.ApproverComment,
		u.ExpiresAt, u.RevokeToken,
		nullableJSON(credJSON), now,
	)
	if err != nil {
		return nil, fmt.Errorf("store: TransitionRequest update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: TransitionRequest commit: %w", err)
	}

	return s.GetRequest(ctx, id)
}

// nullableJSON returns nil if the input is empty, otherwise the raw bytes.
func nullableJSON(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	return string(b)
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
		&r.ApproverIdentity, &r.ApproverComment,
		&r.ExpiresAt, &r.RevokeToken, &credJSON,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan request: %w", err)
	}
	if len(metaJSON) > 0 {
		_ = json.Unmarshal(metaJSON, &r.Metadata)
	}
	if len(credJSON) > 0 {
		_ = json.Unmarshal(credJSON, &r.CredentialsJSON)
	}
	return r, nil
}
