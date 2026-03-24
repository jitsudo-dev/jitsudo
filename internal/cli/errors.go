// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// internalPrefixes are package-path-style prefixes that appear in server error
// messages but are implementation details not useful to end users.
var internalPrefixes = []string{
	"workflow: ",
	"store: ",
}

// friendlyError translates a gRPC status error into a user-facing error by
// stripping the wire-level "rpc error: code = X desc = " wrapper and removing
// known internal package-name prefixes from the description.
//
// Non-gRPC errors are returned unchanged.
func friendlyError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	if st.Code() == codes.NotFound {
		return errors.New("not found")
	}
	msg := st.Message()
	// Strip any leading internal-package prefixes (e.g. "workflow: ", "store: ").
	for _, prefix := range internalPrefixes {
		msg = strings.TrimPrefix(msg, prefix)
	}
	return errors.New(msg)
}
