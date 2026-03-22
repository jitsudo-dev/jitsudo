// License: Elastic License 2.0 (ELv2)
package api

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// ── mock PolicyEngine ─────────────────────────────────────────────────────────

type mockPolicyEngine struct {
	reloadErr error
	evalRaw   func(ctx context.Context, ptype store.PolicyType, input map[string]any) (bool, string, error)
}

func (m *mockPolicyEngine) Reload(_ context.Context) error { return m.reloadErr }
func (m *mockPolicyEngine) EvalRaw(ctx context.Context, ptype store.PolicyType, input map[string]any) (bool, string, error) {
	if m.evalRaw != nil {
		return m.evalRaw(ctx, ptype, input)
	}
	return false, "", nil
}

// ── mock dataStore ────────────────────────────────────────────────────────────

type mockDataStore struct {
	listPoliciesRows []*store.PolicyRow
	listPoliciesErr  error
}

func (m *mockDataStore) GetRequest(_ context.Context, _ string) (*store.RequestRow, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDataStore) ListRequests(_ context.Context, _ store.ListFilter) ([]*store.RequestRow, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDataStore) ListPolicies(_ context.Context, _ *store.PolicyType) ([]*store.PolicyRow, error) {
	return m.listPoliciesRows, m.listPoliciesErr
}
func (m *mockDataStore) GetPolicy(_ context.Context, _ string) (*store.PolicyRow, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDataStore) UpsertPolicy(_ context.Context, _ *store.PolicyRow) (*store.PolicyRow, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDataStore) DeletePolicy(_ context.Context, _ string) error {
	return errors.New("not implemented")
}
func (m *mockDataStore) QueryAuditEvents(_ context.Context, _ store.AuditFilter) ([]*store.AuditEventRow, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDataStore) SetPrincipalTrustTier(_ context.Context, _ string, _ int, _ string) (*store.PrincipalRow, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDataStore) GetPrincipal(_ context.Context, _ string) (*store.PrincipalRow, error) {
	return nil, errors.New("not implemented")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func authedCtx() context.Context {
	return auth.WithIdentity(context.Background(), &auth.Identity{
		Subject: "sub-123",
		Email:   "admin@example.com",
	})
}

func adminCtx() context.Context {
	return auth.WithIdentity(context.Background(), &auth.Identity{
		Subject: "sub-admin",
		Email:   "admin@example.com",
		Groups:  []string{"jitsudo-admins"},
	})
}

func handlerWithMocks(p PolicyEngine, s dataStore) *Handler {
	return &Handler{policy: p, store: s}
}

// ── TestReloadPolicies ────────────────────────────────────────────────────────

func TestReloadPolicies_Unauthenticated(t *testing.T) {
	h := handlerWithMocks(&mockPolicyEngine{}, &mockDataStore{})
	_, err := h.ReloadPolicies(context.Background(), &emptypb.Empty{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code := status.Code(err); code != codes.Unauthenticated {
		t.Errorf("got %v, want %v", code, codes.Unauthenticated)
	}
}

func TestReloadPolicies_ReloadError(t *testing.T) {
	reloadErr := errors.New("opa compile failed")
	h := handlerWithMocks(
		&mockPolicyEngine{reloadErr: reloadErr},
		&mockDataStore{},
	)
	_, err := h.ReloadPolicies(authedCtx(), &emptypb.Empty{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("got %v, want %v", code, codes.Internal)
	}
}

func TestReloadPolicies_ListPoliciesError(t *testing.T) {
	h := handlerWithMocks(
		&mockPolicyEngine{},
		&mockDataStore{listPoliciesErr: errors.New("db timeout")},
	)
	_, err := h.ReloadPolicies(authedCtx(), &emptypb.Empty{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code := status.Code(err); code != codes.Internal {
		t.Errorf("got %v, want %v", code, codes.Internal)
	}
}

func TestReloadPolicies_CountsEnabledPolicies(t *testing.T) {
	policies := []*store.PolicyRow{
		{ID: "pol_1", Enabled: true},
		{ID: "pol_2", Enabled: false},
		{ID: "pol_3", Enabled: true},
	}
	h := handlerWithMocks(
		&mockPolicyEngine{},
		&mockDataStore{listPoliciesRows: policies},
	)
	resp, err := h.ReloadPolicies(authedCtx(), &emptypb.Empty{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetPoliciesLoaded() != 2 {
		t.Errorf("PoliciesLoaded = %d, want 2", resp.GetPoliciesLoaded())
	}
}

// ── TestSetPrincipalTrustTier ─────────────────────────────────────────────────

func TestSetPrincipalTrustTier_Unauthenticated(t *testing.T) {
	h := handlerWithMocks(&mockPolicyEngine{}, &mockDataStore{})
	_, err := h.SetPrincipalTrustTier(context.Background(), &jitsudov1alpha1.SetPrincipalTrustTierInput{
		Identity:  "alice@example.com",
		TrustTier: 2,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code := status.Code(err); code != codes.Unauthenticated {
		t.Errorf("got %v, want Unauthenticated", code)
	}
}

func TestSetPrincipalTrustTier_NonAdmin(t *testing.T) {
	h := handlerWithMocks(&mockPolicyEngine{}, &mockDataStore{})
	// authedCtx() has no Groups → isAdmin returns false
	_, err := h.SetPrincipalTrustTier(authedCtx(), &jitsudov1alpha1.SetPrincipalTrustTierInput{
		Identity:  "alice@example.com",
		TrustTier: 2,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code := status.Code(err); code != codes.PermissionDenied {
		t.Errorf("got %v, want PermissionDenied", code)
	}
}

func TestSetPrincipalTrustTier_InvalidTier(t *testing.T) {
	h := handlerWithMocks(&mockPolicyEngine{}, &mockDataStore{})
	_, err := h.SetPrincipalTrustTier(adminCtx(), &jitsudov1alpha1.SetPrincipalTrustTierInput{
		Identity:  "alice@example.com",
		TrustTier: 5, // out of range [0,4]
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("got %v, want InvalidArgument", code)
	}
}

func TestSetPrincipalTrustTier_MissingIdentity(t *testing.T) {
	h := handlerWithMocks(&mockPolicyEngine{}, &mockDataStore{})
	_, err := h.SetPrincipalTrustTier(adminCtx(), &jitsudov1alpha1.SetPrincipalTrustTierInput{
		Identity:  "", // empty
		TrustTier: 2,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("got %v, want InvalidArgument", code)
	}
}

// ── TestGetPrincipal ──────────────────────────────────────────────────────────

func TestGetPrincipal_Unauthenticated(t *testing.T) {
	h := handlerWithMocks(&mockPolicyEngine{}, &mockDataStore{})
	_, err := h.GetPrincipal(context.Background(), &jitsudov1alpha1.GetPrincipalInput{
		Identity: "alice@example.com",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code := status.Code(err); code != codes.Unauthenticated {
		t.Errorf("got %v, want Unauthenticated", code)
	}
}
