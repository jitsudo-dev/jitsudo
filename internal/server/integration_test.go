//go:build integration

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/internal/server"
	"github.com/jitsudo-dev/jitsudo/internal/store"
	"github.com/jitsudo-dev/jitsudo/internal/testutil"
	"github.com/jitsudo-dev/jitsudo/pkg/client"
)

const testMCPToken = "integration-test-mcp-token"

// Package-level state shared across all server integration tests.
var (
	testGRPCAddr  string
	testHTTPAddr  string
	aliceClient   *client.Client
	bobClient     *client.Client
	charlieClient *client.Client
	aliceToken    string
)

func TestMain(m *testing.M) {
	dbURL := os.Getenv("JITSUDOD_DATABASE_URL")
	if dbURL == "" {
		os.Exit(0) // no database → skip silently
	}
	oidcIssuer := testutil.MustGetEnv("JITSUDOD_OIDC_ISSUER", "http://localhost:5556/dex")

	// Run migrations (direct call, no testing.TB needed).
	if err := store.RunMigrations(dbURL); err != nil {
		fmt.Fprintf(os.Stderr, "server integration TestMain: migrations: %v\n", err)
		os.Exit(1)
	}

	// Remove any stale test policies left by previous failed runs before
	// starting the server, so they don't cause OPA compile errors.
	{
		cleanCtx := context.Background()
		cleanStore, cleanErr := store.New(cleanCtx, dbURL)
		if cleanErr == nil {
			if policies, listErr := cleanStore.ListPolicies(cleanCtx, nil); listErr == nil {
				for _, p := range policies {
					if strings.HasPrefix(p.Name, "test-policy-") {
						cleanStore.DeletePolicy(cleanCtx, p.ID) //nolint:errcheck
					}
				}
			}
			cleanStore.Close()
		}
	}

	// Fetch OIDC tokens via ROPC for each test user.
	var err error
	aliceToken, err = testutil.FetchToken(oidcIssuer, "alice@example.com", "password")
	if err != nil {
		fmt.Fprintf(os.Stderr, "server integration TestMain: alice token: %v\n", err)
		os.Exit(1)
	}
	bobToken, err := testutil.FetchToken(oidcIssuer, "bob@example.com", "password")
	if err != nil {
		fmt.Fprintf(os.Stderr, "server integration TestMain: bob token: %v\n", err)
		os.Exit(1)
	}
	charlieToken, err := testutil.FetchToken(oidcIssuer, "charlie@example.com", "password")
	if err != nil {
		fmt.Fprintf(os.Stderr, "server integration TestMain: charlie token: %v\n", err)
		os.Exit(1)
	}

	// Start jitsudod in-process on two random free ports.
	testGRPCAddr, testHTTPAddr = mustStartServerMain(dbURL, oidcIssuer)

	// Build authenticated gRPC clients.
	aliceClient = mustNewClientMain(testGRPCAddr, aliceToken)
	bobClient = mustNewClientMain(testGRPCAddr, bobToken)
	charlieClient = mustNewClientMain(testGRPCAddr, charlieToken)

	os.Exit(m.Run())
}

// mustStartServerMain starts jitsudod in a goroutine for TestMain use.
// It polls /healthz until ready (10 second timeout) and exits on failure.
func mustStartServerMain(dbURL, oidcIssuer string) (grpcAddr, httpAddr string) {
	grpcAddr = testutil.GetFreeAddrDirect()
	httpAddr = testutil.GetFreeAddrDirect()

	ctx := context.Background()
	s, err := store.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mustStartServerMain: store.New: %v\n", err)
		os.Exit(1)
	}

	srv := server.New(server.Config{
		GRPCAddr:         grpcAddr,
		HTTPAddr:         httpAddr,
		DatabaseURL:      dbURL,
		OIDCIssuer:       oidcIssuer,
		OIDCClientID:     "jitsudo-cli",
		MCPToken:         testMCPToken,
		MCPAgentIdentity: "test-mcp-agent",
		// Dex static-password users don't carry a groups claim, so we grant
		// Alice admin access via the email allowlist instead.
		AdminEmails: []string{"alice@example.com"},
	}, s)

	go func() {
		if err := srv.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, httpErr := http.Get("http://" + httpAddr + "/healthz")
		if httpErr == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return grpcAddr, httpAddr
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "mustStartServerMain: server at %s did not become ready within 10s\n", httpAddr)
	os.Exit(1)
	return "", ""
}

// mustNewClientMain creates an authenticated gRPC client for TestMain use.
func mustNewClientMain(grpcAddr, token string) *client.Client {
	c, err := client.New(context.Background(), client.Config{
		ServerURL: grpcAddr,
		Token:     token,
		Insecure:  true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mustNewClientMain: %v\n", err)
		os.Exit(1)
	}
	return c
}

// grpcCode returns the gRPC status code from an error, or codes.OK if nil.
func grpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	st, _ := status.FromError(err)
	return st.Code()
}

// newTestRequest is a helper that creates an elevation request as alice.
func newTestRequest(t *testing.T) string {
	t.Helper()
	resp, err := aliceClient.Service().CreateRequest(context.Background(), &jitsudov1alpha1.CreateRequestInput{
		Provider:        "mock",
		Role:            "test-role",
		ResourceScope:   "test-scope",
		DurationSeconds: 300,
		Reason:          fmt.Sprintf("integration test %s", ulid.Make().String()),
	})
	if err != nil {
		t.Fatalf("newTestRequest: CreateRequest: %v", err)
	}
	return resp.Request.Id
}

// ── Auth Enforcement ──────────────────────────────────────────────────────────

func TestAPI_AuthEnforcement_NoToken(t *testing.T) {
	unauthClient := testutil.MustNewClient(t, testGRPCAddr, "" /* no token */)
	ctx := context.Background()

	if _, err := unauthClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
		Provider: "mock", Role: "r", ResourceScope: "s", DurationSeconds: 300, Reason: "test",
	}); grpcCode(err) != codes.Unauthenticated {
		t.Errorf("CreateRequest without token: got %v, want Unauthenticated", grpcCode(err))
	}

	if _, err := unauthClient.Service().ListRequests(ctx, &jitsudov1alpha1.ListRequestsFilter{}); grpcCode(err) != codes.Unauthenticated {
		t.Errorf("ListRequests without token: got %v, want Unauthenticated", grpcCode(err))
	}

	// HTTP: missing Authorization header → 401
	resp, err := http.Get("http://" + testHTTPAddr + "/api/v1alpha1/requests")
	if err != nil {
		t.Fatalf("HTTP GET /api/v1alpha1/requests: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("HTTP without token: got %d, want 401", resp.StatusCode)
	}
}

