// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCmd_MissingDBURL(t *testing.T) {
	t.Setenv("JITSUDOD_DATABASE_URL", "")
	cmd := newInitCmd()
	cmd.SetArgs([]string{
		"--oidc-issuer", "https://idp.example.com",
		"--oidc-client-id", "jitsudo-server",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--db-url or JITSUDOD_DATABASE_URL is required") {
		t.Errorf("expected --db-url error, got: %v", err)
	}
}

func TestInitCmd_MissingOIDCIssuer(t *testing.T) {
	cmd := newInitCmd()
	cmd.SetArgs([]string{
		"--db-url", "postgres://localhost/jitsudo",
		"--oidc-client-id", "jitsudo-server",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--oidc-issuer is required") {
		t.Errorf("expected --oidc-issuer error, got: %v", err)
	}
}

func TestInitCmd_MissingClientID(t *testing.T) {
	cmd := newInitCmd()
	cmd.SetArgs([]string{
		"--db-url", "postgres://localhost/jitsudo",
		"--oidc-issuer", "https://idp.example.com",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--oidc-client-id is required") {
		t.Errorf("expected --oidc-client-id error, got: %v", err)
	}
}

func TestInitCmd_WritesConfigFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "jitsudo.yaml")

	cmd := newInitCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{
		"--db-url", "postgres://localhost:5432/jitsudo",
		"--oidc-issuer", "https://idp.example.com",
		"--oidc-client-id", "jitsudo-server",
		"--config-out", configPath,
	})

	err := cmd.Execute()
	// DB will be unreachable in unit test environment — that's expected.
	// We verify the failure is about the DB, not about flags or config marshaling.
	if err != nil {
		if !strings.Contains(err.Error(), "database connectivity check failed") &&
			!strings.Contains(err.Error(), "connection refused") &&
			!strings.Contains(err.Error(), "no such host") {
			t.Errorf("unexpected error (not a DB error): %v", err)
		}
		// Config file should NOT exist since we never got to write it.
		if _, statErr := os.Stat(configPath); statErr == nil {
			t.Error("config file should not exist when DB check fails")
		}
		return
	}

	// If somehow a DB was reachable, validate the written config.
	b, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("config file not written: %v", readErr)
	}
	if !strings.Contains(string(b), "https://idp.example.com") {
		t.Errorf("config file missing oidc_issuer:\n%s", string(b))
	}
	if !strings.Contains(string(b), "jitsudo-server") {
		t.Errorf("config file missing client_id:\n%s", string(b))
	}
}

func TestInitCmd_FlagDefaults(t *testing.T) {
	cmd := newInitCmd()
	for flagName, wantDefault := range map[string]string{
		"http-addr":       ":8080",
		"grpc-addr":       ":8443",
		"config-out":      "jitsudo.yaml",
		"skip-migrations": "false",
	} {
		f := cmd.Flags().Lookup(flagName)
		if f == nil {
			t.Errorf("flag --%s not defined", flagName)
			continue
		}
		if f.DefValue != wantDefault {
			t.Errorf("--%s default = %q, want %q", flagName, f.DefValue, wantDefault)
		}
	}
}
