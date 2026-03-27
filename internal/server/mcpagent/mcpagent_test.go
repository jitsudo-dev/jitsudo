// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package mcpagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
	"github.com/jitsudo-dev/jitsudo/internal/server/notifications"
	"github.com/jitsudo-dev/jitsudo/internal/server/workflow"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// ── Stubs ─────────────────────────────────────────────────────────────────────

type stubVerifier struct {
	identity *auth.Identity
	err      error
}

func (v *stubVerifier) Verify(_ context.Context, _ string) (*auth.Identity, error) {
	if v.err != nil {
		return nil, v.err
	}
	cp := *v.identity
	return &cp, nil
}

type stubWorkflow struct {
	createResult *store.RequestRow
	createErr    error
	revokeResult *store.RequestRow
	revokeErr    error
}

func (w *stubWorkflow) CreateMCPRequest(_ context.Context, _ *auth.Identity, _ workflow.MCPRequestInput) (*store.RequestRow, error) {
	return w.createResult, w.createErr
}

func (w *stubWorkflow) RevokeRequest(_ context.Context, _ *auth.Identity, _ string, _ string) (*store.RequestRow, error) {
	return w.revokeResult, w.revokeErr
}

type stubAgentStore struct {
	row       *store.RequestRow
	getErr    error
	grants    []*store.RequestRow
	grantsErr error
}

func (s *stubAgentStore) GetRequest(_ context.Context, _ string) (*store.RequestRow, error) {
	return s.row, s.getErr
}

func (s *stubAgentStore) ListActiveGrantsByIdentity(_ context.Context, _ string) ([]*store.RequestRow, error) {
	return s.grants, s.grantsErr
}

type stubBrokerStore struct {
	row *store.RequestRow
	err error
}