func TestAPI_AuthEnforcement_InvalidToken(t *testing.T) {
	badClient := testutil.MustNewClient(t, testGRPCAddr, "garbage.not.a.jwt")
	_, err := badClient.Service().ListRequests(context.Background(), &jitsudov1alpha1.ListRequestsFilter{})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("ListRequests with bad token: got %v, want Unauthenticated", grpcCode(err))
	}
}

// ── Request CRUD ──────────────────────────────────────────────────────────────

func TestAPI_CreateRequest_Success(t *testing.T) {
	resp, err := aliceClient.Service().CreateRequest(context.Background(), &jitsudov1alpha1.CreateRequestInput{
		Provider:        "mock",
		Role:            "admin",
		ResourceScope:   "integration-test",
		DurationSeconds: 3600,
		Reason:          "TestAPI_CreateRequest_Success",
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if !strings.HasPrefix(resp.Request.Id, "req_") {
		t.Errorf("ID should start with req_, got %q", resp.Request.Id)
	}
	if resp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_PENDING {
		t.Errorf("State: got %v, want PENDING", resp.Request.State)
	}
	if resp.Request.RequesterIdentity != "alice@example.com" {
		t.Errorf("RequesterIdentity: got %q", resp.Request.RequesterIdentity)
	}
}

func TestAPI_CreateRequest_UnknownProvider(t *testing.T) {
	_, err := aliceClient.Service().CreateRequest(context.Background(), &jitsudov1alpha1.CreateRequestInput{
		Provider:        "does-not-exist",
		Role:            "admin",
		ResourceScope:   "scope",
		DurationSeconds: 3600,
		Reason:          "unknown provider test",
	})
	if grpcCode(err) != codes.InvalidArgument {
		t.Errorf("unknown provider: got %v, want InvalidArgument", grpcCode(err))
	}
}

func TestAPI_GetRequest_NotFound(t *testing.T) {
	_, err := aliceClient.Service().GetRequest(context.Background(), &jitsudov1alpha1.GetRequestInput{
		Id: "req_does_not_exist_zzz",
	})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("GetRequest nonexistent: got %v, want NotFound", grpcCode(err))
	}
}

func TestAPI_ListRequests(t *testing.T) {
	ctx := context.Background()

	// Create 2 requests as alice, 1 as bob.
	for i := 0; i < 2; i++ {
		aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{ //nolint:errcheck
			Provider: "mock", Role: "r", ResourceScope: "s",
			DurationSeconds: 300, Reason: fmt.Sprintf("list test %d", i),
		})
	}
	bobClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{ //nolint:errcheck
		Provider: "mock", Role: "r", ResourceScope: "s",
		DurationSeconds: 300, Reason: "bob list test",
	})

	// Unfiltered — at least 2 requests.
	allResp, err := aliceClient.Service().ListRequests(ctx, &jitsudov1alpha1.ListRequestsFilter{})
	if err != nil {
		t.Fatalf("ListRequests(unfiltered): %v", err)
	}
	if len(allResp.Requests) < 2 {
		t.Errorf("expected ≥2 requests, got %d", len(allResp.Requests))
	}

	// Filter by alice's identity — all results are alice's.
	aliceResp, err := aliceClient.Service().ListRequests(ctx, &jitsudov1alpha1.ListRequestsFilter{
		RequesterIdentity: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("ListRequests(alice): %v", err)
	}
	for _, r := range aliceResp.Requests {
		if r.RequesterIdentity != "alice@example.com" {
			t.Errorf("non-alice request in alice-filtered list: %q", r.RequesterIdentity)
		}
	}
}

// ── Full Workflow ─────────────────────────────────────────────────────────────

// TestAPI_FullWorkflow_ApproveToActive is the primary end-to-end scenario:
// create → approve → get request → get credentials.
func TestAPI_FullWorkflow_ApproveToActive(t *testing.T) {
	ctx := context.Background()

	// Step 1: alice creates a request.
	createResp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
		Provider:        "mock",
		Role:            "engineer",
		ResourceScope:   "dev-scope",
		DurationSeconds: 300,
		Reason:          "TestAPI_FullWorkflow_ApproveToActive",
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	reqID := createResp.Request.Id
	if createResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_PENDING {
		t.Fatalf("after create: State = %v, want PENDING", createResp.Request.State)
	}

	// Step 2: bob approves.
	approveResp, err := bobClient.Service().ApproveRequest(ctx, &jitsudov1alpha1.ApproveRequestInput{
		RequestId: reqID,
		Comment:   "LGTM",
	})
	if err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}
	if approveResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE {
		t.Fatalf("after approve: State = %v, want ACTIVE", approveResp.Request.State)
	}
	if approveResp.Request.ApproverIdentity != "bob@example.com" {
		t.Errorf("ApproverIdentity: got %q", approveResp.Request.ApproverIdentity)
	}
	if approveResp.Request.ExpiresAt == nil {
		t.Error("ExpiresAt should be set after ACTIVE")
	}

	// Step 3: alice fetches the request and confirms ACTIVE state.
	getResp, err := aliceClient.Service().GetRequest(ctx, &jitsudov1alpha1.GetRequestInput{Id: reqID})
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if getResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE {
		t.Errorf("GetRequest state: got %v, want ACTIVE", getResp.Request.State)
	}

	// Step 4: alice retrieves credentials.
	credResp, err := aliceClient.Service().GetCredentials(ctx, &jitsudov1alpha1.GetCredentialsInput{RequestId: reqID})
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if credResp.Grant == nil {
		t.Fatal("GetCredentials: grant is nil")
	}
	credMap := make(map[string]string)
	for _, c := range credResp.Grant.Credentials {
		credMap[c.Name] = c.Value
	}
	if _, ok := credMap["MOCK_ACCESS_KEY"]; !ok {
		t.Errorf("MOCK_ACCESS_KEY missing from credentials: %v", credMap)
	}
	if _, ok := credMap["MOCK_SESSION_TOKEN"]; !ok {
		t.Errorf("MOCK_SESSION_TOKEN missing from credentials: %v", credMap)
	}
	if credResp.Grant.ExpiresAt == nil {
		t.Error("grant ExpiresAt should be set")
	}

	// Step 5: bob cannot retrieve alice's credentials.
	_, err = bobClient.Service().GetCredentials(ctx, &jitsudov1alpha1.GetCredentialsInput{RequestId: reqID})
	if err == nil {
		t.Error("bob should not be able to retrieve alice's credentials")
	}
}

