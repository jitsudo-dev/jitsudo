//go:build integration

package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/jitsudo-dev/jitsudo/internal/store"
	"github.com/jitsudo-dev/jitsudo/internal/testutil"
)

var testStore *store.Store

func TestMain(m *testing.M) {
	dsn := os.Getenv("JITSUDOD_DATABASE_URL")
	if dsn == "" {
		// No database available — skip all integration tests silently.
		os.Exit(0)
	}

	ctx := context.Background()

	if err := store.RunMigrations(dsn); err != nil {
		fmt.Fprintf(os.Stderr, "store integration: RunMigrations: %v\n", err)
		os.Exit(1)
	}

	s, err := store.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store integration: store.New: %v\n", err)
		os.Exit(1)
	}
	testStore = s

	code := m.Run()

	s.Close()
	os.Exit(code)
}

// newReqID returns a unique request ID for testing.
func newReqID() string { return "req_" + ulid.Make().String() }

// newPolicyID returns a unique policy ID for testing.
func newPolicyID() string { return "pol_" + ulid.Make().String() }

// newPolicyName returns a unique policy name for testing.
func newPolicyName() string { return "test-policy-" + ulid.Make().String() }

// TestStore_CreateAndGetRequest verifies basic request persistence.
func TestStore_CreateAndGetRequest(t *testing.T) {
	ctx := context.Background()
	_ = testutil.MustGetDBURL // accessed via TestMain above

	now := time.Now().UTC().Truncate(time.Millisecond)
	row := &store.RequestRow{
		ID:                newReqID(),
		State:             store.StatePending,
		RequesterIdentity: "alice@example.com",
		Provider:          "mock",
		Role:              "admin",
		ResourceScope:     "test-scope-1",
		DurationSeconds:   3600,
		Reason:            "integration test",
		BreakGlass:        false,
		Metadata:          map[string]string{"env": "test"},
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := testStore.CreateRequest(ctx, row); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	got, err := testStore.GetRequest(ctx, row.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}

	if got.ID != row.ID {
		t.Errorf("ID: got %q, want %q", got.ID, row.ID)
	}
	if got.State != store.StatePending {
		t.Errorf("State: got %q, want PENDING", got.State)
	}
	if got.RequesterIdentity != "alice@example.com" {
		t.Errorf("RequesterIdentity: got %q", got.RequesterIdentity)
	}
	if got.Provider != "mock" {
		t.Errorf("Provider: got %q", got.Provider)
	}
	if got.Metadata["env"] != "test" {
		t.Errorf("Metadata: got %v", got.Metadata)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}

	// Non-existent ID returns ErrNotFound.
	_, err = testStore.GetRequest(ctx, "req_does_not_exist")
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
}

// TestStore_ListRequests verifies filter-based request listing.
func TestStore_ListRequests(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	aliceID1 := newReqID()
	aliceID2 := newReqID()
	bobID := newReqID()

	for _, row := range []*store.RequestRow{
		{ID: aliceID1, State: store.StatePending, RequesterIdentity: "alice@example.com", Provider: "mock", Role: "r", ResourceScope: "s", DurationSeconds: 3600, Reason: "test", CreatedAt: now, UpdatedAt: now},
		{ID: aliceID2, State: store.StatePending, RequesterIdentity: "alice@example.com", Provider: "mock", Role: "r", ResourceScope: "s", DurationSeconds: 3600, Reason: "test", CreatedAt: now, UpdatedAt: now},
		{ID: bobID, State: store.StatePending, RequesterIdentity: "bob@example.com", Provider: "mock", Role: "r", ResourceScope: "s", DurationSeconds: 3600, Reason: "test", CreatedAt: now, UpdatedAt: now},
	} {
		if err := testStore.CreateRequest(ctx, row); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
	}

	// Filter by requester identity.
	aliceRows, err := testStore.ListRequests(ctx, store.ListFilter{RequesterIdentity: "alice@example.com"})
	if err != nil {
		t.Fatalf("ListRequests(alice): %v", err)
	}
	aliceIDs := make(map[string]bool)
	for _, r := range aliceRows {
		aliceIDs[r.ID] = true
	}
	if !aliceIDs[aliceID1] || !aliceIDs[aliceID2] {
		t.Errorf("alice's requests not found in filtered list")
	}
	if aliceIDs[bobID] {
		t.Error("bob's request incorrectly included in alice filter")
	}

	// Filter by state returns at least the 3 rows we created.
	pendingRows, err := testStore.ListRequests(ctx, store.ListFilter{State: store.StatePending})
	if err != nil {
		t.Fatalf("ListRequests(PENDING): %v", err)
	}
	if len(pendingRows) < 3 {
		t.Errorf("expected ≥3 PENDING rows, got %d", len(pendingRows))
	}
}

// TestStore_TransitionRequest_PendingToRejected verifies the PENDING→REJECTED transition.
func TestStore_TransitionRequest_PendingToRejected(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	row := &store.RequestRow{
		ID: newReqID(), State: store.StatePending,
		RequesterIdentity: "alice@example.com", Provider: "mock",
		Role: "admin", ResourceScope: "scope", DurationSeconds: 3600,
		Reason: "test", CreatedAt: now, UpdatedAt: now,
	}
	if err := testStore.CreateRequest(ctx, row); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	updated, err := testStore.TransitionRequest(ctx, row.ID, store.StatePending, store.StateRejected, store.RequestUpdate{
		ApproverIdentity: "bob@example.com",
		ApproverComment:  "not today",
	})
	if err != nil {
		t.Fatalf("TransitionRequest: %v", err)
	}
	if updated.State != store.StateRejected {
		t.Errorf("State: got %q, want REJECTED", updated.State)
	}
	if updated.ApproverIdentity != "bob@example.com" {
		t.Errorf("ApproverIdentity: got %q", updated.ApproverIdentity)
	}
	if updated.ApproverComment != "not today" {
		t.Errorf("ApproverComment: got %q", updated.ApproverComment)
	}

	// Confirm state persisted.
	re, err := testStore.GetRequest(ctx, row.ID)
	if err != nil {
		t.Fatalf("GetRequest after reject: %v", err)
	}
	if re.State != store.StateRejected {
		t.Errorf("persisted State: got %q", re.State)
	}
}

// TestStore_TransitionRequest_FullApproveFlow verifies PENDING→APPROVED→ACTIVE.
func TestStore_TransitionRequest_FullApproveFlow(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	row := &store.RequestRow{
		ID: newReqID(), State: store.StatePending,
		RequesterIdentity: "alice@example.com", Provider: "mock",
		Role: "engineer", ResourceScope: "dev", DurationSeconds: 300,
		Reason: "full flow test", CreatedAt: now, UpdatedAt: now,
	}
	if err := testStore.CreateRequest(ctx, row); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	// PENDING → APPROVED
	approved, err := testStore.TransitionRequest(ctx, row.ID, store.StatePending, store.StateApproved, store.RequestUpdate{
		ApproverIdentity: "bob@example.com",
		ApproverComment:  "LGTM",
	})
	if err != nil {
		t.Fatalf("TransitionRequest PENDING→APPROVED: %v", err)
	}
	if approved.State != store.StateApproved {
		t.Errorf("after approve: State = %q", approved.State)
	}

	// APPROVED → ACTIVE
	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	active, err := testStore.TransitionRequest(ctx, row.ID, store.StateApproved, store.StateActive, store.RequestUpdate{
		ExpiresAt:       &expiresAt,
		RevokeToken:     "revoke-token-xyz",
		CredentialsJSON: map[string]string{"MOCK_ACCESS_KEY": "AKIATEST", "MOCK_SESSION_TOKEN": "sess123"},
	})
	if err != nil {
		t.Fatalf("TransitionRequest APPROVED→ACTIVE: %v", err)
	}
	if active.State != store.StateActive {
		t.Errorf("after activate: State = %q", active.State)
	}
	if active.ExpiresAt == nil {
		t.Error("ExpiresAt should be set after ACTIVE transition")
	}
	if active.CredentialsJSON["MOCK_ACCESS_KEY"] != "AKIATEST" {
		t.Errorf("CredentialsJSON: %v", active.CredentialsJSON)
	}
}

// TestStore_TransitionRequest_WrongState verifies that wrong fromState returns ErrWrongState.
func TestStore_TransitionRequest_WrongState(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	row := &store.RequestRow{
		ID: newReqID(), State: store.StatePending,
		RequesterIdentity: "alice@example.com", Provider: "mock",
		Role: "r", ResourceScope: "s", DurationSeconds: 3600,
		Reason: "wrong state test", CreatedAt: now, UpdatedAt: now,
	}
	if err := testStore.CreateRequest(ctx, row); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	// Attempt transition from APPROVED (request is actually PENDING).
	_, err := testStore.TransitionRequest(ctx, row.ID, store.StateApproved, store.StateActive, store.RequestUpdate{})
	if err == nil {
		t.Fatal("expected ErrWrongState, got nil")
	}
}

// TestStore_TransitionRequest_NotFound verifies that a missing ID returns ErrNotFound.
func TestStore_TransitionRequest_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := testStore.TransitionRequest(ctx, "req_does_not_exist", store.StatePending, store.StateRejected, store.RequestUpdate{})
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
}

