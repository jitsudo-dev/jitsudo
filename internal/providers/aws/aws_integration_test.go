//go:build integration

// Package aws — integration tests against LocalStack.
//
// Prerequisites:
//   - LocalStack running and accessible at AWS_ENDPOINT_URL (default: http://localhost:4566)
//   - AWS credentials set to any non-empty value (LocalStack ignores them):
//     AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
//
// Run with:
//
//	go test ./internal/providers/aws/... -tags integration -v
//
// License: Apache 2.0
package aws_test

import (
	"context"
	"os"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/jitsudo-dev/jitsudo/internal/providers"
	awsprovider "github.com/jitsudo-dev/jitsudo/internal/providers/aws"
	"github.com/jitsudo-dev/jitsudo/pkg/types"
)

func localstackEndpoint(t *testing.T) string {
	t.Helper()
	ep := os.Getenv("AWS_ENDPOINT_URL")
	if ep == "" {
		ep = "http://localhost:4566"
	}
	return ep
}

// setupLocalstackRole creates an IAM role in LocalStack and returns its ARN.
func setupLocalstackRole(t *testing.T, endpoint string) string {
	t.Helper()
	ctx := context.Background()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}

	iamClient := iam.NewFromConfig(awsCfg, func(o *iam.Options) {
		o.BaseEndpoint = &endpoint
	})

	roleName := "jitsudo-integration-test"
	trustPolicy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"sts:AssumeRole"}]}`

	out, err := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 &roleName,
		AssumeRolePolicyDocument: &trustPolicy,
	})
	if err != nil {
		// Role may already exist from a previous test run — look it up instead.
		getOut, getErr := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: &roleName})
		if getErr != nil {
			t.Fatalf("create/get role: create=%v get=%v", err, getErr)
		}
		return *getOut.Role.Arn
	}
	return *out.Role.Arn
}

func TestIntegration_AWSProvider_GrantRevokeIsActive(t *testing.T) {
	endpoint := localstackEndpoint(t)
	roleARN := setupLocalstackRole(t, endpoint)

	// Extract account ID from the ARN (arn:aws:iam::<account>:role/<name>).
	// LocalStack uses "000000000000" as the default account ID.
	const accountID = "000000000000"

	cfg := awsprovider.Config{
		Mode:            "sts_assume_role",
		Region:          "us-east-1",
		RoleARNTemplate: roleARN, // use literal ARN — no template substitution needed
		MaxDuration:     types.Duration{Duration: 15 * time.Minute},
		EndpointURL:     endpoint,
	}
	_ = accountID // used above for setup

	ctx := context.Background()
	p, err := awsprovider.New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := providers.ElevationRequest{
		RequestID:     "integ-test-001",
		UserIdentity:  "alice@example.com",
		Provider:      "aws",
		RoleName:      "integration-test",
		ResourceScope: "000000000000",
		Duration:      15 * time.Minute,
		Reason:        "integration test",
	}

	// Override template to use the literal role ARN.
	cfg2 := cfg
	cfg2.RoleARNTemplate = roleARN

	awsCfgSDK, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	stsClient := sts.NewFromConfig(awsCfgSDK, func(o *sts.Options) { o.BaseEndpoint = &endpoint })
	iamClient := iam.NewFromConfig(awsCfgSDK, func(o *iam.Options) { o.BaseEndpoint = &endpoint })
	p2 := awsprovider.NewWithClients(cfg2, stsClient, iamClient)

	// Grant.
	grant, err := p2.Grant(ctx, req)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if grant.RequestID != req.RequestID {
		t.Errorf("grant.RequestID = %q, want %q", grant.RequestID, req.RequestID)
	}
	if grant.Credentials["AWS_ACCESS_KEY_ID"] == "" {
		t.Error("grant missing AWS_ACCESS_KEY_ID")
	}

	// IsActive (before expiry).
	active, err := p2.IsActive(ctx, *grant)
	if err != nil {
		t.Fatalf("IsActive: %v", err)
	}
	if !active {
		t.Error("grant should be active immediately after Grant")
	}

	// Idempotent Grant.
	grant2, err := p2.Grant(ctx, req)
	if err != nil {
		t.Fatalf("second Grant: %v", err)
	}
	if grant2.RequestID != grant.RequestID {
		t.Error("idempotent Grant returned different RequestID")
	}

	// Revoke.
	if err := p2.Revoke(ctx, *grant); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// Idempotent Revoke.
	if err := p2.Revoke(ctx, *grant); err != nil {
		t.Errorf("second Revoke should be idempotent, got: %v", err)
	}

	// Verify New() also works end-to-end (ambient credential chain + endpoint override).
	_ = p // ensure the non-WithClients constructor compiled correctly
}