// TestAPI_FullWorkflow_DenyRequest verifies the deny path.
func TestAPI_FullWorkflow_DenyRequest(t *testing.T) {
	ctx := context.Background()
	reqID := newTestRequest(t)

	// bob denies.
	denyResp, err := bobClient.Service().DenyRequest(ctx, &jitsudov1alpha1.DenyRequestInput{
		RequestId: reqID,
		Reason:    "not justified",
	})
	if err != nil {
		t.Fatalf("DenyRequest: %v", err)
	}
	if denyResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_REJECTED {
		t.Errorf("after deny: State = %v, want REJECTED", denyResp.Request.State)
	}

	// alice cannot get credentials for a rejected request.
	_, err = aliceClient.Service().GetCredentials(ctx, &jitsudov1alpha1.GetCredentialsInput{RequestId: reqID})
	if err == nil {
		t.Error("GetCredentials on REJECTED request should fail")
	}
}

// TestAPI_GetCredentials_NotActive verifies that credentials can't be fetched while PENDING.
func TestAPI_GetCredentials_NotActive(t *testing.T) {
	reqID := newTestRequest(t)
	_, err := aliceClient.Service().GetCredentials(context.Background(), &jitsudov1alpha1.GetCredentialsInput{RequestId: reqID})
	if err == nil {
		t.Error("GetCredentials on PENDING request should fail")
	}
}

// TestAPI_ApproveRequest_AlreadyActive verifies that a second approval fails.
func TestAPI_ApproveRequest_AlreadyActive(t *testing.T) {
	ctx := context.Background()
	reqID := newTestRequest(t)

	// First approval succeeds.
	if _, err := bobClient.Service().ApproveRequest(ctx, &jitsudov1alpha1.ApproveRequestInput{RequestId: reqID}); err != nil {
		t.Fatalf("first ApproveRequest: %v", err)
	}

	// Second approval fails (request is now ACTIVE, not PENDING).
	_, err := bobClient.Service().ApproveRequest(ctx, &jitsudov1alpha1.ApproveRequestInput{RequestId: reqID})
	if err == nil {
		t.Error("second ApproveRequest should fail")
	}
}

// ── Policy Lifecycle ──────────────────────────────────────────────────────────

func TestAPI_PolicyLifecycle(t *testing.T) {
	ctx := context.Background()
	policyName := "test-policy-" + ulid.Make().String()

	// Apply a new policy. Disabled so it doesn't conflict with the seeded
	// allow-all eligibility policy (OPA rejects multiple default allow rules
	// in the same package). The CRUD path is exercised regardless of enablement.
	applyResp, err := aliceClient.Service().ApplyPolicy(ctx, &jitsudov1alpha1.ApplyPolicyInput{
		Name:        policyName,
		Type:        jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY,
		Rego:        "package jitsudo.eligibility\nimport future.keywords.if\nallow if { input.requester_identity != \"\" }",
		Description: "lifecycle test policy",
		Enabled:     false,
	})
	if err != nil {
		t.Fatalf("ApplyPolicy (create): %v", err)
	}
	if applyResp.Policy.Name != policyName {
		t.Errorf("Name: got %q", applyResp.Policy.Name)
	}
	policyID := applyResp.Policy.Id

	// ListPolicies contains our new policy.
	listResp, err := aliceClient.Service().ListPolicies(ctx, &jitsudov1alpha1.ListPoliciesInput{})
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	found := false
	for _, p := range listResp.Policies {
		if p.Id == policyID {
			found = true
		}
	}
	if !found {
		t.Errorf("policy %q not found in ListPolicies", policyID)
	}

	// GetPolicy by ID.
	getResp, err := aliceClient.Service().GetPolicy(ctx, &jitsudov1alpha1.GetPolicyInput{Id: policyID})
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if getResp.Policy.Name != policyName {
		t.Errorf("GetPolicy.Name: got %q", getResp.Policy.Name)
	}

	// Delete the policy.
	if _, err := aliceClient.Service().DeletePolicy(ctx, &jitsudov1alpha1.DeletePolicyInput{Id: policyID}); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}

	// GetPolicy after delete → NotFound.
	_, err = aliceClient.Service().GetPolicy(ctx, &jitsudov1alpha1.GetPolicyInput{Id: policyID})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("GetPolicy after delete: got %v, want NotFound", grpcCode(err))
	}
}

