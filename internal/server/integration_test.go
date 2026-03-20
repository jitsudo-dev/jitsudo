//go:build integration

package server_test

import (
	"context"
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
		GRPCAddr:     grpcAddr,
		HTTPAddr:     httpAddr,
		DatabaseURL:  dbURL,
		OIDCIssuer:   oidcIssuer,
		OIDCClientID: "jitsudo-cli",
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