func (s *stubBrokerStore) GetRequest(_ context.Context, _ string) (*store.RequestRow, error) {
	return s.row, s.err
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func okVerifier() *stubVerifier {
	return &stubVerifier{identity: &auth.Identity{Email: "agent@example.com", Subject: "sub-agent"}}
}

func newTestServer(wf agentWorkflow, s agentStore, v authVerifier) *Server {
	broker := NewBroker(&stubBrokerStore{row: &store.RequestRow{}})
	return New(wf, s, v, broker)
}

// rpcBody builds a JSON-RPC 2.0 POST body. id may be nil for notification.
func rpcBody(id any, method string, params any) *bytes.Buffer {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	b, _ := json.Marshal(m)
	return bytes.NewBuffer(b)
}

func postMessages(srv *Server, v authVerifier, body *bytes.Buffer) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp/agent/messages", body)
	if v != nil {
		req.Header.Set("Authorization", "Bearer valid-token")
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func decodeRPCResponse(t *testing.T, w *httptest.ResponseRecorder) rpcResponse {
	t.Helper()
	var resp rpcResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode rpcResponse: %v", err)
	}
	return resp
}

// ── authenticate ──────────────────────────────────────────────────────────────

func TestAuthenticate_MissingHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	id := authenticate(w, req, okVerifier())
	if id != nil {
		t.Error("expected nil identity for missing Authorization header")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthenticate_NoBearerPrefix(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Token abc123")
	w := httptest.NewRecorder()
	id := authenticate(w, req, okVerifier())
	if id != nil {
		t.Error("expected nil identity for non-Bearer token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthenticate_VerifyError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	w := httptest.NewRecorder()
	v := &stubVerifier{err: errors.New("token expired")}
	id := authenticate(w, req, v)
	if id != nil {
		t.Error("expected nil identity on verify error")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthenticate_Success(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer good-token")
	w := httptest.NewRecorder()
	id := authenticate(w, req, okVerifier())
	if id == nil {
		t.Fatal("expected non-nil identity")
	}
	if id.Email != "agent@example.com" {
		t.Errorf("Email = %q, want %q", id.Email, "agent@example.com")
	}
	if id.PrincipalType != auth.PrincipalTypeAgent {
		t.Errorf("PrincipalType = %q, want %q", id.PrincipalType, auth.PrincipalTypeAgent)
	}
}

// ── handleMessages ────────────────────────────────────────────────────────────

func TestHandleMessages_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
	req := httptest.NewRequest(http.MethodGet, "/mcp/agent/messages", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleMessages_AuthFailure(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, &stubVerifier{err: errors.New("bad")})
	req := httptest.NewRequest(http.MethodPost, "/mcp/agent/messages", rpcBody(1, "initialize", nil))
	req.Header.Set("Authorization", "Bearer bad")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleMessages_InvalidJSON(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
	w := postMessages(srv, okVerifier(), bytes.NewBufferString("{bad json"))
	resp := decodeRPCResponse(t, w)
	if resp.Error == nil || resp.Error.Code != errCodeParse {
		t.Errorf("want errCodeParse (%d), got %v", errCodeParse, resp.Error)
	}
}

func TestHandleMessages_BadVersion(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
	body, _ := json.Marshal(map[string]any{"jsonrpc": "1.0", "id": 1, "method": "initialize"})
	w := postMessages(srv, okVerifier(), bytes.NewBuffer(body))
	resp := decodeRPCResponse(t, w)
	if resp.Error == nil || resp.Error.Code != errCodeInvalidReq {
		t.Errorf("want errCodeInvalidReq (%d), got %v", errCodeInvalidReq, resp.Error)
	}
}

func TestHandleMessages_Notification(t *testing.T) {
	// Notifications have null id — server must return 202 with no body.
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	req := httptest.NewRequest(http.MethodPost, "/mcp/agent/messages", bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer valid-token")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", w.Body.String())
	}
}

func TestHandleMessages_Initialize(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
	w := postMessages(srv, okVerifier(), rpcBody(1, "initialize", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := decodeRPCResponse(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var result initializeResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ServerInfo.Name != "jitsudo-mcp-agent" {
		t.Errorf("ServerInfo.Name = %q, want %q", result.ServerInfo.Name, "jitsudo-mcp-agent")
	}
	if result.Capabilities.Tools == nil {
		t.Error("expected non-nil Capabilities.Tools")
	}
}

func TestHandleMessages_ToolsList(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
	w := postMessages(srv, okVerifier(), rpcBody(1, "tools/list", nil))
	resp := decodeRPCResponse(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var result toolsListResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Tools) != 3 {
		t.Errorf("len(Tools) = %d, want 3", len(result.Tools))
	}
}

func TestHandleMessages_UnknownMethod(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
	w := postMessages(srv, okVerifier(), rpcBody(1, "foo/bar", nil))
	resp := decodeRPCResponse(t, w)
	if resp.Error == nil || resp.Error.Code != errCodeMethodUnknown {
		t.Errorf("want errCodeMethodUnknown (%d), got %v", errCodeMethodUnknown, resp.Error)
	}
}

// ── toolRequestAccess ─────────────────────────────────────────────────────────

func toolsCallBody(id any, toolName string, args map[string]any) *bytes.Buffer {
	return rpcBody(id, "tools/call", map[string]any{"name": toolName, "arguments": args})
}

func TestToolRequestAccess_MissingRequiredArg(t *testing.T) {
	fullArgs := map[string]any{
		"action":           "s3:GetObject",
		"resource":         "arn:aws:s3:::my-bucket/*",
		"reason":           "need data",
		"duration_seconds": 3600.0,
	}
	for _, missing := range []string{"action", "resource", "reason", "duration_seconds"} {
		t.Run("missing_"+missing, func(t *testing.T) {
			args := make(map[string]any, len(fullArgs))
			for k, v := range fullArgs {
				if k != missing {
					args[k] = v
				}
			}
			srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
			w := postMessages(srv, okVerifier(), toolsCallBody(1, "request_access", args))
			resp := decodeRPCResponse(t, w)
			if resp.Error == nil || resp.Error.Code != errCodeInvalidParams {
				t.Errorf("want errCodeInvalidParams (%d) for missing %q, got %v", errCodeInvalidParams, missing, resp.Error)
			}
		})
	}
}

func TestToolRequestAccess_WorkflowError(t *testing.T) {
	wf := &stubWorkflow{createErr: errors.New("policy denied")}
	srv := newTestServer(wf, &stubAgentStore{}, okVerifier())
	args := map[string]any{"action": "s3:Get", "resource": "arn:aws:s3:::b", "reason": "r", "duration_seconds": 3600.0}
	w := postMessages(srv, okVerifier(), toolsCallBody(1, "request_access", args))
	resp := decodeRPCResponse(t, w)
	if resp.Error == nil || resp.Error.Code != errCodeInternal {
		t.Errorf("want errCodeInternal (%d), got %v", errCodeInternal, resp.Error)
	}
}

func TestToolRequestAccess_Pending(t *testing.T) {
	now := time.Now().UTC()
	wf := &stubWorkflow{createResult: &store.RequestRow{
		ID: "req_001", State: store.StatePending, CreatedAt: now, UpdatedAt: now,
	}}
	srv := newTestServer(wf, &stubAgentStore{}, okVerifier())
	args := map[string]any{"action": "s3:Get", "resource": "arn:aws:s3:::b", "reason": "r", "duration_seconds": 3600.0}
	w := postMessages(srv, okVerifier(), toolsCallBody(1, "request_access", args))
	resp := decodeRPCResponse(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var result toolCallResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["status"] != "pending" {
		t.Errorf("status = %q, want %q", payload["status"], "pending")
	}
	if payload["request_id"] != "req_001" {
		t.Errorf("request_id = %q, want %q", payload["request_id"], "req_001")
	}
}

func TestToolRequestAccess_AutoApproved(t *testing.T) {
	now := time.Now().UTC()
	exp := now.Add(time.Hour)
	wf := &stubWorkflow{createResult: &store.RequestRow{
		ID:              "req_002",
		State:           store.StateActive,
		CredentialsJSON: map[string]string{"AWS_ACCESS_KEY_ID": "AKIAIOSFODNN7EXAMPLE"},
		ExpiresAt:       &exp,
		CreatedAt:       now,
		UpdatedAt:       now,
	}}
	srv := newTestServer(wf, &stubAgentStore{}, okVerifier())
	args := map[string]any{"action": "s3:Get", "resource": "arn:aws:s3:::b", "reason": "r", "duration_seconds": 3600.0}
	w := postMessages(srv, okVerifier(), toolsCallBody(1, "request_access", args))
	resp := decodeRPCResponse(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var result toolCallResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["status"] != "active" {
		t.Errorf("status = %q, want %q", payload["status"], "active")
	}
	if payload["credentials"] == nil {
		t.Error("expected credentials in auto-approved response")
	}
	if payload["expires_at"] == nil {
		t.Error("expected expires_at in auto-approved response")
	}
}

// ── toolListMyActiveGrants ────────────────────────────────────────────────────

func TestToolListMyActiveGrants_Empty(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{grants: nil}, okVerifier())
	w := postMessages(srv, okVerifier(), toolsCallBody(1, "list_my_active_grants", map[string]any{}))
	resp := decodeRPCResponse(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var result toolCallResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &items); err != nil {
		t.Fatalf("unmarshal items: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty list, got %d items", len(items))
	}
}

func TestToolListMyActiveGrants_WithGrants(t *testing.T) {
	now := time.Now().UTC()
	exp := now.Add(time.Hour)
	grants := []*store.RequestRow{
		{ID: "req_a", Provider: "aws", Role: "readonly", ResourceScope: "123456789012", ExpiresAt: &exp},
		{ID: "req_b", Provider: "gcp", Role: "viewer", ResourceScope: "my-project"},
	}
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{grants: grants}, okVerifier())
	w := postMessages(srv, okVerifier(), toolsCallBody(1, "list_my_active_grants", map[string]any{}))
	resp := decodeRPCResponse(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var result toolCallResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &items); err != nil {
		t.Fatalf("unmarshal items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0]["id"] != "req_a" {
		t.Errorf("items[0].id = %v, want req_a", items[0]["id"])
	}
	if items[0]["expires_at"] == nil {
		t.Error("expected expires_at on req_a")
	}
	if items[1]["id"] != "req_b" {
		t.Errorf("items[1].id = %v, want req_b", items[1]["id"])
	}
	if _, hasExp := items[1]["expires_at"]; hasExp {
		t.Error("expected no expires_at on req_b (nil ExpiresAt)")
	}
}

func TestToolListMyActiveGrants_StoreError(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{grantsErr: errors.New("db down")}, okVerifier())
	w := postMessages(srv, okVerifier(), toolsCallBody(1, "list_my_active_grants", map[string]any{}))
	resp := decodeRPCResponse(t, w)
	if resp.Error == nil || resp.Error.Code != errCodeInternal {
		t.Errorf("want errCodeInternal (%d), got %v", errCodeInternal, resp.Error)
	}
}

// ── toolRevokeAccess ──────────────────────────────────────────────────────────

func TestToolRevokeAccess_MissingRequestID(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
	w := postMessages(srv, okVerifier(), toolsCallBody(1, "revoke_access", map[string]any{}))
	resp := decodeRPCResponse(t, w)
	if resp.Error == nil || resp.Error.Code != errCodeInvalidParams {
		t.Errorf("want errCodeInvalidParams (%d), got %v", errCodeInvalidParams, resp.Error)
	}
}

func TestToolRevokeAccess_Success(t *testing.T) {
	now := time.Now().UTC()
	wf := &stubWorkflow{revokeResult: &store.RequestRow{
		ID: "req_001", State: store.StateRevoked, CreatedAt: now, UpdatedAt: now,
	}}
	srv := newTestServer(wf, &stubAgentStore{}, okVerifier())
	args := map[string]any{"request_id": "req_001", "reason": "done early"}
	w := postMessages(srv, okVerifier(), toolsCallBody(1, "revoke_access", args))
	resp := decodeRPCResponse(t, w)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	b, _ := json.Marshal(resp.Result)
	var result toolCallResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["status"] != "revoked" {
		t.Errorf("status = %q, want %q", payload["status"], "revoked")
	}
}

func TestToolRevokeAccess_WorkflowError(t *testing.T) {
	wf := &stubWorkflow{revokeErr: errors.New("not found")}
	srv := newTestServer(wf, &stubAgentStore{}, okVerifier())
	args := map[string]any{"request_id": "req_missing"}
	w := postMessages(srv, okVerifier(), toolsCallBody(1, "revoke_access", args))
	resp := decodeRPCResponse(t, w)
	if resp.Error == nil || resp.Error.Code != errCodeInternal {
		t.Errorf("want errCodeInternal (%d), got %v", errCodeInternal, resp.Error)
	}
}

// ── handleSSE ─────────────────────────────────────────────────────────────────

func sseRequest(method, requestID string, v *stubVerifier) *http.Request {
	target := "/mcp/agent/sse"
	if requestID != "" {
		target = fmt.Sprintf("/mcp/agent/sse?request_id=%s", requestID)
	}
	req := httptest.NewRequest(method, target, nil)
	if v != nil {
		req.Header.Set("Authorization", "Bearer valid-token")
	}
	return req
}

func TestHandleSSE_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
	req := sseRequest(http.MethodPost, "req_001", okVerifier())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleSSE_AuthFailure(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, &stubVerifier{err: errors.New("bad")})
	req := sseRequest(http.MethodGet, "req_001", okVerifier())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleSSE_MissingRequestID(t *testing.T) {
	srv := newTestServer(&stubWorkflow{}, &stubAgentStore{}, okVerifier())
	req := sseRequest(http.MethodGet, "", okVerifier())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleSSE_RequestNotFound(t *testing.T) {
	s := &stubAgentStore{getErr: store.ErrNotFound}
	srv := newTestServer(&stubWorkflow{}, s, okVerifier())
	req := sseRequest(http.MethodGet, "req_missing", okVerifier())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleSSE_Forbidden(t *testing.T) {
	s := &stubAgentStore{row: &store.RequestRow{
		ID:                "req_001",
		State:             store.StatePending,
		RequesterIdentity: "other@example.com", // different from agent@example.com
	}}
	srv := newTestServer(&stubWorkflow{}, s, okVerifier())
	req := sseRequest(http.MethodGet, "req_001", okVerifier())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestHandleSSE_AlreadyActive(t *testing.T) {
	creds := map[string]string{"AWS_ACCESS_KEY_ID": "AKIAIOSFODNN7EXAMPLE"}
	s := &stubAgentStore{row: &store.RequestRow{
		ID:                "req_001",
		State:             store.StateActive,
		RequesterIdentity: "agent@example.com",
		CredentialsJSON:   creds,
	}}
	srv := newTestServer(&stubWorkflow{}, s, okVerifier())
	req := sseRequest(http.MethodGet, "req_001", okVerifier())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Result().Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", w.Result().Header.Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: access/resolved") {
		t.Errorf("expected SSE event in body, got: %q", body)
	}
	if !strings.Contains(body, `"outcome":"approved"`) {
		t.Errorf("expected outcome=approved in body, got: %q", body)
	}
	if !strings.Contains(body, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("expected credentials in body, got: %q", body)
	}
}

func TestHandleSSE_AlreadyRejected(t *testing.T) {
	s := &stubAgentStore{row: &store.RequestRow{
		ID:                "req_002",
		State:             store.StateRejected,
		RequesterIdentity: "agent@example.com",
	}}
	srv := newTestServer(&stubWorkflow{}, s, okVerifier())
	req := sseRequest(http.MethodGet, "req_002", okVerifier())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"outcome":"denied"`) {
		t.Errorf("expected outcome=denied in body, got: %q", body)
	}
}

// ── Broker ────────────────────────────────────────────────────────────────────

func TestBrokerSubscribe_ChannelBuffered(t *testing.T) {
	b := NewBroker(&stubBrokerStore{})
	ch, unsub := b.Subscribe("req_001", "agent@example.com")
	defer unsub()
	if cap(ch) != 1 {
		t.Errorf("channel cap = %d, want 1", cap(ch))
	}
}

func TestBrokerSubscribe_Unsubscribe(t *testing.T) {
	b := NewBroker(&stubBrokerStore{})
	_, unsub := b.Subscribe("req_001", "agent@example.com")
	unsub()

	b.mu.RLock()
	_, exists := b.subs["req_001"]
	b.mu.RUnlock()
	if exists {
		t.Error("expected requestID to be removed from subs map after unsubscribe")
	}
}

func TestBrokerNotify_NonResolutionEvent(t *testing.T) {
	b := NewBroker(&stubBrokerStore{row: &store.RequestRow{ID: "req_001"}})
	ch, unsub := b.Subscribe("req_001", "agent@example.com")
	defer unsub()

	_ = b.Notify(context.Background(), notifications.Event{
		Type:      notifications.EventRequestCreated,
		RequestID: "req_001",
	})

	select {
	case ev := <-ch:
		t.Errorf("expected no event for non-resolution type, got %v", ev)
	default:
		// correct: channel is empty
	}
}

func TestBrokerNotify_Approved(t *testing.T) {
	creds := map[string]string{"token": "abc"}
	bs := &stubBrokerStore{row: &store.RequestRow{
		ID:              "req_001",
		State:           store.StateActive,
		CredentialsJSON: creds,
	}}
	b := NewBroker(bs)
	ch, unsub := b.Subscribe("req_001", "agent@example.com")
	defer unsub()

	_ = b.Notify(context.Background(), notifications.Event{
		Type:      notifications.EventApproved,
		RequestID: "req_001",
	})

	select {
	case ev := <-ch:
		if ev.Outcome != "approved" {
			t.Errorf("Outcome = %q, want approved", ev.Outcome)
		}
		if ev.Credentials["token"] != "abc" {
			t.Errorf("Credentials = %v, want token=abc", ev.Credentials)
		}
	default:
		t.Error("expected event on channel, got nothing")
	}
}

func TestBrokerNotify_Denied(t *testing.T) {
	bs := &stubBrokerStore{row: &store.RequestRow{ID: "req_002", State: store.StateRejected}}
	b := NewBroker(bs)
	ch, unsub := b.Subscribe("req_002", "agent@example.com")
	defer unsub()

	_ = b.Notify(context.Background(), notifications.Event{
		Type:      notifications.EventDenied,
		RequestID: "req_002",
	})

	select {
	case ev := <-ch:
		if ev.Outcome != "denied" {
			t.Errorf("Outcome = %q, want denied", ev.Outcome)
		}
		if len(ev.Credentials) != 0 {
			t.Errorf("expected no credentials for denied, got %v", ev.Credentials)
		}
	default:
		t.Error("expected event on channel, got nothing")
	}
}

func TestBrokerNotify_StoreError(t *testing.T) {
	bs := &stubBrokerStore{err: errors.New("db down")}
	b := NewBroker(bs)
	ch, unsub := b.Subscribe("req_003", "agent@example.com")
	defer unsub()

	// Must not panic; must still deliver a minimal event.
	_ = b.Notify(context.Background(), notifications.Event{
		Type:      notifications.EventApproved,
		RequestID: "req_003",
	})

	select {
	case ev := <-ch:
		if ev.Outcome != "approved" {
			t.Errorf("Outcome = %q, want approved", ev.Outcome)
		}
		// Credentials should be nil/empty since the store fetch failed.
		if len(ev.Credentials) != 0 {
			t.Errorf("expected no credentials after store error, got %v", ev.Credentials)
		}
	default:
		t.Error("expected minimal event even on store error")
	}
}