// TestStore_ListActiveExpired verifies the expiry sweeper query.
func TestStore_ListActiveExpired(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Create request that will be expired (ExpiresAt 1 hour ago).
	expiredID := newReqID()
	freshID := newReqID()

	for _, id := range []string{expiredID, freshID} {
		row := &store.RequestRow{
			ID: id, State: store.StatePending,
			RequesterIdentity: "alice@example.com", Provider: "mock",
			Role: "r", ResourceScope: "s", DurationSeconds: 3600,
			Reason: "expiry test", CreatedAt: now, UpdatedAt: now,
		}
		if err := testStore.CreateRequest(ctx, row); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
		if _, err := testStore.TransitionRequest(ctx, id, store.StatePending, store.StateApproved, store.RequestUpdate{
			ApproverIdentity: "bob@example.com",
		}); err != nil {
			t.Fatalf("TransitionRequest PENDING→APPROVED: %v", err)
		}
	}

	pastExpiry := time.Now().UTC().Add(-1 * time.Hour)
	futureExpiry := time.Now().UTC().Add(1 * time.Hour)

	if _, err := testStore.TransitionRequest(ctx, expiredID, store.StateApproved, store.StateActive, store.RequestUpdate{
		ExpiresAt: &pastExpiry, RevokeToken: "tok1",
		CredentialsJSON: map[string]string{"K": "V"},
	}); err != nil {
		t.Fatalf("TransitionRequest expired→ACTIVE: %v", err)
	}

	if _, err := testStore.TransitionRequest(ctx, freshID, store.StateApproved, store.StateActive, store.RequestUpdate{
		ExpiresAt: &futureExpiry, RevokeToken: "tok2",
		CredentialsJSON: map[string]string{"K": "V"},
	}); err != nil {
		t.Fatalf("TransitionRequest fresh→ACTIVE: %v", err)
	}

	expired, err := testStore.ListActiveExpired(ctx)
	if err != nil {
		t.Fatalf("ListActiveExpired: %v", err)
	}

	ids := make(map[string]bool)
	for _, r := range expired {
		ids[r.ID] = true
	}
	if !ids[expiredID] {
		t.Errorf("expired request %s not in ListActiveExpired results", expiredID)
	}
	if ids[freshID] {
		t.Errorf("fresh request %s incorrectly in ListActiveExpired results", freshID)
	}
}

