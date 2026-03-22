// License: Elastic License 2.0 (ELv2)
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// ── Mocks ──────────────────────────────────────────────────────────────────

type mockWorkflow struct {
	approveFunc  func(ctx context.Context, id, agent, reasoning string) (*store.RequestRow, error)
	denyFunc     func(ctx context.Context, id, agent, reasoning string) (*store.RequestRow, error)
	escalateFunc func(ctx context.Context, id, agent, reasoning string) (*store.RequestRow, error)
}

func (m *mockWorkflow) AIApproveRequest(ctx context.Context, id, agent, reasoning string) (*store.RequestRow, error) {
	return m.approveFunc(ctx, id, agent, reasoning)
}
func (m *mockWorkflow) AIDenyRequest(ctx context.Context, id, agent, reasoning string) (*store.RequestRow, error) {
	return m.denyFunc(ctx, id, agent, reasoning)
}
func (m *mockWorkflow) AIEscalateRequest(ctx context.Context, id, agent, reasoning string) (*store.RequestRow, error) {
	return m.escalateFunc(ctx, id, agent, reasoning)
}

type mockStore struct {
	listFunc func(ctx context.Context) ([]*store.RequestRow, error)
	getFunc  func(ctx context.Context, id string) (*store.RequestRow, error)
}

func (m *mockStore) ListPendingAIReview(ctx context.Context) ([]*store.RequestRow, error) {
	return m.listFunc(ctx)
}
func (m *mockStore) GetRequest(ctx context.Context, id string) (*store.RequestRow, error) {
	return m.getFunc(ctx, id)
}

// ── Helpers ────────────────────────────────────────────────────────────────

func testServer(t *testing.T, wf workflowEngine, s storeReader) *Server {
	t.Helper()
	return New(wf, s, "test-token", "test-agent")
}

func doPost(t *testing.T, srv *Server, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func decodeResp(t *testing.T, w *httptest.ResponseRecorder) rpcResponse {
	t.Helper()
	var r rpcResponse
	if err := json.NewDecoder(w.Body).Decode(&r); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	return r
}

func rpcReq(method string, params any) map[string]any {
	m := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		m["params"] = params
	}
	return m
}

// ── Auth ───────────────────────────────────────────────────────────────────

