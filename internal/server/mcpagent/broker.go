// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

// Package mcpagent implements the MCP agent-as-requestor surface for jitsudod.
// AI agents connect via MCP-over-SSE on a dedicated port, call request_access,
// and receive an access/resolved push notification the instant a decision is made.
package mcpagent

import (
	"context"
	"sync"
	"time"

	"github.com/jitsudo-dev/jitsudo/internal/server/notifications"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// brokerStore is the subset of *store.Store used by the Broker.
type brokerStore interface {
	GetRequest(ctx context.Context, id string) (*store.RequestRow, error)
}

// sseEvent is the payload pushed to a waiting SSE subscriber when a request resolves.
type sseEvent struct {
	RequestID   string            `json:"request_id"`
	Outcome     string            `json:"outcome"` // "approved" | "denied" | "expired" | "revoked"
	State       string            `json:"state"`
	Credentials map[string]string `json:"credentials,omitempty"`
	ExpiresAt   *time.Time        `json:"expires_at,omitempty"`
}

type subscription struct {
	ch       chan sseEvent
	identity string
}

// Broker fans resolution events to waiting SSE connections. It implements
// notifications.Notifier and is appended to the dispatcher's notifier list
// so it receives every workflow event.
type Broker struct {
	store brokerStore
	mu    sync.RWMutex
	subs  map[string][]subscription // key: requestID
}

// NewBroker creates a Broker backed by s for credential fetches.
func NewBroker(s brokerStore) *Broker {
	return &Broker{store: s, subs: make(map[string][]subscription)}
}

// Subscribe registers a waiter for the given requestID. Returns a read channel
// (buffered 1) that will receive at most one sseEvent when the request resolves,
// and an unsubscribe function the caller must defer.
func (b *Broker) Subscribe(requestID, identity string) (<-chan sseEvent, func()) {
	ch := make(chan sseEvent, 1)
	sub := subscription{ch: ch, identity: identity}

	b.mu.Lock()
	b.subs[requestID] = append(b.subs[requestID], sub)
	b.mu.Unlock()

	unsub := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subs := b.subs[requestID]
		for i, s := range subs {
			if s.ch == ch {
				b.subs[requestID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(b.subs[requestID]) == 0 {
			delete(b.subs, requestID)
		}
	}
	return ch, unsub
}

// Notify implements notifications.Notifier. For resolution events it fetches
// the current row (for credentials) and delivers to any waiting subscribers.
func (b *Broker) Notify(ctx context.Context, event notifications.Event) error {
	outcome := outcomeFor(event.Type)
	if outcome == "" {
		return nil // not a resolution event — nothing to push
	}

	b.mu.RLock()
	subs := b.subs[event.RequestID]
	if len(subs) == 0 {
		b.mu.RUnlock()
		return nil
	}
	// Copy slice so we can release the lock before fetching from store.
	targets := make([]subscription, len(subs))
	copy(targets, subs)
	b.mu.RUnlock()

	// Fetch the full row so we can include credentials for approved requests.
	req, err := b.store.GetRequest(ctx, event.RequestID)
	if err != nil {
		// Best-effort: send a minimal event without credentials rather than blocking.
		req = &store.RequestRow{ID: event.RequestID}
	}

	ev := sseEvent{
		RequestID: event.RequestID,
		Outcome:   outcome,
		State:     string(req.State),
		ExpiresAt: req.ExpiresAt,
	}
	if outcome == "approved" {
		ev.Credentials = req.CredentialsJSON
	}

	for _, sub := range targets {
		select {
		case sub.ch <- ev:
		default:
			// Channel full — subscriber is slow or gone; drop the event.
			// The SSE handler will detect client disconnect via ctx.Done.
		}
	}
	return nil
}

// outcomeFor maps a workflow EventType to the SSE outcome string.
// Returns "" for non-resolution events.
func outcomeFor(t notifications.EventType) string {
	switch t {
	case notifications.EventApproved, notifications.EventAutoApproved, notifications.EventAIApproved:
		return "approved"
	case notifications.EventDenied, notifications.EventAIDenied:
		return "denied"
	case notifications.EventExpired:
		return "expired"
	case notifications.EventRevoked:
		return "revoked"
	default:
		return ""
	}
}