// TestStore_UpsertAndGetPolicy verifies policy creation and update by name.
//
// Test policies use a non-standard Rego package (package test_store_jitsudo)
// so they don't interfere with the OPA engine's jitsudo.eligibility namespace,
// and are created with Enabled:false so the server won't try to load them.
func TestStore_UpsertAndGetPolicy(t *testing.T) {
	ctx := context.Background()

	name := newPolicyName()
	p := &store.PolicyRow{
		ID:          newPolicyID(),
		Name:        name,
		Type:        store.PolicyTypeEligibility,
		Rego:        "package test_store_jitsudo\ndefault allow := true",
		Description: "initial version",
		Enabled:     false, // disabled so OPA doesn't load it
		UpdatedBy:   "test",
	}

	created, err := testStore.UpsertPolicy(ctx, p)
	if err != nil {
		t.Fatalf("UpsertPolicy (create): %v", err)
	}
	t.Cleanup(func() { testStore.DeletePolicy(ctx, created.ID) }) //nolint:errcheck
	if created.Name != name {
		t.Errorf("Name: got %q, want %q", created.Name, name)
	}
	if created.Type != store.PolicyTypeEligibility {
		t.Errorf("Type: got %q", created.Type)
	}

	// Update: same name, different rego.
	p.Rego = "package test_store_jitsudo\ndefault allow := false"
	p.Description = "updated version"
	updated, err := testStore.UpsertPolicy(ctx, p)
	if err != nil {
		t.Fatalf("UpsertPolicy (update): %v", err)
	}
	if updated.Rego != "package test_store_jitsudo\ndefault allow := false" {
		t.Errorf("Rego not updated: got %q", updated.Rego)
	}
	if updated.Description != "updated version" {
		t.Errorf("Description not updated: got %q", updated.Description)
	}

	// GetPolicy by ID.
	got, err := testStore.GetPolicy(ctx, updated.ID)
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if got.Name != name {
		t.Errorf("GetPolicy.Name: got %q", got.Name)
	}

	// GetPolicyByName.
	byName, err := testStore.GetPolicyByName(ctx, name)
	if err != nil {
		t.Fatalf("GetPolicyByName: %v", err)
	}
	if byName.ID != got.ID {
		t.Errorf("GetPolicyByName returned different ID")
	}

	// ListPolicies should include our policy.
	list, err := testStore.ListPolicies(ctx, nil)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	found := false
	for _, lp := range list {
		if lp.Name == name {
			found = true
		}
	}
	if !found {
		t.Errorf("policy %q not found in ListPolicies", name)
	}
}

