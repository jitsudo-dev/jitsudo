//go:build integration

// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

// Package testutil provides shared helpers for integration tests.
// All functions in this package require external services (postgres, dex) to be running.
//
// Prerequisites:
//
//	make dev-deps
//	export JITSUDOD_DATABASE_URL=postgres://jitsudo:jitsudo@localhost:5432/jitsudo?sslmode=disable
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"testing"

	"github.com/jitsudo-dev/jitsudo/pkg/client"
)

// MustGetEnv returns the value of key, or fallback if the env var is not set.
func MustGetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// MustGetDBURL reads JITSUDOD_DATABASE_URL. Calls t.Skip if not set.
func MustGetDBURL(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv("JITSUDOD_DATABASE_URL")
	if dsn == "" {
		t.Skip("JITSUDOD_DATABASE_URL not set — skipping integration test")
	}
	return dsn
}

// MustGetOIDCIssuer reads JITSUDOD_OIDC_ISSUER, defaulting to the local Dex URL.
func MustGetOIDCIssuer(t testing.TB) string {
	t.Helper()
	return MustGetEnv("JITSUDOD_OIDC_ISSUER", "http://localhost:5556/dex")
}

// tokenRedirectURI is the local callback URI registered in dex-config.yaml for headless testing.
// It does not need to be a real server — we only capture the redirect URL before following it.
const tokenRedirectURI = "http://localhost:9999/callback"

// FetchToken obtains an OIDC id_token from Dex by automating the OAuth2
// authorization code flow headlessly (no browser required). It:
//
//  1. Starts an auth code request at issuer/auth
//  2. Follows the redirect to the dex local password login form
//  3. POSTs the credentials to obtain an authorization code
//  4. Exchanges the code for a token at issuer/token
//
// The client (jitsudo-cli) must have http://localhost:9999/callback in its
// redirectURIs in dex-config.yaml for this to work.
func FetchToken(issuer, username, password string) (string, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Step 1: Request authorization code, follow redirects to login page.
	state := fmt.Sprintf("teststate%d", rand.Int63())
	authURL := issuer + "/auth?" + url.Values{
		"response_type": {"code"},
		"client_id":     {"jitsudo-cli"},
		"redirect_uri":  {tokenRedirectURI},
		"scope":         {"openid email profile offline_access"},
		"state":         {state},
	}.Encode()

	resp, err := client.Get(authURL)
	if err != nil {
		return "", fmt.Errorf("FetchToken: auth request for %s: %w", username, err)
	}
	defer resp.Body.Close()
	loginHTML, _ := io.ReadAll(resp.Body)
	loginURL := resp.Request.URL.String()

	// Extract the dex state from the login form URL (differs from outer state).
	dexStateMatch := regexp.MustCompile(`state=([a-z0-9]+)`).FindStringSubmatch(loginURL)
	if dexStateMatch == nil {
		return "", fmt.Errorf("FetchToken: could not find dex state in login URL %q for %s", loginURL, username)
	}
	_ = loginHTML // already have what we need from the URL

	// Step 2: POST credentials. Dex redirects to our callback URI with the code.
	// We don't follow the redirect (our callback isn't a real server) — we
	// capture the Location header instead.
	loginPostURL := issuer + "/auth/local/login?back=&state=" + dexStateMatch[1]
	noRedirClient := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // stop at first redirect
		},
	}
	loginResp, err := noRedirClient.PostForm(loginPostURL, url.Values{
		"login":    {username},
		"password": {password},
	})
	if err != nil && loginResp == nil {
		return "", fmt.Errorf("FetchToken: login POST for %s: %w", username, err)
	}
	defer loginResp.Body.Close()

	// The redirect Location contains the auth code.
	location := loginResp.Header.Get("Location")
	if location == "" {
		body, _ := io.ReadAll(loginResp.Body)
		return "", fmt.Errorf("FetchToken: no redirect after login for %s (status %d): %s", username, loginResp.StatusCode, body)
	}

	codeMatch := regexp.MustCompile(`[?&]code=([^&]+)`).FindStringSubmatch(location)
	if codeMatch == nil {
		return "", fmt.Errorf("FetchToken: no code in redirect %q for %s", location, username)
	}
	code, _ := url.QueryUnescape(codeMatch[1])

	// Step 3: Exchange the authorization code for tokens.
	tokenResp, err := client.PostForm(issuer+"/token", url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {tokenRedirectURI},
		"client_id":    {"jitsudo-cli"},
	})
	if err != nil {
		return "", fmt.Errorf("FetchToken: token exchange for %s: %w", username, err)
	}
	defer tokenResp.Body.Close()
	tokenBody, _ := io.ReadAll(tokenResp.Body)
	if tokenResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("FetchToken: token endpoint returned %d for %s: %s", tokenResp.StatusCode, username, tokenBody)
	}

	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(tokenBody, &tok); err != nil {
		return "", fmt.Errorf("FetchToken: unmarshal token for %s: %w", username, err)
	}
	if tok.IDToken == "" {
		return "", fmt.Errorf("FetchToken: empty id_token for %s; response: %s", username, tokenBody)
	}
	return tok.IDToken, nil
}

// MustFetchToken is the testing.TB wrapper around FetchToken.
func MustFetchToken(t testing.TB, issuer, username, password string) string {
	t.Helper()
	tok, err := FetchToken(issuer, username, password)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return tok
}

// GetFreeAddrDirect returns a free TCP address on 127.0.0.1 by temporarily
// binding a listener. Panics on error. Useful in TestMain where no testing.TB
// is available.
func GetFreeAddrDirect() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(fmt.Sprintf("GetFreeAddrDirect: %v", err))
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// GetFreeAddr returns a free TCP address on 127.0.0.1 by temporarily binding
// a listener. There is a small TOCTOU window between Close and the caller
// binding the port, which is acceptable in test environments.
func GetFreeAddr(t testing.TB) string {
	t.Helper()
	addr := GetFreeAddrDirect()
	return addr
}

// MustNewClient creates an authenticated gRPC client. The client is closed via t.Cleanup.
func MustNewClient(t testing.TB, grpcAddr, token string) *client.Client {
	t.Helper()
	c, err := client.New(context.Background(), client.Config{
		ServerURL: grpcAddr,
		Token:     token,
		Insecure:  true,
	})
	if err != nil {
		t.Fatalf("MustNewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
