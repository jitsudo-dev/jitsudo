// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"time"

	oidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/jitsudo-dev/jitsudo/pkg/client"
)

const oidcClientID = "jitsudo-cli"

func newLoginCmd() *cobra.Command {
	var issuer string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with the configured IdP via OIDC device flow",
		Long: `Authenticate with your identity provider using the OIDC Device Authorization Flow (RFC 8628).

This flow works without a browser redirect, making it suitable for headless terminals and SSH sessions.
Upon success, credentials are stored at ~/.jitsudo/credentials.`,
		Example: `  jitsudo login --provider http://localhost:5556/dex
  jitsudo login --provider https://your-idp.okta.com --server https://jitsudo.example.com:8443`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// OIDC discovery.
			provider, err := oidc.NewProvider(ctx, issuer)
			if err != nil {
				return fmt.Errorf("OIDC discovery at %q: %w", issuer, err)
			}

			oauthCfg := &oauth2.Config{
				ClientID: oidcClientID,
				Endpoint: provider.Endpoint(),
				Scopes:   []string{oidc.ScopeOpenID, "email", "profile", "groups", "offline_access"},
			}

			// Step 1: request device code.
			devResp, err := oauthCfg.DeviceAuth(ctx)
			if err != nil {
				return fmt.Errorf("device authorization request: %w", err)
			}

			out := cmd.OutOrStdout()
			if devResp.VerificationURIComplete != "" {
				fmt.Fprintf(out, "Open this URL to authenticate:\n  %s\n\n", devResp.VerificationURIComplete)
			} else {
				fmt.Fprintf(out, "Visit: %s\n", devResp.VerificationURI)
				fmt.Fprintf(out, "Code:  %s\n\n", devResp.UserCode)
			}
			fmt.Fprintln(out, "Waiting for authorization...")

			// Step 2: poll until the user authorizes.
			token, err := oauthCfg.DeviceAccessToken(ctx, devResp)
			if err != nil {
				return fmt.Errorf("device access token: %w", err)
			}

			rawIDToken, ok := token.Extra("id_token").(string)
			if !ok {
				return fmt.Errorf("OIDC: no id_token in token response")
			}

			verifier := provider.Verifier(&oidc.Config{ClientID: oidcClientID})
			idToken, err := verifier.Verify(ctx, rawIDToken)
			if err != nil {
				return fmt.Errorf("OIDC: id_token verification: %w", err)
			}

			var claims struct {
				Email string `json:"email"`
			}
			_ = idToken.Claims(&claims)

			expiry := token.Expiry
			if expiry.IsZero() {
				expiry = time.Now().Add(time.Hour)
			}

			serverURL := flags.serverURL
			if err := client.SaveCredentials(&client.StoredCredentials{
				ServerURL: serverURL,
				Token:     rawIDToken,
				ExpiresAt: expiry,
				Email:     claims.Email,
			}); err != nil {
				return fmt.Errorf("save credentials: %w", err)
			}

			if !flags.quiet {
				fmt.Fprintf(out, "Logged in as %s\n", claims.Email)
				if serverURL != "" {
					fmt.Fprintf(out, "Server:     %s\n", serverURL)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&issuer, "provider", "", "OIDC issuer URL (required)")
	_ = cmd.MarkFlagRequired("provider")

	return cmd
}
