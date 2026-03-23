// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

// Package version holds the version string injected at build time via ldflags.
package version

// Version is set at build time via: -ldflags "-X github.com/jitsudo-dev/jitsudo/internal/version.Version=..."
var Version = "dev"