// TestAPI_PolicyEnforcement_EligibilityDeny verifies that disabling all eligibility
// policies causes the server to deny new requests (safe-by-default behaviour).
//
// This test mutates shared policy state — it must NOT be run with t.Parallel().
func TestAPI_PolicyEnforcement_EligibilityDeny(t *testing.T) {
	ctx := context.Background()

	// Fetch current enabled eligibility policies so we can restore them.
	listResp, err := aliceClient.Service().ListPolicies(ctx, &jitsudov1alpha1.ListPoliciesInput{})
	if err != nil {
		t.Fatalf("ListPolicies (pre-disable): %v", err)
	}

	type savedPolicy struct {
		id   string
		name string
		rego string
	}
	var eligibilityEnabled []savedPolicy
	for _, p := range listResp.Policies {
		if p.Type == jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY && p.Enabled {
			eligibilityEnabled = append(eligibilityEnabled, savedPolicy{id: p.Id, name: p.Name, rego: p.Rego})
		}
	}

	if len(eligibilityEnabled) == 0 {
		t.Skip("no enabled eligibility policies found — cannot test enforcement")
	}

	// Disable each policy (re-apply with enabled=false, preserving rego).
	for _, p := range eligibilityEnabled {
		if _, err := aliceClient.Service().ApplyPolicy(ctx, &jitsudov1alpha1.ApplyPolicyInput{
			Name:    p.name,
			Type:    jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY,
			Rego:    p.rego,
			Enabled: false,
		}); err != nil {
			t.Fatalf("ApplyPolicy (disable %q): %v", p.name, err)
		}
	}

	// Always restore all disabled policies, even if the test panics.
	t.Cleanup(func() {
		for _, p := range eligibilityEnabled {
			aliceClient.Service().ApplyPolicy(ctx, &jitsudov1alpha1.ApplyPolicyInput{ //nolint:errcheck
				Name:    p.name,
				Type:    jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY,
				Rego:    p.rego,
				Enabled: true,
			})
		}
	})

	// With no enabled eligibility policies, the engine falls back to deny-all.
	_, err = aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
		Provider:        "mock",
		Role:            "admin",
		ResourceScope:   "deny-test-scope",
		DurationSeconds: 300,
		Reason:          "should be denied by policy engine",
	})
	if err == nil {
		t.Error("CreateRequest should be denied when no eligibility policies are active")
	}
}

// ── Audit Log ─────────────────────────────────────────────────────────────────

func TestAPI_QueryAudit(t *testing.T) {
	ctx := context.Background()

	// Create a request and approve it to generate audit entries.
	reqID := newTestRequest(t)
	if _, err := bobClient.Service().ApproveRequest(ctx, &jitsudov1alpha1.ApproveRequestInput{RequestId: reqID}); err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}

	// Query audit events for this specific request.
	auditResp, err := aliceClient.Service().QueryAudit(ctx, &jitsudov1alpha1.QueryAuditInput{RequestId: reqID})
	if err != nil {
		t.Fatalf("QueryAudit: %v", err)
	}
	if len(auditResp.Events) < 2 {
		t.Errorf("expected ≥2 audit events (created + approved/grant), got %d", len(auditResp.Events))
	}

	// Events must be in ascending ID order and the hash chain must be consistent.
	for i := 1; i < len(auditResp.Events); i++ {
		prev := auditResp.Events[i-1]
		curr := auditResp.Events[i]
		if curr.Id <= prev.Id {
			t.Errorf("events not in ascending ID order: events[%d].id=%d <= events[%d].id=%d", i, curr.Id, i-1, prev.Id)
		}
		if curr.PrevHash != prev.Hash {
			t.Errorf("hash chain broken at event[%d]: PrevHash=%q, but event[%d].Hash=%q",
				i, curr.PrevHash, i-1, prev.Hash)
		}
	}
}

// ── Revoke ────────────────────────────────────────────────────────────────────

// TestAPI_FullWorkflow_RevokeActive verifies the full revoke flow:
// create → approve → active → revoke → REVOKED state.
func TestAPI_FullWorkflow_RevokeActive(t *testing.T) {
	ctx := context.Background()

	// Alice creates a request.
	createResp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
		Provider:        "mock",
		Role:            "engineer",
		ResourceScope:   "dev-scope",
		DurationSeconds: 300,
		Reason:          "TestAPI_FullWorkflow_RevokeActive",
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	reqID := createResp.Request.Id

	// Bob approves.
	if _, err := bobClient.Service().ApproveRequest(ctx, &jitsudov1alpha1.ApproveRequestInput{
		RequestId: reqID,
		Comment:   "approved for revoke test",
	}); err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}

	// Verify ACTIVE state.
	getResp, err := aliceClient.Service().GetRequest(ctx, &jitsudov1alpha1.GetRequestInput{Id: reqID})
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if getResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE {
		t.Fatalf("after approve: State = %v, want ACTIVE", getResp.Request.State)
	}

	// Alice revokes her own request.
	revokeResp, err := aliceClient.Service().RevokeRequest(ctx, &jitsudov1alpha1.RevokeRequestInput{
		RequestId: reqID,
		Reason:    "no longer needed",
	})
	if err != nil {
		t.Fatalf("RevokeRequest: %v", err)
	}
	if revokeResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_REVOKED {
		t.Errorf("after revoke: State = %v, want REVOKED", revokeResp.Request.State)
	}

	// Credentials must no longer be available.
	_, credErr := aliceClient.Service().GetCredentials(ctx, &jitsudov1alpha1.GetCredentialsInput{
		RequestId: reqID,
	})
	if credErr == nil {
		t.Error("GetCredentials on REVOKED request should fail, got nil error")
	}

	// Revoking again is idempotent (returns current state without error).
	revokeAgain, err := aliceClient.Service().RevokeRequest(ctx, &jitsudov1alpha1.RevokeRequestInput{
		RequestId: reqID,
		Reason:    "double revoke",
	})
	if err != nil {
		t.Fatalf("second RevokeRequest (idempotency): %v", err)
	}
	if revokeAgain.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_REVOKED {
		t.Errorf("after second revoke: State = %v, want REVOKED", revokeAgain.Request.State)
	}
}

// TestAPI_RevokeRequest_AccessDenied verifies that a different user cannot revoke
// a request they did not create (and are not an admin).
func TestAPI_RevokeRequest_AccessDenied(t *testing.T) {
	ctx := context.Background()

	// Alice creates and bob approves a request.
	reqID := newTestRequest(t)
	if _, err := bobClient.Service().ApproveRequest(ctx, &jitsudov1alpha1.ApproveRequestInput{
		RequestId: reqID,
	}); err != nil {
		t.Fatalf("ApproveRequest: %v", err)
	}

	// Charlie (unrelated user) attempts to revoke — must be denied.
	_, err := charlieClient.Service().RevokeRequest(ctx, &jitsudov1alpha1.RevokeRequestInput{
		RequestId: reqID,
		Reason:    "charlie trying to revoke",
	})
	if grpcCode(err) != codes.FailedPrecondition {
		t.Errorf("non-requester revoke: got %v, want FailedPrecondition", grpcCode(err))
	}
}

