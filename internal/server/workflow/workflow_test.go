// License: Elastic License 2.0 (ELv2)
package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	"github.com/jitsudo-dev/jitsudo/internal/providers/mock"
	"github.com/jitsudo-dev/jitsudo/internal/server/audit"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// ── In-memory stubs ───────────────────────────────────────────────────────────

// stubStore is a minimal in-memory implementation of engineStore for unit tests.
type stubStore struct {
	mu   sync.Mutex
	rows map[string]*store.RequestRow

	// Inject errors for specific operations.
	transitionErr error
	setTierErr    error
}

func newStubStore(rows ...*store.RequestRow) *stubStore {
	s := &stubStore{rows: make(map[string]*store.RequestRow)}
	for _, r := range rows {
		cp := *r
		s.rows[r.ID] = &cp
	}
	return s
}

func (s *stubStore) UpsertPrincipalLastSeen(_ context.Context, _ string) error { return nil }

func (s *stubStore) CreateRequest(_ context.Context, req *store.RequestRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *req
	s.rows[req.ID] = &cp
	return nil
}

func (s *stubStore) GetRequest(_ context.Context, id string) (*store.RequestRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *row
	return &cp, nil
}

func (s *stubStore) TransitionRequest(_ context.Context, id string, from, to store.RequestState, u store.RequestUpdate) (*store.RequestRow, error) {
	if s.transitionErr != nil {
		return nil, s.transitionErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if row.State != from {
		return nil, fmt.Errorf("%w: expected %s, got %s", store.ErrWrongState, from, row.State)
	}
	row.State = to
	if u.ApproverIdentity != "" {
		row.ApproverIdentity = u.ApproverIdentity
	}
	if u.ApproverComment != "" {
		row.ApproverComment = u.ApproverComment
	}
	if u.AIReasoningJSON != "" {
		row.AIReasoningJSON = u.AIReasoningJSON
	}
	if u.ExpiresAt != nil {
		row.ExpiresAt = u.ExpiresAt
	}
	if u.RevokeToken != "" {
		row.RevokeToken = u.RevokeToken
	}
	if u.CredentialsJSON != nil {
		row.CredentialsJSON = u.CredentialsJSON
	}
	cp := *row
	return &cp, nil
}

func (s *stubStore) SetApproverTier(_ context.Context, id, tier string) error {
	if s.setTierErr != nil {
		return s.setTierErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return store.ErrNotFound
	}
	row.ApproverTier = tier
	return nil
}

func (s *stubStore) ListActiveExpired(_ context.Context) ([]*store.RequestRow, error) {
	return nil, nil
}

// stubAudit records all Append calls for assertion.
type stubAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *stubAudit) Append(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *stubAudit) actions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.entries {
		out = append(out, e.Action)
	}
	return out
}

