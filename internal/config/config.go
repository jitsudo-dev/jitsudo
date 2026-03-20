// Package config loads jitsudod configuration from an optional YAML file and
// overlays JITSUDOD_* environment variables on top. Environment variables
// always win, preserving twelve-factor and Kubernetes Secret compatibility.
//
// License: Elastic License 2.0 (ELv2)
package config

import (
	"fmt"
	"os"

	awsprovider "github.com/jitsudo-dev/jitsudo/internal/providers/aws"
	azureprovider "github.com/jitsudo-dev/jitsudo/internal/providers/azure"
	gcpprovider "github.com/jitsudo-dev/jitsudo/internal/providers/gcp"
	k8sprovider "github.com/jitsudo-dev/jitsudo/internal/providers/kubernetes"
	"github.com/jitsudo-dev/jitsudo/internal/server/notifications"
	"gopkg.in/yaml.v3"
)

// FileConfig is the canonical on-disk configuration schema for jitsudod.
// All fields map 1-to-1 to the YAML keys shown in deploy/config/config.example.yaml.
type FileConfig struct {
	Server        ServerCfg        `yaml:"server"`
	Database      DatabaseCfg      `yaml:"database"`
	Auth          AuthCfg          `yaml:"auth"`
	TLS           TLSCfg           `yaml:"tls"`
	Providers     ProvidersCfg     `yaml:"providers"`
	Notifications NotificationsCfg `yaml:"notifications"`
	Log           LogCfg           `yaml:"log"`
}

// ServerCfg holds network listener addresses.
type ServerCfg struct {
	HTTPAddr string `yaml:"http_addr"`
	GRPCAddr string `yaml:"grpc_addr"`
}

// DatabaseCfg holds the PostgreSQL connection DSN.
type DatabaseCfg struct {
	URL string `yaml:"url"`
}

// AuthCfg holds OIDC settings for verifying inbound JWTs.
type AuthCfg struct {
	OIDCIssuer string `yaml:"oidc_issuer"`
	ClientID   string `yaml:"client_id"`
}

// TLSCfg holds paths to TLS credentials for the gRPC listener.
// All three fields must be set to enable mTLS. CertFile + KeyFile alone
// enable server-only TLS. Leaving all empty keeps the listener insecure
// (local development default).
type TLSCfg struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
}

// ProvidersCfg holds optional cloud provider configurations.
// A nil pointer means the provider is not registered at startup.
type ProvidersCfg struct {
	AWS        *awsprovider.Config   `yaml:"aws"`
	GCP        *gcpprovider.Config   `yaml:"gcp"`
	Azure      *azureprovider.Config `yaml:"azure"`
	Kubernetes *k8sprovider.Config   `yaml:"kubernetes"`
}

// NotificationsCfg holds optional notification channel configurations.
type NotificationsCfg struct {
	Slack *notifications.SlackConfig `yaml:"slack"`
	SMTP  *notifications.SMTPConfig  `yaml:"smtp"`
}

// LogCfg controls log level and output format.
type LogCfg struct {
	// Level is the minimum log level: "debug", "info", "warn", "error".
	Level string `yaml:"level"`
	// Format is the output format: "json" (default, structured) or "text" (human-readable).
	Format string `yaml:"format"`
}

// Load reads an optional YAML config file and overlays JITSUDOD_* environment
// variables. If path is empty only environment variables and compiled-in
// defaults are used. Environment variables always take precedence.
func Load(path string) (*FileConfig, error) {
	cfg := defaults()

	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("config: open %q: %w", path, err)
		}
		defer f.Close()
		if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
			return nil, fmt.Errorf("config: parse %q: %w", path, err)
		}
	}

	applyEnv(cfg)
	return cfg, nil
}

// defaults returns a FileConfig populated with safe compiled-in defaults.
// These match the existing docker-compose.yaml / local-dev values so that
// upgrading from env-var-only mode requires no config file changes.
func defaults() *FileConfig {
	return &FileConfig{
		Server: ServerCfg{
			HTTPAddr: ":8080",
			GRPCAddr: ":8443",
		},
		Database: DatabaseCfg{
			URL: "postgres://jitsudo:jitsudo@localhost:5432/jitsudo?sslmode=disable",
		},
		Auth: AuthCfg{
			OIDCIssuer: "http://localhost:5556/dex",
			ClientID:   "jitsudo-cli",
		},
		Log: LogCfg{
			Level:  "info",
			Format: "json",
		},
	}
}

// applyEnv overlays JITSUDOD_* environment variables onto cfg.
// Any non-empty env var wins over the YAML value.
func applyEnv(cfg *FileConfig) {
	setIfEnv(&cfg.Server.HTTPAddr, "JITSUDOD_HTTP_ADDR")
	setIfEnv(&cfg.Server.GRPCAddr, "JITSUDOD_GRPC_ADDR")
	setIfEnv(&cfg.Database.URL, "JITSUDOD_DATABASE_URL")
	setIfEnv(&cfg.Auth.OIDCIssuer, "JITSUDOD_OIDC_ISSUER")
	setIfEnv(&cfg.Auth.ClientID, "JITSUDOD_OIDC_CLIENT_ID")
	setIfEnv(&cfg.TLS.CertFile, "JITSUDOD_TLS_CERT_FILE")
	setIfEnv(&cfg.TLS.KeyFile, "JITSUDOD_TLS_KEY_FILE")
	setIfEnv(&cfg.TLS.CAFile, "JITSUDOD_TLS_CA_FILE")
	setIfEnv(&cfg.Log.Level, "JITSUDOD_LOG_LEVEL")

	// Notification env vars allow sensitive values (webhook URLs, passwords)
	// to come from Kubernetes Secrets without appearing in the config file.
	if v := os.Getenv("JITSUDOD_SLACK_WEBHOOK_URL"); v != "" {
		if cfg.Notifications.Slack == nil {
			cfg.Notifications.Slack = &notifications.SlackConfig{}
		}
		cfg.Notifications.Slack.WebhookURL = v
	}
	if v := os.Getenv("JITSUDOD_SMTP_HOST"); v != "" {
		if cfg.Notifications.SMTP == nil {
			cfg.Notifications.SMTP = &notifications.SMTPConfig{}
		}
		cfg.Notifications.SMTP.Host = v
	}
	if v := os.Getenv("JITSUDOD_SMTP_PASSWORD"); v != "" {
		if cfg.Notifications.SMTP == nil {
			cfg.Notifications.SMTP = &notifications.SMTPConfig{}
		}
		cfg.Notifications.SMTP.Password = v
	}
}

func setIfEnv(field *string, key string) {
	if v := os.Getenv(key); v != "" {
		*field = v
	}
}
