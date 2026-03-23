// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

// Package mcp implements an MCP (Model Context Protocol) server for the
// jitsudo Tier 2 AI approver interface. AI agents connect via the Streamable
// HTTP transport, call tools to review pending requests, and emit approve,
// deny, or escalate decisions — with full reasoning captured in the audit log.
//
// Authentication: Bearer token (not OIDC; agents use static API keys).
// Mount point: POST /mcp (handled by the HTTP mux in server.go).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// workflowEngine is the subset of *workflow.Engine methods used by the MCP server.
type workflowEngine interface {
	AIApproveRequest(ctx context.Context, requestID, agentIdentity, reasoning string) (*store.RequestRow, error)
	AIDenyRequest(ctx context.Context, requestID, agentIdentity, reasoning string) (*store.RequestRow, error)
	AIEscalateRequest(ctx context.Context, requestID, agentIdentity, reasoning string) (*store.RequestRow, error)
}

// storeReader is the subset of *store.Store methods used for read queries.
type storeReader interface {
	ListPendingAIReview(ctx context.Context) ([]*store.RequestRow, error)
	GetRequest(ctx context.Context, id string) (*store.RequestRow, error)
}

// Server implements the MCP Streamable HTTP transport for the AI approver interface.
type Server struct {
	workflow      workflowEngine
	store         storeReader
	token         string // required Bearer token; empty = disabled
	agentIdentity string // identity recorded in audit log for this MCP server instance
}

// New creates an MCP Server. token must be non-empty to enable the endpoint;
// agentIdentity is the name recorded in the audit log for AI decisions.
func New(wf workflowEngine, s storeReader, token, agentIdentity string) *Server {
	if agentIdentity == "" {
		agentIdentity = "mcp-agent"
	}
	return &Server{workflow: wf, store: s, token: token, agentIdentity: agentIdentity}
}

// ─── JSON-RPC 2.0 types ────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // string | number | null
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	errCodeParse         = -32700
	errCodeInvalidReq    = -32600
	errCodeMethodUnknown = -32601
	errCodeInvalidParams = -32602
	errCodeInternal      = -32603
)

// ─── MCP protocol types ────────────────────────────────────────────────────

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type capabilities struct {
	Tools *struct{} `json:"tools,omitempty"`
}

type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	ServerInfo      serverInfo   `json:"serverInfo"`
	Capabilities    capabilities `json:"capabilities"`
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ─── ServeHTTP ─────────────────────────────────────────────────────────────

// ServeHTTP handles a single MCP Streamable HTTP request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.token == "" {
		http.Error(w, "MCP endpoint not configured", http.StatusNotFound)
		return
	}

	// Auth: Bearer token.
	if !s.checkAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, nil, errCodeParse, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeError(w, req.ID, errCodeInvalidReq, "jsonrpc must be \"2.0\"")
		return
	}

	// Notifications have no id and require no response (return 202).
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, rErr := s.dispatch(r.Context(), req)
	if rErr != nil {
		s.writeError(w, req.ID, rErr.Code, rErr.Message)
		return
	}

	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Warn().Err(err).Msg("mcp: encode response")
	}
}

func (s *Server) checkAuth(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	return strings.EqualFold(strings.TrimPrefix(auth, "Bearer "), s.token) && s.token != ""
}

func (s *Server) writeError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors are always 200
	_ = json.NewEncoder(w).Encode(resp)
}

// ─── Dispatcher ────────────────────────────────────────────────────────────

func (s *Server) dispatch(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize()
	case "tools/list":
		return s.handleToolsList()
	case "tools/call":
		return s.handleToolsCall(ctx, req.Params)
	default:
		return nil, &rpcError{Code: errCodeMethodUnknown, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}
}

// ─── MCP method handlers ───────────────────────────────────────────────────

func (s *Server) handleInitialize() (any, *rpcError) {
	return initializeResult{
		ProtocolVersion: "2025-03-26",
		ServerInfo:      serverInfo{Name: "jitsudo-mcp", Version: "1.0"},
		Capabilities:    capabilities{Tools: &struct{}{}},
	}, nil
}

func (s *Server) handleToolsList() (any, *rpcError) {
	return toolsListResult{Tools: allTools()}, nil
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "invalid params: " + err.Error()}
	}

	switch p.Name {
	case "list_pending_ai_review":
		return s.toolListPending(ctx)
	case "get_request_details":
		return s.toolGetRequest(ctx, p.Arguments)
	case "approve_request":
		return s.toolApprove(ctx, p.Arguments)
	case "deny_request":
		return s.toolDeny(ctx, p.Arguments)
	case "escalate_to_human":
		return s.toolEscalate(ctx, p.Arguments)
	default:
		return nil, &rpcError{Code: errCodeMethodUnknown, Message: fmt.Sprintf("unknown tool: %s", p.Name)}
	}
}

// ─── Tool implementations ──────────────────────────────────────────────────

func (s *Server) toolListPending(ctx context.Context) (any, *rpcError) {
	reqs, err := s.store.ListPendingAIReview(ctx)
	if err != nil {
		return nil, &rpcError{Code: errCodeInternal, Message: err.Error()}
	}
	type summary struct {
		ID              string `json:"id"`
		RequesterID     string `json:"requester_identity"`
		Provider        string `json:"provider"`
		Role            string `json:"role"`
		ResourceScope   string `json:"resource_scope"`
		DurationSeconds int64  `json:"duration_seconds"`
		Reason          string `json:"reason"`
		CreatedAt       string `json:"created_at"`
	}
	items := make([]summary, 0, len(reqs))
	for _, r := range reqs {
		items = append(items, summary{
			ID:              r.ID,
			RequesterID:     r.RequesterIdentity,
			Provider:        r.Provider,
			Role:            r.Role,
			ResourceScope:   r.ResourceScope,
			DurationSeconds: r.DurationSeconds,
			Reason:          r.Reason,
			CreatedAt:       r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	txt, _ := json.Marshal(items)
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(txt)}}}, nil
}

