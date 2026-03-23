// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package client

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

// ── cert generation helper ────────────────────────────────────────────────────

type testCerts struct {
	certPEM  []byte
	certFile string
	keyFile  string
}

func generateSelfSigned(t *testing.T) testCerts {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "jitsudo-client-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
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
	certFile := filepath.Join(dir, "client.crt")
	keyFile := filepath.Join(dir, "client.key")
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	return testCerts{certPEM: certPEM, certFile: certFile, keyFile: keyFile}
}

// ── buildClientTLS ────────────────────────────────────────────────────────────

func TestBuildClientTLS_CAFile(t *testing.T) {
	certs := generateSelfSigned(t)

	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, certs.certPEM, 0600); err != nil {
		t.Fatalf("write ca: %v", err)
	}

	creds, err := buildClientTLS(&TLSConfig{CAFile: caFile})
	if err != nil {
		t.Fatalf("buildClientTLS with CAFile: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
}

func TestBuildClientTLS_mTLS(t *testing.T) {
	certs := generateSelfSigned(t)

	caFile := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caFile, certs.certPEM, 0600); err != nil {
		t.Fatalf("write ca: %v", err)
	}

	creds, err := buildClientTLS(&TLSConfig{
		CAFile:   caFile,
		CertFile: certs.certFile,
		KeyFile:  certs.keyFile,
	})
	if err != nil {
		t.Fatalf("buildClientTLS mTLS: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
}

func TestBuildClientTLS_InsecureSkipVerify(t *testing.T) {
	creds, err := buildClientTLS(&TLSConfig{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("buildClientTLS InsecureSkipVerify: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
}

func TestBuildClientTLS_NoConfig(t *testing.T) {
	// Empty TLSConfig: system cert pool, no client cert.
	creds, err := buildClientTLS(&TLSConfig{})
	if err != nil {
		t.Fatalf("buildClientTLS empty config: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
}

func TestBuildClientTLS_MissingCAFile(t *testing.T) {
	_, err := buildClientTLS(&TLSConfig{CAFile: "/nonexistent/ca.crt"})
	if err == nil {
		t.Error("expected error for missing CA file, got nil")
	}
}

func TestBuildClientTLS_InvalidCAFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := buildClientTLS(&TLSConfig{CAFile: bad})
	if err == nil {
		t.Error("expected error for invalid CA PEM, got nil")
	}
}

func TestBuildClientTLS_MissingKeyFile(t *testing.T) {
	certs := generateSelfSigned(t)
	_, err := buildClientTLS(&TLSConfig{
		CertFile: certs.certFile,
		KeyFile:  "/nonexistent/client.key",
	})
	if err == nil {
		t.Error("expected error for missing key file, got nil")
	}
}

func TestBuildClientTLS_MissingCertFile(t *testing.T) {
	certs := generateSelfSigned(t)
	_, err := buildClientTLS(&TLSConfig{
		CertFile: "/nonexistent/client.crt",
		KeyFile:  certs.keyFile,
	})
	if err == nil {
		t.Error("expected error for missing cert file, got nil")
	}
}
