// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFriendlyError_NonGRPC(t *testing.T) {
	orig := errors.New("plain error")
	got := friendlyError(orig)
	if got != orig {
		t.Errorf("expected same error for non-gRPC error; got %v", got)
	}
}

func TestFriendlyError_NotFound(t *testing.T) {
	err := status.Error(codes.NotFound, "request req_123 not found")
	got := friendlyError(err)
	if got.Error() != "not found" {
		t.Errorf("expected %q; got %q", "not found", got.Error())
	}
}

func TestFriendlyError_StripGRPCWrapper(t *testing.T) {
	err := status.Error(codes.InvalidArgument, "duration must be positive")
	got := friendlyError(err)
	if got.Error() != "duration must be positive" {
		t.Errorf("expected raw message; got %q", got.Error())
	}
}

func TestFriendlyError_StripWorkflowPrefix(t *testing.T) {
	err := status.Error(codes.Internal, "workflow: state machine transition denied")
	got := friendlyError(err)
	if got.Error() != "state machine transition denied" {
		t.Errorf("expected prefix stripped; got %q", got.Error())
	}
}

func TestFriendlyError_StripStorePrefix(t *testing.T) {
	err := status.Error(codes.Internal, "store: database connection failed")
	got := friendlyError(err)
	if got.Error() != "database connection failed" {
		t.Errorf("expected prefix stripped; got %q", got.Error())
	}
}

func TestFriendlyError_ChainedPrefixes(t *testing.T) {
	// Both known prefixes should be stripped when chained (e.g. wrapped errors).
	err := status.Error(codes.Internal, "workflow: store: nested prefix")
	got := friendlyError(err)
	if got.Error() != "nested prefix" {
		t.Errorf("expected both chained prefixes stripped; got %q", got.Error())
	}
}

func TestFriendlyError_UnknownGRPCCode(t *testing.T) {
	err := status.Error(codes.PermissionDenied, "not authorized")
	got := friendlyError(err)
	if got.Error() != "not authorized" {
		t.Errorf("expected message for unknown code; got %q", got.Error())
	}
}