// TestAPI_BreakGlass_ImmediateActive verifies that a break-glass request
// bypasses the approval workflow and is returned in ACTIVE state immediately.
func TestAPI_BreakGlass_ImmediateActive(t *testing.T) {
	ctx := context.Background()

	resp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
		Provider:        "mock",
		Role:            "admin",
		ResourceScope:   "prod-scope",
		DurationSeconds: 600,
		Reason:          "TestAPI_BreakGlass: production incident",
		BreakGlass:      true,
	})
	if err != nil {
		t.Fatalf("break-glass CreateRequest: %v", err)
	}
	req := resp.Request
	if req.State != jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE {
		t.Fatalf("break-glass request state = %v, want ACTIVE", req.State)
	}
	if !req.BreakGlass {
		t.Error("break-glass request: BreakGlass flag should be true")
	}

	// Credentials must be immediately available.
	creds, err := aliceClient.Service().GetCredentials(ctx, &jitsudov1alpha1.GetCredentialsInput{
		RequestId: req.Id,
	})
	if err != nil {
		t.Fatalf("GetCredentials on break-glass: %v", err)
	}
	if len(creds.GetGrant().GetCredentials()) == 0 {
		t.Error("break-glass: expected non-empty credentials")
	}
}

// ── Tier 1: Auto-Approve ──────────────────────────────────────────────────────

// autoApprovalRego is the shared Rego policy for auto-approve integration tests.
// It routes role "auto-approve-role" to Tier 1 (auto).
// Note: no "default allow" or "default approver_tier" here — the dev seed policy
// already provides "default allow := true", and evalTier defaults to "human".
// Defining these defaults again causes OPA compile errors (duplicate default rules).
const autoApprovalRego = `package jitsudo.approval
import rego.v1

approver_tier := "auto" if {
    input.request.role == "auto-approve-role"
    input.request.provider == "mock"
}
`

// withAutoApprovalPolicy applies a test approval policy that auto-approves
// "auto-approve-role" requests, calls fn, and cleans up the policy after.
// The test must NOT be run with t.Parallel() as it mutates shared policy state.
func withAutoApprovalPolicy(t *testing.T, fn func()) {
	t.Helper()
	ctx := context.Background()
	policyName := "test-policy-" + ulid.Make().String()

	resp, err := aliceClient.Service().ApplyPolicy(ctx, &jitsudov1alpha1.ApplyPolicyInput{
		Name:        policyName,
		Type:        jitsudov1alpha1.PolicyType_POLICY_TYPE_APPROVAL,
		Rego:        autoApprovalRego,
		Description: "Tier 1 auto-approve integration test",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("withAutoApprovalPolicy: ApplyPolicy: %v", err)
	}
	t.Cleanup(func() {
		aliceClient.Service().DeletePolicy(ctx, &jitsudov1alpha1.DeletePolicyInput{Id: resp.Policy.Id}) //nolint:errcheck
	})

	fn()
}

// TestAPI_TierRouting_AutoApprove verifies that a policy returning approver_tier="auto"
// causes CreateRequest to return a fully ACTIVE grant (Tier 1 synchronous flow).
func TestAPI_TierRouting_AutoApprove(t *testing.T) {
	withAutoApprovalPolicy(t, func() {
		ctx := context.Background()
		resp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
			Provider:        "mock",
			Role:            "auto-approve-role",
			ResourceScope:   "tier1-test-scope",
			DurationSeconds: 300,
			Reason:          "TestAPI_TierRouting_AutoApprove",
		})
		if err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
		if resp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE {
			t.Errorf("State: got %v, want ACTIVE", resp.Request.State)
		}
		if resp.Request.ApproverIdentity != "policy" {
			t.Errorf("ApproverIdentity: got %q, want %q", resp.Request.ApproverIdentity, "policy")
		}
		if resp.Request.ApproverTier != "auto" {
			t.Errorf("ApproverTier: got %q, want %q", resp.Request.ApproverTier, "auto")
		}
	})
}

// TestAPI_TierRouting_HumanDefault verifies that a request routed to tier "human"
// stays PENDING, waiting for a human approver.
func TestAPI_TierRouting_HumanDefault(t *testing.T) {
	ctx := context.Background()
	// The seeded dev-allow-all-approval policy has no approver_tier rule → defaults to "human".
	resp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
		Provider:        "mock",
		Role:            "test-role",
		ResourceScope:   "human-tier-test-scope",
		DurationSeconds: 300,
		Reason:          "TestAPI_TierRouting_HumanDefault",
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if resp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_PENDING {
		t.Errorf("State: got %v, want PENDING", resp.Request.State)
	}
	if resp.Request.ApproverTier != "human" {
		t.Errorf("ApproverTier: got %q, want %q", resp.Request.ApproverTier, "human")
	}
}