func TestMCP_Auth_Missing(t *testing.T) {
	srv := testServer(t, &mockWorkflow{}, &mockStore{})
	w := doPost(t, srv, "", rpcReq("initialize", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}

func TestMCP_Auth_WrongToken(t *testing.T) {
	srv := testServer(t, &mockWorkflow{}, &mockStore{})
	w := doPost(t, srv, "wrong-token", rpcReq("initialize", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", w.Code)
	}
}

func TestMCP_Disabled(t *testing.T) {
	srv := New(&mockWorkflow{}, &mockStore{}, "" /* no token = disabled */, "agent")
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

// ── initialize ─────────────────────────────────────────────────────────────

func TestMCP_Initialize(t *testing.T) {
	srv := testServer(t, &mockWorkflow{}, &mockStore{})
	w := doPost(t, srv, "test-token", rpcReq("initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"clientInfo":      map[string]any{"name": "test", "version": "1"},
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
	resp := decodeResp(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result, _ := json.Marshal(resp.Result)
	var init initializeResult
	if err := json.Unmarshal(result, &init); err != nil {
		t.Fatalf("unmarshal initializeResult: %v", err)
	}
	if init.ProtocolVersion == "" {
		t.Error("protocolVersion should be non-empty")
	}
	if init.ServerInfo.Name == "" {
		t.Error("serverInfo.name should be non-empty")
	}
}

// ── tools/list ─────────────────────────────────────────────────────────────

func TestMCP_ToolsList(t *testing.T) {
	srv := testServer(t, &mockWorkflow{}, &mockStore{})
	w := doPost(t, srv, "test-token", rpcReq("tools/list", nil))
	resp := decodeResp(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, _ := json.Marshal(resp.Result)
	var list toolsListResult
	if err := json.Unmarshal(result, &list); err != nil {
		t.Fatalf("unmarshal toolsListResult: %v", err)
	}

	wantTools := []string{"list_pending_ai_review", "get_request_details", "approve_request", "deny_request", "escalate_to_human"}
	found := map[string]bool{}
	for _, tool := range list.Tools {
		found[tool.Name] = true
		if tool.InputSchema == nil {
			t.Errorf("tool %q has nil input schema", tool.Name)
		}
	}
	for _, name := range wantTools {
		if !found[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

// ── tools/call: list_pending_ai_review ─────────────────────────────────────

func TestMCP_ListPending(t *testing.T) {
	now := time.Now().UTC()
	s := &mockStore{
		listFunc: func(_ context.Context) ([]*store.RequestRow, error) {
			return []*store.RequestRow{
				{ID: "req_001", Provider: "aws", Role: "admin", RequesterIdentity: "alice@example.com", CreatedAt: now},
			}, nil
		},
	}
	srv := testServer(t, &mockWorkflow{}, s)
	w := doPost(t, srv, "test-token", rpcReq("tools/call", map[string]any{
		"name": "list_pending_ai_review", "arguments": map[string]any{},
	}))
	resp := decodeResp(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result, _ := json.Marshal(resp.Result)
	var tc toolCallResult
	if err := json.Unmarshal(result, &tc); err != nil {
		t.Fatalf("unmarshal toolCallResult: %v", err)
	}
	if len(tc.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	if !bytes.Contains([]byte(tc.Content[0].Text), []byte("req_001")) {
		t.Errorf("expected req_001 in response, got: %s", tc.Content[0].Text)
	}
}

// ── tools/call: approve_request ────────────────────────────────────────────

func TestMCP_Approve(t *testing.T) {
	var capturedReasoning string
	wf := &mockWorkflow{
		approveFunc: func(_ context.Context, id, agent, reasoning string) (*store.RequestRow, error) {
			capturedReasoning = reasoning
			return &store.RequestRow{ID: id, State: store.StateActive}, nil
		},
	}
	srv := testServer(t, wf, &mockStore{})
	w := doPost(t, srv, "test-token", rpcReq("tools/call", map[string]any{
		"name": "approve_request",
		"arguments": map[string]any{
			"request_id": "req_001",
			"reasoning":  `{"assessment":"low risk","blast_radius":"read-only"}`,
		},
	}))
	resp := decodeResp(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if !json.Valid([]byte(capturedReasoning)) {
		t.Errorf("captured reasoning should be valid JSON, got: %s", capturedReasoning)
	}
}

// ── tools/call: deny_request ───────────────────────────────────────────────

func TestMCP_Deny(t *testing.T) {
	wf := &mockWorkflow{
		denyFunc: func(_ context.Context, id, _, _ string) (*store.RequestRow, error) {
			return &store.RequestRow{ID: id, State: store.StateRejected}, nil
		},
	}
	srv := testServer(t, wf, &mockStore{})
	w := doPost(t, srv, "test-token", rpcReq("tools/call", map[string]any{
		"name": "deny_request",
		"arguments": map[string]any{
			"request_id": "req_002",
			"reasoning":  "blast radius too high",
		},
	}))
	resp := decodeResp(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result, _ := json.Marshal(resp.Result)
	var tc toolCallResult
	if err := json.Unmarshal(result, &tc); err != nil {
		t.Fatalf("unmarshal toolCallResult: %v", err)
	}
	if len(tc.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	if !bytes.Contains([]byte(tc.Content[0].Text), []byte("denied")) {
		t.Errorf("expected 'denied' in response, got: %s", tc.Content[0].Text)
	}
	if !bytes.Contains([]byte(tc.Content[0].Text), []byte("req_002")) {
		t.Errorf("expected request_id in response, got: %s", tc.Content[0].Text)
	}
}

// ── tools/call: escalate_to_human ──────────────────────────────────────────

func TestMCP_Escalate(t *testing.T) {
	wf := &mockWorkflow{
		escalateFunc: func(_ context.Context, id, _, _ string) (*store.RequestRow, error) {
			return &store.RequestRow{ID: id, State: store.StatePending, ApproverTier: "human"}, nil
		},
	}
	srv := testServer(t, wf, &mockStore{})
	w := doPost(t, srv, "test-token", rpcReq("tools/call", map[string]any{
		"name": "escalate_to_human",
		"arguments": map[string]any{
			"request_id": "req_003",
			"reasoning":  "uncertain — linked incident may broaden scope",
		},
	}))
	resp := decodeResp(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result, _ := json.Marshal(resp.Result)
	var tc toolCallResult
	if err := json.Unmarshal(result, &tc); err != nil {
		t.Fatalf("unmarshal toolCallResult: %v", err)
	}
	if len(tc.Content) == 0 || !bytes.Contains([]byte(tc.Content[0].Text), []byte("escalated")) {
		t.Errorf("expected 'escalated' in response, got: %v", tc.Content)
	}
}

// ── tools/call: missing required arg ───────────────────────────────────────

func TestMCP_Approve_MissingRequestID(t *testing.T) {
	srv := testServer(t, &mockWorkflow{}, &mockStore{})
	w := doPost(t, srv, "test-token", rpcReq("tools/call", map[string]any{
		"name":      "approve_request",
		"arguments": map[string]any{"reasoning": "some reason"},
	}))
	resp := decodeResp(t, w)
	if resp.Error == nil {
		t.Error("expected error for missing request_id")
	}
	if resp.Error.Code != errCodeInvalidParams {
		t.Errorf("expected InvalidParams, got code %d", resp.Error.Code)
	}
}

// ── notifications (no id → 202) ────────────────────────────────────────────

func TestMCP_Notification_NoResponse(t *testing.T) {
	srv := testServer(t, &mockWorkflow{}, &mockStore{})
	// A notification has no "id" field
	body := map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("got %d, want 202 for notification", w.Code)
	}
}

// ── unknown method ─────────────────────────────────────────────────────────

func TestMCP_UnknownMethod(t *testing.T) {
	srv := testServer(t, &mockWorkflow{}, &mockStore{})
	w := doPost(t, srv, "test-token", rpcReq("unknown/method", nil))
	resp := decodeResp(t, w)
	if resp.Error == nil {
		t.Error("expected error for unknown method")
	}
	if resp.Error.Code != errCodeMethodUnknown {
		t.Errorf("expected MethodUnknown, got %d", resp.Error.Code)
	}
}

// ── tools/call: get_request_details ────────────────────────────────────────

func TestMCP_GetRequestDetails(t *testing.T) {
	now := time.Now().UTC()
	s := &mockStore{
		getFunc: func(_ context.Context, id string) (*store.RequestRow, error) {
			return &store.RequestRow{
				ID:                id,
				State:             store.StatePending,
				RequesterIdentity: "alice@example.com",
				Provider:          "aws",
				Role:              "prod-admin",
				ResourceScope:     "123456789012",
				DurationSeconds:   3600,
				Reason:            "investigating incident INC-001",
				ApproverTier:      "ai_review",
				CreatedAt:         now,
			}, nil
		},
	}
	srv := testServer(t, &mockWorkflow{}, s)
	w := doPost(t, srv, "test-token", rpcReq("tools/call", map[string]any{
		"name":      "get_request_details",
		"arguments": map[string]any{"request_id": "req_001"},
	}))
	resp := decodeResp(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result, _ := json.Marshal(resp.Result)
	var tc toolCallResult
	if err := json.Unmarshal(result, &tc); err != nil {
		t.Fatalf("unmarshal toolCallResult: %v", err)
	}
	if len(tc.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	// Response should contain the request fields.
	body := tc.Content[0].Text
	for _, want := range []string{"req_001", "aws", "prod-admin", "alice@example.com"} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("expected %q in response, got: %s", want, body)
		}
	}
}

func TestMCP_GetRequestDetails_MissingID(t *testing.T) {
	srv := testServer(t, &mockWorkflow{}, &mockStore{})
	w := doPost(t, srv, "test-token", rpcReq("tools/call", map[string]any{
		"name":      "get_request_details",
		"arguments": map[string]any{},
	}))
	resp := decodeResp(t, w)
	if resp.Error == nil {
		t.Error("expected error for missing request_id")
	}
	if resp.Error.Code != errCodeInvalidParams {
		t.Errorf("expected InvalidParams, got code %d", resp.Error.Code)
	}
}

// ── error paths ────────────────────────────────────────────────────────────

func TestMCP_ListPending_StoreError(t *testing.T) {
	s := &mockStore{
		listFunc: func(_ context.Context) ([]*store.RequestRow, error) {
			return nil, errors.New("db connection lost")
		},
	}
	srv := testServer(t, &mockWorkflow{}, s)
	w := doPost(t, srv, "test-token", rpcReq("tools/call", map[string]any{
		"name": "list_pending_ai_review", "arguments": map[string]any{},
	}))
	resp := decodeResp(t, w)
	if resp.Error == nil {
		t.Error("expected error when store fails")
	}
	if resp.Error.Code != errCodeInternal {
		t.Errorf("expected Internal error code, got %d", resp.Error.Code)
	}
}

func TestMCP_Approve_WorkflowError(t *testing.T) {
	wf := &mockWorkflow{
		approveFunc: func(_ context.Context, _, _, _ string) (*store.RequestRow, error) {
			return nil, errors.New("request already in terminal state")
		},
	}
	srv := testServer(t, wf, &mockStore{})
	w := doPost(t, srv, "test-token", rpcReq("tools/call", map[string]any{
		"name": "approve_request",
		"arguments": map[string]any{
			"request_id": "req_999",
			"reasoning":  "low risk",
		},
	}))
	resp := decodeResp(t, w)
	if resp.Error == nil {
		t.Error("expected error when workflow fails")
	}
	if resp.Error.Code != errCodeInternal {
		t.Errorf("expected Internal error code, got %d", resp.Error.Code)
	}
}
