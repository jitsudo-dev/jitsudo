// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

// Package mock provides a mock implementation of the providers.Provider interface
// for use in unit tests and local development. The mock does not make any
// real cloud API calls; it stores state in memory.
package mock

import (
	"context"
	"sync"
	"time"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
)

// Provider is the mock Provider implementation.
type Provider struct {
	mu     sync.Mutex
	grants map[string]*providers.ElevationGrant // keyed by RequestID
}

// New returns a new mock Provider instance.
func New() *Provider {
	return &Provider{
		grants: make(map[string]*providers.ElevationGrant),
	}
}

// Name returns the canonical provider name.
func (p *Provider) Name() string {
	return "mock"
}

// ValidateRequest performs basic validation without any external calls.
func (p *Provider) ValidateRequest(_ context.Context, req providers.ElevationRequest) error {
	return providers.ValidateCommon(req)
}

// Grant creates an in-memory grant. Idempotent: returns existing grant if already issued.
func (p *Provider) Grant(_ context.Context, req providers.ElevationRequest) (*providers.ElevationGrant, error) {
	if err := p.ValidateRequest(context.Background(), req); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, ok := p.grants[req.RequestID]; ok {
		return existing, nil
	}

	now := time.Now().UTC()
	grant := &providers.ElevationGrant{
		RequestID: req.RequestID,
		Credentials: map[string]string{
			"MOCK_ACCESS_KEY":    "mock-access-key-" + req.RequestID,
			"MOCK_SESSION_TOKEN": "mock-session-token-" + req.RequestID,
		},
		IssuedAt:    now,
		ExpiresAt:   now.Add(req.Duration),
		RevokeToken: "mock-revoke-token-" + req.RequestID,
	}
	p.grants[req.RequestID] = grant
	return grant, nil
}

// Revoke removes the in-memory grant. Idempotent: no error if already revoked.
func (p *Provider) Revoke(_ context.Context, grant providers.ElevationGrant) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.grants, grant.RequestID)
	return nil
}

// IsActive returns true if the grant exists in memory and has not expired.
func (p *Provider) IsActive(_ context.Context, grant providers.ElevationGrant) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	stored, ok := p.grants[grant.RequestID]
	if !ok {
		return false, nil
	}
	return time.Now().UTC().Before(stored.ExpiresAt), nil
}
