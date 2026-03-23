// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// AuditEventRow is a row from the audit_events table.
type AuditEventRow struct {
	ID            int64
	Timestamp     time.Time
	ActorIdentity string
	Action        string
	RequestID     string
	Provider      string
	ResourceScope string
	Outcome       string
	DetailsJSON   string
	PrevHash      string
	Hash          string
}

// AuditFilter selects audit events to return.
type AuditFilter struct {
	ActorIdentity string
	RequestID     string
	Provider      string
	Since         *time.Time
	Until         *time.Time
	Limit         int
}

// AppendAuditEvent appends an event to the audit log, computing the hash chain.
// The transaction is serializable to guarantee the hash chain is consistent
// under concurrent appends. On a serialization conflict (SQLSTATE 40001) the
// operation is retried up to 5 times with brief exponential backoff.
func (s *Store) AppendAuditEvent(ctx context.Context, e *AuditEventRow) (*AuditEventRow, error) {
	const maxAttempts = 5
	for attempt := range maxAttempts {
		result, err := s.appendAuditEventOnce(ctx, e)
		if err == nil {
			return result, nil
		}
		if !isSerializationError(err) || attempt == maxAttempts-1 {
			return nil, err
		}
		// Brief backoff before retry (10 ms, 20 ms, 30 ms, 40 ms).
		select {
		case <-time.After(time.Duration(attempt+1) * 10 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// All attempts exhausted — return the last error (loop exits via return above).
	return nil, fmt.Errorf("store: AppendAuditEvent: max retries exceeded")
}

// appendAuditEventOnce performs a single serializable append attempt.
func (s *Store) appendAuditEventOnce(ctx context.Context, e *AuditEventRow) (*AuditEventRow, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("store: AppendAuditEvent begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Fetch the most recent hash.
	var prevHash string
	err = tx.QueryRow(ctx,
		`SELECT hash FROM audit_events ORDER BY id DESC LIMIT 1`,
	).Scan(&prevHash)
	if err != nil && err != pgx.ErrNoRows {
		return nil, fmt.Errorf("store: AppendAuditEvent fetch prev hash: %w", err)
	}

	// Truncate to microsecond precision to match PostgreSQL TIMESTAMP storage,
	// so the hash computed here equals the hash recomputed from the stored timestamp.
	ts := time.Now().UTC().Truncate(time.Microsecond)
	hash := computeHash(prevHash, ts, e)

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO audit_events
			(timestamp, actor_identity, action, request_id, provider,
			 resource_scope, outcome, details_json, prev_hash, hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`,
		ts, e.ActorIdentity, e.Action,
		nullableStr(e.RequestID), e.Provider,
		e.ResourceScope, e.Outcome, e.DetailsJSON, prevHash, hash,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("store: AppendAuditEvent insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: AppendAuditEvent commit: %w", err)
	}

	e.ID = id
	e.Timestamp = ts
	e.PrevHash = prevHash
	e.Hash = hash
	return e, nil
}

// isSerializationError reports whether err is a PostgreSQL serialization failure
// (SQLSTATE 40001), which indicates a serializable transaction must be retried.
func isSerializationError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

// QueryAuditEvents returns audit events matching the filter.
func (s *Store) QueryAuditEvents(ctx context.Context, f AuditFilter) ([]*AuditEventRow, error) {
	query := `
		SELECT id, timestamp, actor_identity, action,
		       COALESCE(request_id,''), provider, resource_scope,
		       outcome, details_json, prev_hash, hash
		FROM audit_events WHERE 1=1`
	args := []any{}
	n := 1

	if f.ActorIdentity != "" {
		query += fmt.Sprintf(" AND actor_identity = $%d", n)
		args = append(args, f.ActorIdentity)
		n++
	}
	if f.RequestID != "" {
		query += fmt.Sprintf(" AND request_id = $%d", n)
		args = append(args, f.RequestID)
		n++
	}
	if f.Provider != "" {
		query += fmt.Sprintf(" AND provider = $%d", n)
		args = append(args, f.Provider)
		n++
	}
	if f.Since != nil {
		query += fmt.Sprintf(" AND timestamp >= $%d", n)
		args = append(args, *f.Since)
		n++
	}
	if f.Until != nil {
		query += fmt.Sprintf(" AND timestamp <= $%d", n)
		args = append(args, *f.Until)
		n++
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query += fmt.Sprintf(" ORDER BY id ASC LIMIT $%d", n)
	args = append(args, limit)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: QueryAuditEvents: %w", err)
	}
	defer rows.Close()

	var out []*AuditEventRow
	for rows.Next() {
		ev := &AuditEventRow{}
		if err := rows.Scan(
			&ev.ID, &ev.Timestamp, &ev.ActorIdentity, &ev.Action,
			&ev.RequestID, &ev.Provider, &ev.ResourceScope,
			&ev.Outcome, &ev.DetailsJSON, &ev.PrevHash, &ev.Hash,
		); err != nil {
			return nil, fmt.Errorf("store: scan audit event: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// computeHash returns the SHA-256 hash of the canonical audit event string:
// "<prevHash>|<unix_ns>|<actor>|<action>|<requestID>|<outcome>"
//
// This format is documented in ADR-011 so external auditors can verify
// the chain independently.
func computeHash(prevHash string, ts time.Time, e *AuditEventRow) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%s|%s|%s|%s",
		prevHash, ts.UnixNano(),
		e.ActorIdentity, e.Action, e.RequestID, e.Outcome,
	)
	return hex.EncodeToString(h.Sum(nil))
}

// nullableStr returns nil if s is empty, otherwise s. Used for nullable TEXT columns.
func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
