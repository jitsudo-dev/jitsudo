// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"testing"
)

// TestNew_StripHTTPScheme verifies that New accepts full http:// and https://
// URLs and strips the scheme before dialling, avoiding the "too many colons"
// error from grpc.NewClient.
func TestNew_StripHTTPScheme(t *testing.T) {
	cases := []struct {
		name      string
		serverURL string
	}{
		{"http scheme", "http://localhost:8080"},
		{"https scheme", "https://localhost:8443"},
		{"bare host:port", "localhost:8080"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(context.Background(), Config{
				ServerURL: tc.serverURL,
				Token:     "test-token",
				Insecure:  true,
			})
			if err != nil {
				t.Fatalf("New(%q) error: %v", tc.serverURL, err)
			}
			c.Close()
		})
	}
}

func TestNew_EmptyServerURL(t *testing.T) {
	_, err := New(context.Background(), Config{})
	if err == nil {
		t.Fatal("expected error for empty ServerURL, got nil")
	}
}
