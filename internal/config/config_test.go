// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jitsudo-dev/jitsudo/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load(\"\") error: %v", err)
	}
	if cfg.Server.HTTPAddr != ":8080" {
		t.Errorf("default HTTPAddr = %q, want :8080", cfg.Server.HTTPAddr)
	}
	if cfg.Server.GRPCAddr != ":8443" {
		t.Errorf("default GRPCAddr = %q, want :8443", cfg.Server.GRPCAddr)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("default log level = %q, want info", cfg.Log.Level)
	}
}

func TestLoad_YAMLOverridesDefaults(t *testing.T) {
	// Clear every env var that applyEnv() reads so the CI integration
	// environment cannot override the YAML values this test is verifying.
	for _, key := range []string{
		"JITSUDOD_HTTP_ADDR", "JITSUDOD_GRPC_ADDR",
		"JITSUDOD_DATABASE_URL",
		"JITSUDOD_OIDC_ISSUER", "JITSUDOD_OIDC_DISCOVERY_URL", "JITSUDOD_OIDC_CLIENT_ID",
		"JITSUDOD_TLS_CERT_FILE", "JITSUDOD_TLS_KEY_FILE", "JITSUDOD_TLS_CA_FILE",
		"JITSUDOD_LOG_LEVEL",
		"JITSUDOD_SLACK_WEBHOOK_URL", "JITSUDOD_SMTP_HOST", "JITSUDOD_SMTP_PASSWORD",
	} {
		t.Setenv(key, "")
	}

	yaml := `
server:
  http_addr: ":9090"
  grpc_addr: ":9443"
database:
  url: "postgres://user:pass@db:5432/mydb?sslmode=require"
auth:
  oidc_issuer: "https://idp.example.com"
  client_id: "my-server"
`
	path := writeTemp(t, yaml)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Server.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want :9090", cfg.Server.HTTPAddr)
	}
	if cfg.Database.URL != "postgres://user:pass@db:5432/mydb?sslmode=require" {
		t.Errorf("Database.URL = %q", cfg.Database.URL)
	}
	if cfg.Auth.OIDCIssuer != "https://idp.example.com" {
		t.Errorf("Auth.OIDCIssuer = %q", cfg.Auth.OIDCIssuer)
	}
}

func TestLoad_EnvVarsOverrideYAML(t *testing.T) {
	yaml := `
database:
  url: "postgres://from-yaml"
server:
  http_addr: ":7070"
`
	path := writeTemp(t, yaml)

	t.Setenv("JITSUDOD_DATABASE_URL", "postgres://from-env")
	t.Setenv("JITSUDOD_HTTP_ADDR", ":6060")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Database.URL != "postgres://from-env" {
		t.Errorf("Database.URL = %q, want postgres://from-env", cfg.Database.URL)
	}
	if cfg.Server.HTTPAddr != ":6060" {
		t.Errorf("HTTPAddr = %q, want :6060", cfg.Server.HTTPAddr)
	}
}