// TestAPI_TierRouting_AIReview verifies that a request routed to tier "ai_review"
// stays PENDING with approver_tier="ai_review" (MCP approver handles it in Phase 5).
func TestAPI_TierRouting_AIReview(t *testing.T) {
	ctx := context.Background()
	policyName := "test-policy-" + ulid.Make().String()

	resp, err := aliceClient.Service().ApplyPolicy(ctx, &jitsudov1alpha1.ApplyPolicyInput{
		Name: policyName,
		Type: jitsudov1alpha1.PolicyType_POLICY_TYPE_APPROVAL,
		Rego: `package jitsudo.approval
import rego.v1

approver_tier := "ai_review" if {
    input.request.role == "ai-review-role"
}
`,
		Description: "Tier 2 ai_review test",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	t.Cleanup(func() {
		aliceClient.Service().DeletePolicy(ctx, &jitsudov1alpha1.DeletePolicyInput{Id: resp.Policy.Id}) //nolint:errcheck
	})

	createResp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
		Provider:        "mock",
		Role:            "ai-review-role",
		ResourceScope:   "ai-tier-test-scope",
		DurationSeconds: 300,
		Reason:          "TestAPI_TierRouting_AIReview",
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if createResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_PENDING {
		t.Errorf("State: got %v, want PENDING (ai_review waits for MCP agent)", createResp.Request.State)
	}
	if createResp.Request.ApproverTier != "ai_review" {
		t.Errorf("ApproverTier: got %q, want %q", createResp.Request.ApproverTier, "ai_review")
	}
}

// TestAPI_AutoApprove_CredentialsAccessible verifies that after a Tier 1 auto-approve,
// the requester can immediately retrieve credentials via GetCredentials.
func TestAPI_AutoApprove_CredentialsAccessible(t *testing.T) {
	withAutoApprovalPolicy(t, func() {
		ctx := context.Background()
		resp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
			Provider:        "mock",
			Role:            "auto-approve-role",
			ResourceScope:   "creds-test-scope",
			DurationSeconds: 300,
			Reason:          "TestAPI_AutoApprove_CredentialsAccessible",
		})
		if err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
		if resp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE {
			t.Fatalf("expected ACTIVE, got %v", resp.Request.State)
		}

		creds, err := aliceClient.Service().GetCredentials(ctx, &jitsudov1alpha1.GetCredentialsInput{
			RequestId: resp.Request.Id,
		})
		if err != nil {
			t.Fatalf("GetCredentials: %v", err)
		}
		if len(creds.GetGrant().GetCredentials()) == 0 {
			t.Error("expected non-empty credentials after auto-approve")
		}
	})
}

// TestAPI_AutoApprove_AuditTrail verifies that Tier 1 auto-approval writes
// request.auto_approved (not request.approved) to the audit log.
func TestAPI_AutoApprove_AuditTrail(t *testing.T) {
	withAutoApprovalPolicy(t, func() {
		ctx := context.Background()
		resp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
			Provider:        "mock",
			Role:            "auto-approve-role",
			ResourceScope:   "audit-test-scope",
			DurationSeconds: 300,
			Reason:          "TestAPI_AutoApprove_AuditTrail",
		})
		if err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}

		auditResp, err := aliceClient.Service().QueryAudit(ctx, &jitsudov1alpha1.QueryAuditInput{
			RequestId: resp.Request.Id,
		})
		if err != nil {
			t.Fatalf("QueryAudit: %v", err)
		}

		var actions []string
		for _, e := range auditResp.Events {
			actions = append(actions, e.Action)
		}

		wantAutoApproved := "request.auto_approved"
		wantGrantIssued := "grant.issued"
		found := map[string]bool{}
		for _, a := range actions {
			found[a] = true
		}
		if !found[wantAutoApproved] {
			t.Errorf("audit trail missing %q; got actions: %v", wantAutoApproved, actions)
		}
		if !found[wantGrantIssued] {
			t.Errorf("audit trail missing %q; got actions: %v", wantGrantIssued, actions)
		}
		// request.approved must NOT appear — that's the human approval action
		if found["request.approved"] {
			t.Errorf("audit trail should not contain %q for auto-approved request; got actions: %v", "request.approved", actions)
		}
	})
}

// ── Tier 2: MCP Approver ──────────────────────────────────────────────────────

// mcpPost sends a JSON-RPC request to the /mcp endpoint and returns the response.
func mcpPost(t *testing.T, method string, params any) map[string]any {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, "http://"+testHTTPAddr+"/mcp", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testMCPToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mcpPost %s: %v", method, err)
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("mcpPost decode: %v", err)
	}
	return result
}

// withAIReviewPolicy applies a test approval policy that routes "ai-review-role"
// requests to ai_review, calls fn, and cleans up afterward.
func withAIReviewPolicy(t *testing.T, fn func()) {
	t.Helper()
	ctx := context.Background()
	policyName := "test-policy-" + ulid.Make().String()
	resp, err := aliceClient.Service().ApplyPolicy(ctx, &jitsudov1alpha1.ApplyPolicyInput{
		Name: policyName,
		Type: jitsudov1alpha1.PolicyType_POLICY_TYPE_APPROVAL,
		Rego: `package jitsudo.approval
import rego.v1

approver_tier := "ai_review" if {
    input.request.role == "ai-review-role"
    input.request.provider == "mock"
}
`,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("withAIReviewPolicy: %v", err)
	}
	t.Cleanup(func() {
		aliceClient.Service().DeletePolicy(ctx, &jitsudov1alpha1.DeletePolicyInput{Id: resp.Policy.Id}) //nolint:errcheck
	})
	fn()
}

// TestAPI_MCP_Initialize verifies the MCP endpoint responds to initialize.
func TestAPI_MCP_Initialize(t *testing.T) {
	result := mcpPost(t, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	})
	if result["error"] != nil {
		t.Fatalf("initialize error: %v", result["error"])
	}
	r, ok := result["result"].(map[string]any)
	if !ok || r == nil {
		t.Fatalf("result field missing or wrong type: %v", result["result"])
	}
	if pv, _ := r["protocolVersion"].(string); pv == "" {
		t.Error("expected non-empty protocolVersion")
	}
}

// TestAPI_MCP_ToolsList verifies the tools/list endpoint returns the five tools.
func TestAPI_MCP_ToolsList(t *testing.T) {
	result := mcpPost(t, "tools/list", nil)
	if result["error"] != nil {
		t.Fatalf("tools/list error: %v", result["error"])
	}
	r, _ := result["result"].(map[string]any)
	tools, _ := r["tools"].([]any)
	if len(tools) < 5 {
		t.Errorf("expected ≥5 tools, got %d", len(tools))
	}
}

// TestAPI_MCP_BadToken verifies that an invalid token is rejected.
func TestAPI_MCP_BadToken(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	req, _ := http.NewRequest(http.MethodPost, "http://"+testHTTPAddr+"/mcp", strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", resp.StatusCode)
	}
}

