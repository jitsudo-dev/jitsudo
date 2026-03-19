// Package store implements the PostgreSQL data access layer for jitsudod.
//
// License: Elastic License 2.0 (ELv2)
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrWrongState is returned by TransitionRequest when the current state
// does not match the expected from-state.
var ErrWrongState = errors.New("store: wrong state for transition")

// Store is the database access layer. All SQL lives here; no SQL appears
// in the server, workflow, audit, or policy packages.
type Store struct {
	db *pgxpool.Pool
}

// New opens a pgx connection pool and verifies connectivity.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{db: pool}, nil
}

// Ping checks database connectivity. Used by /readyz.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.Ping(ctx)
}

// Close releases all pool connections.
func (s *Store) Close() {
	s.db.Close()
}
