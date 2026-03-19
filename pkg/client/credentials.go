package client

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// StoredCredentials is the credential file written by `jitsudo login`.
type StoredCredentials struct {
	ServerURL string    `yaml:"server_url"`
	Token     string    `yaml:"token"`
	ExpiresAt time.Time `yaml:"expires_at"`
	Email     string    `yaml:"email"`
}

// CredentialsPath returns the path to the credentials file (~/.jitsudo/credentials).
func CredentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("client: home dir: %w", err)
	}
	return filepath.Join(home, ".jitsudo", "credentials"), nil
}

// LoadCredentials reads and parses the stored credentials file.
func LoadCredentials() (*StoredCredentials, error) {
	path, err := CredentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("client: read credentials: %w (run 'jitsudo login' first)", err)
	}
	var creds StoredCredentials
	if err := yaml.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("client: parse credentials: %w", err)
	}
	return &creds, nil
}

// SaveCredentials writes credentials to ~/.jitsudo/credentials (mode 0600).
func SaveCredentials(c *StoredCredentials) error {
	path, err := CredentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("client: mkdir credentials dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("client: marshal credentials: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("client: write credentials: %w", err)
	}
	return nil
}
