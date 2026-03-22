// License: Elastic License 2.0 (ELv2)
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PrincipalRow is a row from the principals table.
type PrincipalRow struct {
	Identity   string
	TrustTier  int
	EnrolledBy string
	EnrolledAt time.Time
	LastSeenAt time.Time
	Notes      string
}

// UpsertPrincipalLastSeen creates or updates the principal's last_seen_at
// timestamp, leaving trust_tier unchanged. Safe to call on every authenticated
// request (fire-and-forget). Creates the row with trust_tier=0 if absent.
func (s *Store) UpsertPrincipalLastSeen(ctx context.Context, identity string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO principals (identity, last_seen_at)
		VALUES ($1, NOW())
		ON CONFLICT (identity) DO UPDATE SET last_seen_at = NOW()`,
		identity,
	)
	if err != nil {
		return fmt.Errorf("store: UpsertPrincipalLastSeen: %w", err)
	}
	return nil
}

// SetPrincipalTrustTier upserts a principal's trust tier. Only admins should
// call this; the API layer enforces the group check before calling.
func (s *Store) SetPrincipalTrustTier(ctx context.Context, identity string, tier int, enrolledBy string) (*PrincipalRow, error) {
	if tier < 0 || tier > 4 {
		return nil, fmt.Errorf("store: SetPrincipalTrustTier: tier %d out of range [0,4]", tier)
	}
	now := time.Now().UTC()
	_, err := s.db.Exec(ctx, `
		INSERT INTO principals (identity, trust_tier, enrolled_by, enrolled_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (identity) DO UPDATE SET
			trust_tier   = EXCLUDED.trust_tier,
			enrolled_by  = EXCLUDED.enrolled_by,
			enrolled_at  = EXCLUDED.enrolled_at,
			last_seen_at = GREATEST(principals.last_seen_at, EXCLUDED.last_seen_at)`,
		identity, tier, enrolledBy, now,
	)
	if err != nil {
		return nil, fmt.Errorf("store: SetPrincipalTrustTier: %w", err)
	}
	return s.GetPrincipal(ctx, identity)
}

// GetPrincipal returns the principal row for the given identity.
// Returns ErrNotFound if the principal has never been seen.
func (s *Store) GetPrincipal(ctx context.Context, identity string) (*PrincipalRow, error) {
	row := s.db.QueryRow(ctx, `
		SELECT identity, trust_tier, enrolled_by, enrolled_at, last_seen_at, notes
		FROM principals WHERE identity = $1`, identity)
	p := &PrincipalRow{}
	err := row.Scan(&p.Identity, &p.TrustTier, &p.EnrolledBy, &p.EnrolledAt, &p.LastSeenAt, &p.Notes)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: GetPrincipal: %w", err)
	}
	return p, nil
}