func TestLoad_TLSFields(t *testing.T) {
	yaml := `
tls:
  cert_file: "/etc/jitsudo/tls.crt"
  key_file: "/etc/jitsudo/tls.key"
  ca_file: "/etc/jitsudo/ca.crt"
`
	path := writeTemp(t, yaml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.TLS.CertFile != "/etc/jitsudo/tls.crt" {
		t.Errorf("TLS.CertFile = %q", cfg.TLS.CertFile)
	}
	if cfg.TLS.CAFile != "/etc/jitsudo/ca.crt" {
		t.Errorf("TLS.CAFile = %q", cfg.TLS.CAFile)
	}
}

func TestLoad_ProviderConfig(t *testing.T) {
	yaml := `
providers:
  aws:
    mode: "sts_assume_role"
    region: "eu-west-1"
    role_arn_template: "arn:aws:iam::{scope}:role/jitsudo-{role}"
    max_duration: "4h"
  kubernetes:
    default_namespace: "platform"
    max_duration: "1h"
`
	path := writeTemp(t, yaml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Providers.AWS == nil {
		t.Fatal("Providers.AWS is nil")
	}
	if cfg.Providers.AWS.Region != "eu-west-1" {
		t.Errorf("AWS.Region = %q, want eu-west-1", cfg.Providers.AWS.Region)
	}
	if cfg.Providers.Kubernetes == nil {
		t.Fatal("Providers.Kubernetes is nil")
	}
	if cfg.Providers.Kubernetes.DefaultNamespace != "platform" {
		t.Errorf("Kubernetes.DefaultNamespace = %q", cfg.Providers.Kubernetes.DefaultNamespace)
	}
	if cfg.Providers.GCP != nil {
		t.Error("Providers.GCP should be nil when not configured")
	}
}

func TestLoad_SlackEnvVar(t *testing.T) {
	t.Setenv("JITSUDOD_SLACK_WEBHOOK_URL", "https://hooks.slack.com/test")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Notifications.Slack == nil {
		t.Fatal("Notifications.Slack is nil")
	}
	if cfg.Notifications.Slack.WebhookURL != "https://hooks.slack.com/test" {
		t.Errorf("Slack.WebhookURL = %q", cfg.Notifications.Slack.WebhookURL)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoad_OIDCDiscoveryURL_YAML(t *testing.T) {
	t.Setenv("JITSUDOD_OIDC_DISCOVERY_URL", "") // ensure env doesn't interfere
	yaml := `
auth:
  oidc_issuer: "http://localhost:5556/dex"
  oidc_discovery_url: "http://dex:5556/dex"
  client_id: "jitsudo-cli"
`
	path := writeTemp(t, yaml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Auth.OIDCDiscoveryURL != "http://dex:5556/dex" {
		t.Errorf("Auth.OIDCDiscoveryURL = %q, want http://dex:5556/dex", cfg.Auth.OIDCDiscoveryURL)
	}
	if cfg.Auth.OIDCIssuer != "http://localhost:5556/dex" {
		t.Errorf("Auth.OIDCIssuer = %q, want http://localhost:5556/dex", cfg.Auth.OIDCIssuer)
	}
}

func TestLoad_OIDCDiscoveryURL_EnvVar(t *testing.T) {
	t.Setenv("JITSUDOD_OIDC_DISCOVERY_URL", "http://dex:5556/dex")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Auth.OIDCDiscoveryURL != "http://dex:5556/dex" {
		t.Errorf("Auth.OIDCDiscoveryURL = %q, want http://dex:5556/dex", cfg.Auth.OIDCDiscoveryURL)
	}
}

func TestLoad_OIDCDiscoveryURL_EnvOverridesYAML(t *testing.T) {
	yaml := `
auth:
  oidc_discovery_url: "http://from-yaml:5556/dex"
`
	path := writeTemp(t, yaml)
	t.Setenv("JITSUDOD_OIDC_DISCOVERY_URL", "http://from-env:5556/dex")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Auth.OIDCDiscoveryURL != "http://from-env:5556/dex" {
		t.Errorf("Auth.OIDCDiscoveryURL = %q, want http://from-env:5556/dex", cfg.Auth.OIDCDiscoveryURL)
	}
}

func TestLoad_OIDCDiscoveryURL_DefaultEmpty(t *testing.T) {
	t.Setenv("JITSUDOD_OIDC_DISCOVERY_URL", "")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Auth.OIDCDiscoveryURL != "" {
		t.Errorf("Auth.OIDCDiscoveryURL default = %q, want empty string", cfg.Auth.OIDCDiscoveryURL)
	}
}

func TestLoad_MCPConfig(t *testing.T) {
	yaml := `
mcp:
  token: "supersecrettoken"
  agent_identity: "claude-approver-prod"
`
	path := writeTemp(t, yaml)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.MCP.Token != "supersecrettoken" {
		t.Errorf("MCP.Token = %q, want %q", cfg.MCP.Token, "supersecrettoken")
	}
	if cfg.MCP.AgentIdentity != "claude-approver-prod" {
		t.Errorf("MCP.AgentIdentity = %q, want %q", cfg.MCP.AgentIdentity, "claude-approver-prod")
	}
}

func TestLoad_MCPToken_EnvVar(t *testing.T) {
	t.Setenv("JITSUDOD_MCP_TOKEN", "token-from-env")
	t.Setenv("JITSUDOD_MCP_AGENT_IDENTITY", "agent-from-env")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.MCP.Token != "token-from-env" {
		t.Errorf("MCP.Token = %q, want %q", cfg.MCP.Token, "token-from-env")
	}
	if cfg.MCP.AgentIdentity != "agent-from-env" {
		t.Errorf("MCP.AgentIdentity = %q, want %q", cfg.MCP.AgentIdentity, "agent-from-env")
	}
}

// TestLoad_MCPToken_EnvOverridesYAML verifies env var wins over YAML value.
func TestLoad_MCPToken_EnvOverridesYAML(t *testing.T) {
	yaml := `
mcp:
  token: "yaml-token"
  agent_identity: "yaml-agent"
`
	path := writeTemp(t, yaml)
	t.Setenv("JITSUDOD_MCP_TOKEN", "env-token")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.MCP.Token != "env-token" {
		t.Errorf("MCP.Token = %q, want %q (env should override YAML)", cfg.MCP.Token, "env-token")
	}
	// AgentIdentity not overridden by env — should keep YAML value.
	if cfg.MCP.AgentIdentity != "yaml-agent" {
		t.Errorf("MCP.AgentIdentity = %q, want %q", cfg.MCP.AgentIdentity, "yaml-agent")
	}
}

// writeTemp writes content to a temp file and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writeTemp: %v", err)
	}
	return path
}