// TestAPI_Tier2_MCPApprove verifies that an MCP approve call transitions an
// ai_review request to ACTIVE and records AI reasoning in the audit trail.
func TestAPI_Tier2_MCPApprove(t *testing.T) {
	withAIReviewPolicy(t, func() {
		ctx := context.Background()

		// Create a request that will be routed to ai_review.
		createResp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
			Provider:        "mock",
			Role:            "ai-review-role",
			ResourceScope:   "mcp-approve-test",
			DurationSeconds: 300,
			Reason:          "TestAPI_Tier2_MCPApprove",
		})
		if err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
		reqID := createResp.Request.Id
		if createResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_PENDING {
			t.Fatalf("expected PENDING, got %v", createResp.Request.State)
		}

		// MCP agent approves with reasoning.
		reasoning := `{"assessment":"low risk","blast_radius":"read-only scope","confidence":0.92}`
		mcpResult := mcpPost(t, "tools/call", map[string]any{
			"name": "approve_request",
			"arguments": map[string]any{
				"request_id": reqID,
				"reasoning":  reasoning,
			},
		})
		if mcpResult["error"] != nil {
			t.Fatalf("MCP approve error: %v", mcpResult["error"])
		}

		// Request should now be ACTIVE.
		getResp, err := aliceClient.Service().GetRequest(ctx, &jitsudov1alpha1.GetRequestInput{Id: reqID})
		if err != nil {
			t.Fatalf("GetRequest: %v", err)
		}
		if getResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE {
			t.Errorf("State: got %v, want ACTIVE", getResp.Request.State)
		}
		if getResp.Request.ApproverIdentity != "test-mcp-agent" {
			t.Errorf("ApproverIdentity: got %q, want %q", getResp.Request.ApproverIdentity, "test-mcp-agent")
		}

		// AI reasoning should appear in audit log.
		auditResp, err := aliceClient.Service().QueryAudit(ctx, &jitsudov1alpha1.QueryAuditInput{RequestId: reqID})
		if err != nil {
			t.Fatalf("QueryAudit: %v", err)
		}
		found := false
		for _, e := range auditResp.Events {
			if e.Action == "request.ai_approved" {
				found = true
				if !strings.Contains(e.DetailsJson, "assessment") {
					t.Errorf("ai_approved audit entry details missing reasoning: %s", e.DetailsJson)
				}
			}
		}
		if !found {
			t.Error("audit trail missing request.ai_approved")
		}
	})
}

// TestAPI_Tier2_MCPEscalate verifies that an MCP escalate call flips the
// approver_tier to "human" so the request enters the human approval queue.
func TestAPI_Tier2_MCPEscalate(t *testing.T) {
	withAIReviewPolicy(t, func() {
		ctx := context.Background()

		createResp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
			Provider:        "mock",
			Role:            "ai-review-role",
			ResourceScope:   "mcp-escalate-test",
			DurationSeconds: 300,
			Reason:          "TestAPI_Tier2_MCPEscalate",
		})
		if err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
		reqID := createResp.Request.Id

		// MCP agent escalates.
		mcpResult := mcpPost(t, "tools/call", map[string]any{
			"name": "escalate_to_human",
			"arguments": map[string]any{
				"request_id": reqID,
				"reasoning":  "uncertainty — linked incident INC-9999 may broaden scope",
			},
		})
		if mcpResult["error"] != nil {
			t.Fatalf("MCP escalate error: %v", mcpResult["error"])
		}

		// Request should still be PENDING but approver_tier should be "human".
		getResp, err := aliceClient.Service().GetRequest(ctx, &jitsudov1alpha1.GetRequestInput{Id: reqID})
		if err != nil {
			t.Fatalf("GetRequest: %v", err)
		}
		if getResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_PENDING {
			t.Errorf("State: got %v, want PENDING", getResp.Request.State)
		}
		if getResp.Request.ApproverTier != "human" {
			t.Errorf("ApproverTier: got %q, want %q", getResp.Request.ApproverTier, "human")
		}

		// Audit trail should contain request.ai_escalated.
		auditResp, err := aliceClient.Service().QueryAudit(ctx, &jitsudov1alpha1.QueryAuditInput{RequestId: reqID})
		if err != nil {
			t.Fatalf("QueryAudit: %v", err)
		}
		found := false
		for _, e := range auditResp.Events {
			if e.Action == "request.ai_escalated" {
				found = true
			}
		}
		if !found {
			t.Error("audit trail missing request.ai_escalated")
		}

		// After escalation to human, a human should be able to approve normally.
		if _, err := bobClient.Service().ApproveRequest(ctx, &jitsudov1alpha1.ApproveRequestInput{
			RequestId: reqID,
			Comment:   "approved after AI escalation",
		}); err != nil {
			t.Fatalf("ApproveRequest (post-escalation): %v", err)
		}
		finalResp, err := aliceClient.Service().GetRequest(ctx, &jitsudov1alpha1.GetRequestInput{Id: reqID})
		if err != nil {
			t.Fatalf("GetRequest (final): %v", err)
		}
		if finalResp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE {
			t.Errorf("final State: got %v, want ACTIVE", finalResp.Request.State)
		}
	})
}

// ── Trust Tier ────────────────────────────────────────────────────────────────

// TestAPI_TrustTier_SetAndGet verifies the full admin round-trip for trust tier
// assignment: set tier, then read it back via GetPrincipal.
func TestAPI_TrustTier_SetAndGet(t *testing.T) {
	ctx := context.Background()
	identity := "alice@example.com"

	// Alice is in jitsudo-admins in the test Dex config — she can set tiers.
	resp, err := aliceClient.Service().SetPrincipalTrustTier(ctx, &jitsudov1alpha1.SetPrincipalTrustTierInput{
		Identity:  identity,
		TrustTier: 3,
		Notes:     "integration test",
	})
	if err != nil {
		t.Fatalf("SetPrincipalTrustTier: %v", err)
	}
	if resp.Principal.TrustTier != 3 {
		t.Errorf("TrustTier: got %d, want 3", resp.Principal.TrustTier)
	}
	if resp.Principal.EnrolledBy != identity {
		t.Errorf("EnrolledBy: got %q, want %q", resp.Principal.EnrolledBy, identity)
	}

	// Read back via GetPrincipal.
	getResp, err := aliceClient.Service().GetPrincipal(ctx, &jitsudov1alpha1.GetPrincipalInput{
		Identity: identity,
	})
	if err != nil {
		t.Fatalf("GetPrincipal: %v", err)
	}
	if getResp.Principal.TrustTier != 3 {
		t.Errorf("GetPrincipal TrustTier: got %d, want 3", getResp.Principal.TrustTier)
	}

	// Cleanup: reset to tier 0.
	t.Cleanup(func() {
		aliceClient.Service().SetPrincipalTrustTier(ctx, &jitsudov1alpha1.SetPrincipalTrustTierInput{ //nolint:errcheck
			Identity: identity, TrustTier: 0,
		})
	})
}