// TestStore_DeletePolicy verifies policy deletion.
func TestStore_DeletePolicy(t *testing.T) {
	ctx := context.Background()

	p := &store.PolicyRow{
		ID: newPolicyID(), Name: newPolicyName(),
		Type:    store.PolicyTypeApproval,
		Rego:    "package test_store_jitsudo\ndefault allow := false",
		Enabled: false, UpdatedBy: "test",
	}
	created, err := testStore.UpsertPolicy(ctx, p)
	if err != nil {
		t.Fatalf("UpsertPolicy: %v", err)
	}

	if err := testStore.DeletePolicy(ctx, created.ID); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}

	// GetPolicy after delete returns ErrNotFound.
	_, err = testStore.GetPolicy(ctx, created.ID)
	if err == nil {
		t.Fatal("expected ErrNotFound after delete, got nil")
	}

	// Deleting again also returns ErrNotFound.
	err = testStore.DeletePolicy(ctx, created.ID)
	if err == nil {
		t.Fatal("expected ErrNotFound on second delete, got nil")
	}
}

// TestStore_AppendAndQueryAuditEvents verifies audit log append and filtering.
func TestStore_AppendAndQueryAuditEvents(t *testing.T) {
	ctx := context.Background()
	reqID := newReqID()

	events := []*store.AuditEventRow{
		{ActorIdentity: "alice@example.com", Action: "request.created", RequestID: reqID, Provider: "mock", ResourceScope: "scope", Outcome: "success", DetailsJSON: `{}`},
		{ActorIdentity: "bob@example.com", Action: "request.approved", RequestID: reqID, Provider: "mock", ResourceScope: "scope", Outcome: "success", DetailsJSON: `{}`},
		{ActorIdentity: "alice@example.com", Action: "grant.issued", RequestID: reqID, Provider: "mock", ResourceScope: "scope", Outcome: "success", DetailsJSON: `{}`},
	}

	for _, e := range events {
		if _, err := testStore.AppendAuditEvent(ctx, e); err != nil {
			t.Fatalf("AppendAuditEvent: %v", err)
		}
	}

	// Filter by request ID.
	byReq, err := testStore.QueryAuditEvents(ctx, store.AuditFilter{RequestID: reqID})
	if err != nil {
		t.Fatalf("QueryAuditEvents(requestID): %v", err)
	}
	if len(byReq) < 3 {
		t.Errorf("expected ≥3 events for request, got %d", len(byReq))
	}

	// Filter by actor identity (alice).
	byAlice, err := testStore.QueryAuditEvents(ctx, store.AuditFilter{RequestID: reqID, ActorIdentity: "alice@example.com"})
	if err != nil {
		t.Fatalf("QueryAuditEvents(alice): %v", err)
	}
	for _, e := range byAlice {
		if e.ActorIdentity != "alice@example.com" {
			t.Errorf("non-alice event returned: actor=%q", e.ActorIdentity)
		}
	}
	if len(byAlice) < 2 {
		t.Errorf("expected ≥2 alice events, got %d", len(byAlice))
	}
}

