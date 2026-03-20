// License: Elastic License 2.0 (ELv2)
package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ── cert generation helpers ───────────────────────────────────────────────────

// tlsTestCerts holds the PEM-encoded cert and key for a self-signed test cert.
type tlsTestCerts struct {
	certPEM  []byte
	keyPEM   []byte
	certFile string
	keyFile  string
}

// generateSelfSigned creates a self-signed ECDSA certificate and writes it
// to temp files, returning their paths alongside the raw PEM bytes.
func generateSelfSigned(t *testing.T) tlsTestCerts {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "jitsudo-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	return tlsTestCerts{certPEM: certPEM, keyPEM: keyPEM, certFile: certFile, keyFile: keyFile}
}

// ── buildServerTLS ────────────────────────────────────────────────────────────

func TestBuildServerTLS_ServerOnlyTLS(t *testing.T) {
	certs := generateSelfSigned(t)

	cfg := TLSConfig{
		CertFile: certs.certFile,
		KeyFile:  certs.keyFile,
	}
	creds, err := buildServerTLS(cfg)
	if err != nil {
		t.Fatalf("buildServerTLS: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}

	// Verify TLS mode is server-only (no client auth required).
	info := creds.Info()
	if info.SecurityProtocol != "tls" {
		t.Errorf("SecurityProtocol = %q, want tls", info.SecurityProtocol)
	}
}

func TestBuildServerTLS_mTLS(t *testing.T) {
	certs := generateSelfSigned(t)

	// Write the cert as the CA file too (self-signed acts as its own CA).
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, certs.certPEM, 0600); err != nil {
		t.Fatalf("write ca: %v", err)
	}

	cfg := TLSConfig{
		CertFile: certs.certFile,
		KeyFile:  certs.keyFile,
		CAFile:   caFile,
	}
	creds, err := buildServerTLS(cfg)
	if err != nil {
		t.Fatalf("buildServerTLS mTLS: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
}

func TestBuildServerTLS_MissingCertFile(t *testing.T) {
	certs := generateSelfSigned(t)
	cfg := TLSConfig{
		CertFile: "/nonexistent/tls.crt",
		KeyFile:  certs.keyFile,
	}
	_, err := buildServerTLS(cfg)
	if err == nil {
		t.Error("expected error for missing cert file, got nil")
	}
}

func TestBuildServerTLS_MissingKeyFile(t *testing.T) {
	certs := generateSelfSigned(t)
	cfg := TLSConfig{
		CertFile: certs.certFile,
		KeyFile:  "/nonexistent/tls.key",
	}
	_, err := buildServerTLS(cfg)
	if err == nil {
		t.Error("expected error for missing key file, got nil")
	}
}

func TestBuildServerTLS_InvalidCAFile(t *testing.T) {
	certs := generateSelfSigned(t)

	// Write garbage as the CA file.
	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0600); err != nil {
		t.Fatalf("write bad ca: %v", err)
	}

	cfg := TLSConfig{
		CertFile: certs.certFile,
		KeyFile:  certs.keyFile,
		CAFile:   caFile,
	}
	_, err := buildServerTLS(cfg)
	if err == nil {
		t.Error("expected error for invalid CA PEM, got nil")
	}
}

func TestBuildServerTLS_MissingCAFile(t *testing.T) {
	certs := generateSelfSigned(t)
	cfg := TLSConfig{
		CertFile: certs.certFile,
		KeyFile:  certs.keyFile,
		CAFile:   "/nonexistent/ca.crt",
	}
	_, err := buildServerTLS(cfg)
	if err == nil {
		t.Error("expected error for missing CA file, got nil")
	}
}

// ── buildGatewayTLS ───────────────────────────────────────────────────────────

func TestBuildGatewayTLS_ValidCert(t *testing.T) {
	certs := generateSelfSigned(t)

	cfg := TLSConfig{CertFile: certs.certFile}
	creds, err := buildGatewayTLS(cfg)
	if err != nil {
		t.Fatalf("buildGatewayTLS: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
}

func TestBuildGatewayTLS_MissingCertFile(t *testing.T) {
	cfg := TLSConfig{CertFile: "/nonexistent/tls.crt"}
	_, err := buildGatewayTLS(cfg)
	if err == nil {
		t.Error("expected error for missing cert file, got nil")
	}
}

func TestBuildGatewayTLS_InvalidPEM(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.crt")
	if err := os.WriteFile(bad, []byte("not a PEM block"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := TLSConfig{CertFile: bad}
	_, err := buildGatewayTLS(cfg)
	if err == nil {
		t.Error("expected error for invalid PEM, got nil")
	}
}