// TestAPI_TrustTier_NonAdminDenied verifies that a non-admin cannot set trust tiers.
func TestAPI_TrustTier_NonAdminDenied(t *testing.T) {
	ctx := context.Background()
	// Charlie is not in jitsudo-admins.
	_, err := charlieClient.Service().SetPrincipalTrustTier(ctx, &jitsudov1alpha1.SetPrincipalTrustTierInput{
		Identity:  "charlie@example.com",
		TrustTier: 2,
	})
	if grpcCode(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", grpcCode(err))
	}
}

// TestAPI_TrustTier_InvalidRange verifies that out-of-range tier values are rejected.
func TestAPI_TrustTier_InvalidRange(t *testing.T) {
	ctx := context.Background()
	_, err := aliceClient.Service().SetPrincipalTrustTier(ctx, &jitsudov1alpha1.SetPrincipalTrustTierInput{
		Identity:  "alice@example.com",
		TrustTier: 5, // out of range
	})
	if grpcCode(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for tier=5, got %v", grpcCode(err))
	}
}

// TestAPI_TrustTier_GetUnknown verifies that GetPrincipal on an unseen identity
// returns NotFound.
func TestAPI_TrustTier_GetUnknown(t *testing.T) {
	ctx := context.Background()
	_, err := aliceClient.Service().GetPrincipal(ctx, &jitsudov1alpha1.GetPrincipalInput{
		Identity: "nobody-" + ulid.Make().String() + "@example.com",
	})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("expected NotFound for unknown principal, got %v", grpcCode(err))
	}
}

// TestAPI_TrustTier_InPolicy verifies that input.context.trust_tier is populated
// in OPA evaluations. A policy that gates auto-approve on trust_tier >= 3 should
// route tier-3 principals to Tier 1, while tier-0 principals go to human.
func TestAPI_TrustTier_InPolicy(t *testing.T) {
	ctx := context.Background()
	identity := "alice@example.com"

	// Elevate Alice to trust tier 3.
	_, err := aliceClient.Service().SetPrincipalTrustTier(ctx, &jitsudov1alpha1.SetPrincipalTrustTierInput{
		Identity: identity, TrustTier: 3,
	})
	if err != nil {
		t.Fatalf("SetPrincipalTrustTier: %v", err)
	}
	t.Cleanup(func() {
		aliceClient.Service().SetPrincipalTrustTier(ctx, &jitsudov1alpha1.SetPrincipalTrustTierInput{ //nolint:errcheck
			Identity: identity, TrustTier: 0,
		})
	})

	// Apply a policy that auto-approves when trust_tier >= 3 and role is "trust-gated-role".
	policyName := "test-policy-" + ulid.Make().String()
	pResp, err := aliceClient.Service().ApplyPolicy(ctx, &jitsudov1alpha1.ApplyPolicyInput{
		Name: policyName,
		Type: jitsudov1alpha1.PolicyType_POLICY_TYPE_APPROVAL,
		// Note: no "default allow" or "default approver_tier" — the dev seed
		// policy already defines them; duplicates cause OPA compile errors.
		Rego: `package jitsudo.approval
import rego.v1

approver_tier := "auto" if {
    input.context.trust_tier >= 3
    input.request.role == "trust-gated-role"
    input.request.provider == "mock"
}
`,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("ApplyPolicy: %v", err)
	}
	t.Cleanup(func() {
		aliceClient.Service().DeletePolicy(ctx, &jitsudov1alpha1.DeletePolicyInput{Id: pResp.Policy.Id}) //nolint:errcheck
	})

	// With trust_tier=3, should auto-approve → ACTIVE.
	resp, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
		Provider:        "mock",
		Role:            "trust-gated-role",
		ResourceScope:   "trust-tier-test",
		DurationSeconds: 300,
		Reason:          "TestAPI_TrustTier_InPolicy tier-3",
	})
	if err != nil {
		t.Fatalf("CreateRequest (tier-3): %v", err)
	}
	if resp.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE {
		t.Errorf("tier-3 State: got %v, want ACTIVE", resp.Request.State)
	}

	// Now drop Alice to tier 0 — same policy should route to human → PENDING.
	_, err = aliceClient.Service().SetPrincipalTrustTier(ctx, &jitsudov1alpha1.SetPrincipalTrustTierInput{
		Identity: identity, TrustTier: 0,
	})
	if err != nil {
		t.Fatalf("SetPrincipalTrustTier (downgrade): %v", err)
	}

	resp2, err := aliceClient.Service().CreateRequest(ctx, &jitsudov1alpha1.CreateRequestInput{
		Provider:        "mock",
		Role:            "trust-gated-role",
		ResourceScope:   "trust-tier-test-2",
		DurationSeconds: 300,
		Reason:          "TestAPI_TrustTier_InPolicy tier-0",
	})
	if err != nil {
		t.Fatalf("CreateRequest (tier-0): %v", err)
	}
	if resp2.Request.State != jitsudov1alpha1.RequestState_REQUEST_STATE_PENDING {
		t.Errorf("tier-0 State: got %v, want PENDING", resp2.Request.State)
	}
}

// ── Health & Version ──────────────────────────────────────────────────────────

func TestAPI_HealthEndpoints(t *testing.T) {
	for _, tc := range []struct {
		path     string
		wantCode int
		wantBody string
	}{
		{"/healthz", http.StatusOK, "ok"},
		{"/readyz", http.StatusOK, "ok"},
		{"/version", http.StatusOK, "version"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := http.Get("http://" + testHTTPAddr + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.wantCode {
				t.Errorf("status: got %d, want %d", resp.StatusCode, tc.wantCode)
			}
			if !strings.Contains(string(body), tc.wantBody) {
				t.Errorf("body %q does not contain %q", body, tc.wantBody)
			}
		})
	}
}