// TestStore_AuditHashChain verifies the tamper-evident hash chain on audit events.
//
// The hash formula (from store/audit.go:computeHash and ADR-011) is:
//
//	sha256("<prevHash>|<unix_ns>|<actor>|<action>|<requestID>|<outcome>")
func TestStore_AuditHashChain(t *testing.T) {
	ctx := context.Background()
	reqID := newReqID()

	actions := []string{"a.created", "b.approved", "c.issued", "d.revoked", "e.expired"}
	for _, action := range actions {
		e := &store.AuditEventRow{
			ActorIdentity: "alice@example.com",
			Action:        action,
			RequestID:     reqID,
			Provider:      "mock",
			ResourceScope: "scope",
			Outcome:       "success",
			DetailsJSON:   `{}`,
		}
		if _, err := testStore.AppendAuditEvent(ctx, e); err != nil {
			t.Fatalf("AppendAuditEvent(%s): %v", action, err)
		}
	}

	evts, err := testStore.QueryAuditEvents(ctx, store.AuditFilter{RequestID: reqID})
	if err != nil {
		t.Fatalf("QueryAuditEvents: %v", err)
	}
	if len(evts) < len(actions) {
		t.Fatalf("expected ≥%d events, got %d", len(actions), len(evts))
	}

	// Verify hash chain: each event's hash must match what we'd compute,
	// and each event's PrevHash must equal the prior event's Hash.
	for i, e := range evts {
		// Recompute expected hash using the same formula as store.computeHash.
		h := sha256.New()
		fmt.Fprintf(h, "%s|%d|%s|%s|%s|%s",
			e.PrevHash,
			e.Timestamp.UnixNano(),
			e.ActorIdentity, e.Action, e.RequestID, e.Outcome,
		)
		want := hex.EncodeToString(h.Sum(nil))
		if e.Hash != want {
			t.Errorf("event[%d] hash mismatch: got %q, want %q", i, e.Hash, want)
		}

		if i > 0 && e.PrevHash != evts[i-1].Hash {
			t.Errorf("event[%d].PrevHash=%q, but event[%d].Hash=%q",
				i, e.PrevHash, i-1, evts[i-1].Hash)
		}
	}
}
