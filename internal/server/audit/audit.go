// Package audit implements the append-only, tamper-evident audit log.
// Each entry includes a SHA-256 hash of the previous entry, forming a
// hash chain. Entries are never updated or deleted.
//
// License: Elastic License 2.0 (ELv2)
package audit

import (
	"context"
	"fmt"

	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// Action constants for all audit-logged events.
const (
	ActionRequestCreated      = "request.created"
	ActionRequestApproved     = "request.approved"
	ActionRequestAutoApproved = "request.auto_approved"
	ActionRequestDenied       = "request.denied"
	ActionRequestAIApproved   = "request.ai_approved"
	ActionRequestAIDenied     = "request.ai_denied"
	ActionRequestAIEscalated  = "request.ai_escalated"
	ActionGrantIssued         = "grant.issued"
	ActionGrantExpired        = "grant.expired"
	ActionGrantRevoked        = "grant.revoked"
)

// Outcome values.
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// Entry is a single event to be appended to the audit log.
type Entry struct {
	ActorIdentity string
	Action        string
	RequestID     string
	Provider      string
	ResourceScope string
	Outcome       string
	DetailsJSON   string // optional JSON blob; defaults to "{}"
}

// Logger appends audit events to PostgreSQL with a tamper-evident hash chain.
type Logger struct {
	store *store.Store
}

// NewLogger creates a Logger backed by the given store.
func NewLogger(s *store.Store) *Logger {
	return &Logger{store: s}
}

// Append writes an audit event. The hash chain is maintained by the store layer.
func (l *Logger) Append(ctx context.Context, e Entry) error {
	details := e.DetailsJSON
	if details == "" {
		details = "{}"
	}
	row := &store.AuditEventRow{
		ActorIdentity: e.ActorIdentity,
		Action:        e.Action,
		RequestID:     e.RequestID,
		Provider:      e.Provider,
		ResourceScope: e.ResourceScope,
		Outcome:       e.Outcome,
		DetailsJSON:   details,
	}
	if _, err := l.store.AppendAuditEvent(ctx, row); err != nil {
		return fmt.Errorf("audit: append %s: %w", e.Action, err)
	}
	return nil
}
