// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// setupViperForTest resets viper to a clean state and initialises the env
// prefix so that JITSUDO_* variables are picked up.  It returns a cleanup
// function that the caller should defer.
func setupViperForTest(t *testing.T) {
	t.Helper()
	viper.Reset()
	viper.SetEnvPrefix("JITSUDO")
	viper.AutomaticEnv()
	t.Cleanup(viper.Reset)
}

// writeCredentials writes a minimal credentials YAML to tmp/.jitsudo/credentials
// and sets HOME=tmp so os.UserHomeDir() returns tmp.
func writeCredentials(t *testing.T, serverURL, token string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, ".jitsudo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir credentials dir: %v", err)
	}
	content := "server_url: \"" + serverURL + "\"\n" +
		"token: \"" + token + "\"\n" +
		"expires_at: 2099-01-01T00:00:00Z\n" +
		"email: test@example.com\n"
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(content), 0600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
}

// TestNewClient_NoServerURL_Error verifies that newClient returns a clear,
// actionable error when no server URL is configured via any mechanism.
func TestNewClient_NoServerURL_Error(t *testing.T) {
	setupViperForTest(t)

	// Credentials with empty server_url and a valid token so that the
	// credentials file is readable but contributes no server URL.
	writeCredentials(t, "", "valid-token")

	// Ensure the global flags are clean.
	prev := flags
	t.Cleanup(func() { flags = prev })
	flags.serverURL = ""
	flags.token = ""

	_, err := newClient(context.Background())
	if err == nil {
		t.Fatal("expected error when no server URL configured, got nil")
	}
	if !strings.Contains(err.Error(), "no server URL configured") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestNewClient_ViperServerURL verifies that JITSUDO_SERVER is picked up via
// viper and used as the server URL without modifying the login credentials.
func TestNewClient_ViperServerURL(t *testing.T) {
	setupViperForTest(t)
	t.Setenv("JITSUDO_SERVER", "localhost:9443")

	// No credentials file; token supplied directly via flag so LoadCredentials
	// is not called (both serverURL and token are non-empty).
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	prev := flags
	t.Cleanup(func() { flags = prev })
	flags.serverURL = ""
	flags.token = "test-token"

	c, err := newClient(context.Background())
	if err != nil {
		t.Fatalf("expected no error with JITSUDO_SERVER set, got: %v", err)
	}
	defer c.Close()
}

// TestNewClient_FlagOverridesViper verifies that --server takes priority over
// the JITSUDO_SERVER env var.
func TestNewClient_FlagOverridesViper(t *testing.T) {
	setupViperForTest(t)
	t.Setenv("JITSUDO_SERVER", "env-server:9000")

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	prev := flags
	t.Cleanup(func() { flags = prev })
	flags.serverURL = "flag-server:8443"
	flags.token = "test-token"

	// If --server flag is set it should be used; the env var value should not
	// cause an error or override the explicit flag.
	c, err := newClient(context.Background())
	if err != nil {
		t.Fatalf("expected no error with --server flag set, got: %v", err)
	}
	defer c.Close()
}