func (a *stubAudit) findByAction(action string) (audit.Entry, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.entries {
		if e.Action == action {
			return e, true
		}
	}
	return audit.Entry{}, false
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func pendingAIRow(id string) *store.RequestRow {
	now := time.Now().UTC()
	return &store.RequestRow{
		ID:                id,
		State:             store.StatePending,
		RequesterIdentity: "alice@example.com",
		Provider:          "mock",
		Role:              "admin",
		ResourceScope:     "test-scope",
		DurationSeconds:   3600,
		Reason:            "unit test",
		ApproverTier:      "ai_review",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func newTestEngine(s engineStore, a auditAppender) *Engine {
	registry := providers.NewRegistry()
	registry.Register(mock.New())
	return NewEngine(s, a, nil /* policy — not used by AI methods */, registry, nil /* notifications — nil-safe */)
}

// ── AIApproveRequest ──────────────────────────────────────────────────────────

func TestAIApproveRequest_HappyPath(t *testing.T) {
	const reqID = "req_ai_approve_001"
	s := newStubStore(pendingAIRow(reqID))
	a := &stubAudit{}
	e := newTestEngine(s, a)

	reasoning := `{"assessment":"low risk","confidence":0.95}`
	req, err := e.AIApproveRequest(context.Background(), reqID, "test-agent", reasoning)
	if err != nil {
		t.Fatalf("AIApproveRequest: %v", err)
	}

	// Final state must be ACTIVE.
	if req.State != store.StateActive {
		t.Errorf("State = %q, want ACTIVE", req.State)
	}
	// Approver identity set to the agent.
	if req.ApproverIdentity != "test-agent" {
		t.Errorf("ApproverIdentity = %q, want %q", req.ApproverIdentity, "test-agent")
	}
	// Credentials issued.
	if len(req.CredentialsJSON) == 0 {
		t.Error("expected non-empty CredentialsJSON after approval")
	}
	// Expiry set.
	if req.ExpiresAt == nil || req.ExpiresAt.IsZero() {
		t.Error("expected non-zero ExpiresAt after approval")
	}

	// Audit: both ai_approved and grant.issued must appear.
	actions := a.actions()
	for _, want := range []string{audit.ActionRequestAIApproved, audit.ActionGrantIssued} {
		found := false
		for _, got := range actions {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("audit missing %q; got %v", want, actions)
		}
	}

	// Reasoning JSON stored on the ai_approved entry.
	if entry, ok := a.findByAction(audit.ActionRequestAIApproved); ok {
		if entry.DetailsJSON != reasoning {
			t.Errorf("audit ai_approved DetailsJSON = %q, want %q", entry.DetailsJSON, reasoning)
		}
		if entry.ActorIdentity != "test-agent" {
			t.Errorf("audit ai_approved ActorIdentity = %q, want %q", entry.ActorIdentity, "test-agent")
		}
	} else {
		t.Error("ai_approved audit entry not found")
	}
}

func TestAIApproveRequest_StoreTransitionError(t *testing.T) {
	s := newStubStore(pendingAIRow("req_001"))
	s.transitionErr = errors.New("db unavailable")
	e := newTestEngine(s, &stubAudit{})

	_, err := e.AIApproveRequest(context.Background(), "req_001", "agent", "{}")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAIApproveRequest_UnknownProvider(t *testing.T) {
	row := pendingAIRow("req_002")
	row.Provider = "nonexistent"
	s := newStubStore(row)
	e := newTestEngine(s, &stubAudit{})

	_, err := e.AIApproveRequest(context.Background(), "req_002", "agent", "{}")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestAIApproveRequest_NotFound(t *testing.T) {
	e := newTestEngine(newStubStore(), &stubAudit{})
	_, err := e.AIApproveRequest(context.Background(), "req_does_not_exist", "agent", "{}")
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
}

// ── AIDenyRequest ─────────────────────────────────────────────────────────────

func TestAIDenyRequest_HappyPath(t *testing.T) {
	const reqID = "req_ai_deny_001"
	s := newStubStore(pendingAIRow(reqID))
	a := &stubAudit{}
	e := newTestEngine(s, a)

	reasoning := "blast radius too high for this scope"
	req, err := e.AIDenyRequest(context.Background(), reqID, "test-agent", reasoning)
	if err != nil {
		t.Fatalf("AIDenyRequest: %v", err)
	}

	// Final state must be REJECTED.
	if req.State != store.StateRejected {
		t.Errorf("State = %q, want REJECTED", req.State)
	}
	if req.ApproverIdentity != "test-agent" {
		t.Errorf("ApproverIdentity = %q, want %q", req.ApproverIdentity, "test-agent")
	}

	// Audit: ai_denied must appear.
	if _, ok := a.findByAction(audit.ActionRequestAIDenied); !ok {
		t.Errorf("audit missing %q; got %v", audit.ActionRequestAIDenied, a.actions())
	}

	// Reasoning stored on the audit entry.
	if entry, ok := a.findByAction(audit.ActionRequestAIDenied); ok {
		if entry.DetailsJSON != reasoning {
			t.Errorf("audit ai_denied DetailsJSON = %q, want %q", entry.DetailsJSON, reasoning)
		}
	}
}

func TestAIDenyRequest_StoreError(t *testing.T) {
	s := newStubStore(pendingAIRow("req_001"))
	s.transitionErr = errors.New("db unavailable")
	e := newTestEngine(s, &stubAudit{})

	_, err := e.AIDenyRequest(context.Background(), "req_001", "agent", "reason")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAIDenyRequest_NotFound(t *testing.T) {
	e := newTestEngine(newStubStore(), &stubAudit{})
	_, err := e.AIDenyRequest(context.Background(), "req_does_not_exist", "agent", "reason")
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
}

// ── AIEscalateRequest ─────────────────────────────────────────────────────────

func TestAIEscalateRequest_HappyPath(t *testing.T) {
	const reqID = "req_ai_escalate_001"
	s := newStubStore(pendingAIRow(reqID))
	a := &stubAudit{}
	e := newTestEngine(s, a)

	reasoning := "uncertain — linked incident may broaden scope"
	req, err := e.AIEscalateRequest(context.Background(), reqID, "test-agent", reasoning)
	if err != nil {
		t.Fatalf("AIEscalateRequest: %v", err)
	}

	// State stays PENDING.
	if req.State != store.StatePending {
		t.Errorf("State = %q, want PENDING", req.State)
	}
	// approver_tier flipped to human.
	if req.ApproverTier != "human" {
		t.Errorf("ApproverTier = %q, want %q", req.ApproverTier, "human")
	}

	// Audit: ai_escalated must appear.
	if _, ok := a.findByAction(audit.ActionRequestAIEscalated); !ok {
		t.Errorf("audit missing %q; got %v", audit.ActionRequestAIEscalated, a.actions())
	}

	// Reasoning stored on the audit entry.
	if entry, ok := a.findByAction(audit.ActionRequestAIEscalated); ok {
		if entry.DetailsJSON != reasoning {
			t.Errorf("audit ai_escalated DetailsJSON = %q, want %q", entry.DetailsJSON, reasoning)
		}
		if entry.ActorIdentity != "test-agent" {
			t.Errorf("audit ai_escalated ActorIdentity = %q, want %q", entry.ActorIdentity, "test-agent")
		}
	}
}

func TestAIEscalateRequest_NotFound(t *testing.T) {
	e := newTestEngine(newStubStore(), &stubAudit{})
	_, err := e.AIEscalateRequest(context.Background(), "req_does_not_exist", "agent", "reason")
	if err == nil {
		t.Fatal("expected error for missing request, got nil")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestAIEscalateRequest_SetTierError(t *testing.T) {
	s := newStubStore(pendingAIRow("req_001"))
	s.setTierErr = errors.New("db unavailable")
	e := newTestEngine(s, &stubAudit{})

	_, err := e.AIEscalateRequest(context.Background(), "req_001", "agent", "reason")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
