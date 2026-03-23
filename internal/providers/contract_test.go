// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

// Package providers contains the contract test suite that all Provider implementations
// must satisfy. Any new provider must be added to the providerFactories map and
// must pass every test in this file.
//
// Run with: go test ./internal/providers/... -short
package providers_test

import (
	"context"
	"testing"
	"time"

	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	"github.com/jitsudo-dev/jitsudo/internal/providers/kubernetes"
	"github.com/jitsudo-dev/jitsudo/internal/providers/mock"
)

// providerFactory is a function that returns a fresh Provider instance for testing.
type providerFactory func(t *testing.T) providers.Provider

// providerFactories lists all Provider implementations subject to contract tests.
// Add new providers here when they are implemented.
var providerFactories = map[string]providerFactory{
	"mock": func(t *testing.T) providers.Provider {
		return mock.New()
	},
	"kubernetes": func(t *testing.T) providers.Provider {
		return kubernetes.NewWithClientset(
			kubernetes.Config{ManagedLabel: "jitsudo.dev/managed"},
			k8sfake.NewSimpleClientset(),
		)
	},
	// "aws":   func(t *testing.T) providers.Provider { ... },  // add when implemented (requires //go:build integration)
	// "azure": func(t *testing.T) providers.Provider { ... },
	// "gcp":   func(t *testing.T) providers.Provider { ... },
}

func TestProviderContract(t *testing.T) {
	for name, factory := range providerFactories {
		t.Run(name, func(t *testing.T) {
			p := factory(t)
			runContractTests(t, p)
		})
	}
}

func runContractTests(t *testing.T, p providers.Provider) {
	t.Helper()

	t.Run("Name is non-empty and stable", func(t *testing.T) {
		name := p.Name()
		if name == "" {
			t.Fatal("Name() must return a non-empty string")
		}
		if p.Name() != name {
			t.Fatal("Name() must return the same value on every call")
		}
	})

	t.Run("ValidateRequest accepts valid input", func(t *testing.T) {
		req := validRequest(p.Name())
		if err := p.ValidateRequest(context.Background(), req); err != nil {
			t.Fatalf("ValidateRequest returned unexpected error for valid request: %v", err)
		}
	})

	t.Run("ValidateRequest rejects empty RequestID", func(t *testing.T) {
		req := validRequest(p.Name())
		req.RequestID = ""
		if err := p.ValidateRequest(context.Background(), req); err == nil {
			t.Fatal("ValidateRequest must reject a request with empty RequestID")
		}
	})

	t.Run("ValidateRequest rejects empty UserIdentity", func(t *testing.T) {
		req := validRequest(p.Name())
		req.UserIdentity = ""
		if err := p.ValidateRequest(context.Background(), req); err == nil {
			t.Fatal("ValidateRequest must reject a request with empty UserIdentity")
		}
	})

	t.Run("ValidateRequest rejects zero Duration", func(t *testing.T) {
		req := validRequest(p.Name())
		req.Duration = 0
		if err := p.ValidateRequest(context.Background(), req); err == nil {
			t.Fatal("ValidateRequest must reject a request with zero Duration")
		}
	})

	t.Run("Grant returns a valid ElevationGrant", func(t *testing.T) {
		req := validRequest(p.Name())
		grant, err := p.Grant(context.Background(), req)
		if err != nil {
			t.Fatalf("Grant returned unexpected error: %v", err)
		}
		if grant == nil {
			t.Fatal("Grant must not return nil")
		}
		if grant.RequestID != req.RequestID {
			t.Errorf("Grant.RequestID = %q, want %q", grant.RequestID, req.RequestID)
		}
		if grant.ExpiresAt.IsZero() {
			t.Error("Grant.ExpiresAt must not be zero")
		}
		if grant.ExpiresAt.Before(time.Now()) {
			t.Error("Grant.ExpiresAt must be in the future")
		}
	})

	t.Run("Grant is idempotent", func(t *testing.T) {
		req := validRequest(p.Name())
		req.RequestID = "idempotency-test-" + p.Name()

		g1, err := p.Grant(context.Background(), req)
		if err != nil {
			t.Fatalf("First Grant call failed: %v", err)
		}

		g2, err := p.Grant(context.Background(), req)
		if err != nil {
			t.Fatalf("Second Grant call (idempotency check) failed: %v", err)
		}

		if g1.RequestID != g2.RequestID {
			t.Error("Idempotent Grant calls must return consistent RequestIDs")
		}
	})

	t.Run("IsActive returns true for active grant", func(t *testing.T) {
		req := validRequest(p.Name())
		req.RequestID = "isactive-test-" + p.Name()

		grant, err := p.Grant(context.Background(), req)
		if err != nil {
			t.Fatalf("Grant failed: %v", err)
		}

		active, err := p.IsActive(context.Background(), *grant)
		if err != nil {
			t.Fatalf("IsActive returned error: %v", err)
		}
		if !active {
			t.Error("IsActive must return true for a just-granted elevation")
		}
	})

	t.Run("Revoke succeeds for active grant", func(t *testing.T) {
		req := validRequest(p.Name())
		req.RequestID = "revoke-test-" + p.Name()

		grant, err := p.Grant(context.Background(), req)
		if err != nil {
			t.Fatalf("Grant failed: %v", err)
		}

		if err := p.Revoke(context.Background(), *grant); err != nil {
			t.Fatalf("Revoke returned unexpected error: %v", err)
		}
	})

	t.Run("Revoke is idempotent", func(t *testing.T) {
		req := validRequest(p.Name())
		req.RequestID = "revoke-idempotent-test-" + p.Name()

		grant, err := p.Grant(context.Background(), req)
		if err != nil {
			t.Fatalf("Grant failed: %v", err)
		}

		if err := p.Revoke(context.Background(), *grant); err != nil {
			t.Fatalf("First Revoke failed: %v", err)
		}
		if err := p.Revoke(context.Background(), *grant); err != nil {
			t.Fatalf("Second Revoke (idempotency check) returned error: %v", err)
		}
	})

	t.Run("IsActive returns false after revocation", func(t *testing.T) {
		req := validRequest(p.Name())
		req.RequestID = "isactive-after-revoke-" + p.Name()

		grant, err := p.Grant(context.Background(), req)
		if err != nil {
			t.Fatalf("Grant failed: %v", err)
		}

		if err := p.Revoke(context.Background(), *grant); err != nil {
			t.Fatalf("Revoke failed: %v", err)
		}

		active, err := p.IsActive(context.Background(), *grant)
		if err != nil {
			t.Fatalf("IsActive returned error after revocation: %v", err)
		}
		if active {
			t.Error("IsActive must return false after revocation")
		}
	})
}

// validRequest returns a well-formed ElevationRequest for the given provider name.
func validRequest(providerName string) providers.ElevationRequest {
	return providers.ElevationRequest{
		RequestID:     "test-req-001",
		UserIdentity:  "alice@example.com",
		Provider:      providerName,
		RoleName:      "test-role",
		ResourceScope: "test-scope",
		Duration:      1 * time.Hour,
		Reason:        "contract test",
	}
}