func (s *Server) toolGetRequest(ctx context.Context, args map[string]any) (any, *rpcError) {
	id, ok := stringArg(args, "request_id")
	if !ok {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "request_id is required"}
	}
	req, err := s.store.GetRequest(ctx, id)
	if err != nil {
		return nil, &rpcError{Code: errCodeInternal, Message: err.Error()}
	}
	txt, _ := json.Marshal(req)
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(txt)}}}, nil
}

func (s *Server) toolApprove(ctx context.Context, args map[string]any) (any, *rpcError) {
	id, ok := stringArg(args, "request_id")
	if !ok {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "request_id is required"}
	}
	reasoning, _ := stringArg(args, "reasoning")
	reasoningJSON := buildReasoningJSON(reasoning, args)

	req, err := s.workflow.AIApproveRequest(ctx, id, s.agentIdentity, reasoningJSON)
	if err != nil {
		return nil, &rpcError{Code: errCodeInternal, Message: err.Error()}
	}
	txt, _ := json.Marshal(map[string]string{
		"status":     "approved",
		"request_id": req.ID,
		"state":      string(req.State),
	})
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(txt)}}}, nil
}

func (s *Server) toolDeny(ctx context.Context, args map[string]any) (any, *rpcError) {
	id, ok := stringArg(args, "request_id")
	if !ok {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "request_id is required"}
	}
	reasoning, _ := stringArg(args, "reasoning")
	reasoningJSON := buildReasoningJSON(reasoning, args)

	req, err := s.workflow.AIDenyRequest(ctx, id, s.agentIdentity, reasoningJSON)
	if err != nil {
		return nil, &rpcError{Code: errCodeInternal, Message: err.Error()}
	}
	txt, _ := json.Marshal(map[string]string{
		"status":     "denied",
		"request_id": req.ID,
		"state":      string(req.State),
	})
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(txt)}}}, nil
}

func (s *Server) toolEscalate(ctx context.Context, args map[string]any) (any, *rpcError) {
	id, ok := stringArg(args, "request_id")
	if !ok {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "request_id is required"}
	}
	reasoning, _ := stringArg(args, "reasoning")
	reasoningJSON := buildReasoningJSON(reasoning, args)

	req, err := s.workflow.AIEscalateRequest(ctx, id, s.agentIdentity, reasoningJSON)
	if err != nil {
		return nil, &rpcError{Code: errCodeInternal, Message: err.Error()}
	}
	txt, _ := json.Marshal(map[string]string{
		"status":        "escalated",
		"request_id":    req.ID,
		"approver_tier": req.ApproverTier,
	})
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(txt)}}}, nil
}

// ─── Helpers ───────────────────────────────────────────────────────────────

func stringArg(args map[string]any, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// buildReasoningJSON marshals the reasoning into a JSON object for audit storage.
// If reasoning is already valid JSON it is stored as-is; otherwise it is wrapped.
func buildReasoningJSON(reasoning string, args map[string]any) string {
	if reasoning == "" {
		if b, err := json.Marshal(args); err == nil {
			return string(b)
		}
		return "{}"
	}
	// If the reasoning string is already valid JSON, return it directly.
	if json.Valid([]byte(reasoning)) {
		return reasoning
	}
	b, _ := json.Marshal(map[string]string{"reasoning": reasoning})
	return string(b)
}

// ─── Tool definitions (schema) ─────────────────────────────────────────────

func allTools() []toolDef {
	strProp := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}

	return []toolDef{
		{
			Name:        "list_pending_ai_review",
			Description: "Returns all PENDING elevation requests routed to AI review (approver_tier=ai_review). Review each one and call approve_request, deny_request, or escalate_to_human.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "get_request_details",
			Description: "Returns full details for a single elevation request, including requester identity, provider, role, resource scope, duration, reason, and metadata.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"request_id": strProp("The elevation request ID (e.g. req_01J8KZ...)")},
				"required":   []string{"request_id"},
			},
		},
		{
			Name:        "approve_request",
			Description: "Approve a Tier 2 elevation request. Credentials are issued immediately. Always provide reasoning — it is stored in the tamper-evident audit log.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"request_id": strProp("The elevation request ID to approve"),
					"reasoning":  strProp("JSON or text explaining the approval decision: risk assessment, blast radius, context signals consulted"),
				},
				"required": []string{"request_id", "reasoning"},
			},
		},
		{
			Name:        "deny_request",
			Description: "Deny a Tier 2 elevation request. Always provide reasoning — it is stored in the audit log and shown to the requester.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"request_id": strProp("The elevation request ID to deny"),
					"reasoning":  strProp("JSON or text explaining the denial decision"),
				},
				"required": []string{"request_id", "reasoning"},
			},
		},
		{
			Name:        "escalate_to_human",
			Description: "Escalate a Tier 2 request to human review when uncertain. The request stays PENDING and is routed to the human approval queue. Always escalate on uncertainty — never approve or deny silently on model error.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"request_id": strProp("The elevation request ID to escalate"),
					"reasoning":  strProp("JSON or text explaining why human review is needed (uncertainty, high blast radius, missing context, etc.)"),
				},
				"required": []string{"request_id", "reasoning"},
			},
		},
	}
}
