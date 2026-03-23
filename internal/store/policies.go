// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PolicyType mirrors the policy_type DB enum.
type PolicyType string

const (
	PolicyTypeEligibility PolicyType = "ELIGIBILITY"
	PolicyTypeApproval    PolicyType = "APPROVAL"
)

// PolicyRow is a row from the policies table.
type PolicyRow struct {
	ID          string
	Name        string
	Type        PolicyType
	Rego        string
	Description string
	Enabled     bool
	UpdatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UpsertPolicy creates or updates a policy by name (idempotent by name).
func (s *Store) UpsertPolicy(ctx context.Context, p *PolicyRow) (*PolicyRow, error) {
	now := time.Now().UTC()
	_, err := s.db.Exec(ctx, `
		INSERT INTO policies (id, name, type, rego, description, enabled, updated_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (name) DO UPDATE SET
			type        = EXCLUDED.type,
			rego        = EXCLUDED.rego,
			description = EXCLUDED.description,
			enabled     = EXCLUDED.enabled,
			updated_by  = EXCLUDED.updated_by,
			updated_at  = EXCLUDED.updated_at`,
		p.ID, p.Name, string(p.Type), p.Rego,
		p.Description, p.Enabled, p.UpdatedBy, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("store: UpsertPolicy: %w", err)
	}
	return s.GetPolicyByName(ctx, p.Name)
}

// GetPolicy fetches a policy by ID.
func (s *Store) GetPolicy(ctx context.Context, id string) (*PolicyRow, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, type, rego, description, enabled, updated_by, created_at, updated_at
		FROM policies WHERE id = $1`, id)
	return scanPolicy(row)
}

// GetPolicyByName fetches a policy by unique name.
func (s *Store) GetPolicyByName(ctx context.Context, name string) (*PolicyRow, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, type, rego, description, enabled, updated_by, created_at, updated_at
		FROM policies WHERE name = $1`, name)
	return scanPolicy(row)
}

// ListPolicies returns all policies, optionally filtered by type.
// Pass nil for ptype to return all types.
func (s *Store) ListPolicies(ctx context.Context, ptype *PolicyType) ([]*PolicyRow, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if ptype != nil {
		rows, err = s.db.Query(ctx, `
			SELECT id, name, type, rego, description, enabled, updated_by, created_at, updated_at
			FROM policies WHERE type = $1 ORDER BY name`, string(*ptype))
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT id, name, type, rego, description, enabled, updated_by, created_at, updated_at
			FROM policies ORDER BY name`)
	}
	if err != nil {
		return nil, fmt.Errorf("store: ListPolicies: %w", err)
	}
	defer rows.Close()

	var out []*PolicyRow
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListEnabledPoliciesByType returns enabled policies for a given type (used by OPA engine).
func (s *Store) ListEnabledPoliciesByType(ctx context.Context, ptype PolicyType) ([]*PolicyRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, type, rego, description, enabled, updated_by, created_at, updated_at
		FROM policies WHERE type = $1 AND enabled = TRUE ORDER BY name`, string(ptype))
	if err != nil {
		return nil, fmt.Errorf("store: ListEnabledPoliciesByType: %w", err)
	}
	defer rows.Close()

	var out []*PolicyRow
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePolicy removes a policy by ID.
func (s *Store) DeletePolicy(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM policies WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: DeletePolicy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanPolicy(row interface{ Scan(...any) error }) (*PolicyRow, error) {
	p := &PolicyRow{}
	err := row.Scan(
		&p.ID, &p.Name, &p.Type, &p.Rego,
		&p.Description, &p.Enabled, &p.UpdatedBy,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan policy: %w", err)
	}
	return p, nil
}
