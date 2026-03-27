// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
	"github.com/jitsudo-dev/jitsudo/internal/server/workflow"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// agentWorkflow is the subset of *workflow.Engine used by the tool handlers.
type agentWorkflow interface {
	CreateMCPRequest(ctx context.Context, identity *auth.Identity, input workflow.MCPRequestInput) (*store.RequestRow, error)
	RevokeRequest(ctx context.Context, actor *auth.Identity, requestID, reason string) (*store.RequestRow, error)
}

// agentStore is the subset of *store.Store used by the tool handlers.
type agentStore interface {
	GetRequest(ctx context.Context, id string) (*store.RequestRow, error)
	ListActiveGrantsByIdentity(ctx context.Context, identity string) ([]*store.RequestRow, error)
}

// ─── JSON-RPC 2.0 types ────────────────────────────────────────────────────
// Copied from internal/server/mcp/mcp.go to avoid cross-package coupling.

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
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

// ─── Tool implementations ──────────────────────────────────────────────────

func (s *Server) toolRequestAccess(ctx context.Context, identity *auth.Identity, args map[string]any) (any, *rpcError) {
	action, ok := stringArg(args, "action")
	if !ok {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "action is required"}
	}
	resource, ok := stringArg(args, "resource")
	if !ok {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "resource is required"}
	}
	reason, ok := stringArg(args, "reason")
	if !ok {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "reason is required"}
	}
	durationSeconds, ok := int64Arg(args, "duration_seconds")
	if !ok {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "duration_seconds is required and must be a number"}
	}
	ticketRef, _ := stringArg(args, "ticket_ref")

	req, err := s.workflow.CreateMCPRequest(ctx, identity, workflow.MCPRequestInput{
		Action:          action,
		Resource:        resource,
		DurationSeconds: durationSeconds,
		Reason:          reason,
		TicketRef:       ticketRef,
	})
	if err != nil {
		return nil, &rpcError{Code: errCodeInternal, Message: err.Error()}
	}

	var payload map[string]any
	if req.State == store.StateActive {
		// Auto-approved: return credentials immediately.
		payload = map[string]any{
			"status":      "active",
			"request_id":  req.ID,
			"credentials": req.CredentialsJSON,
		}
		if req.ExpiresAt != nil {
			payload["expires_at"] = req.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
		}
	} else {
		payload = map[string]any{
			"status":     "pending",
			"request_id": req.ID,
			"message":    fmt.Sprintf("Request is pending approval. Open GET /mcp/agent/sse?request_id=%s to wait for the resolution notification.", req.ID),
		}
	}

	txt, _ := json.Marshal(payload)
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(txt)}}}, nil
}

func (s *Server) toolListMyActiveGrants(ctx context.Context, identity *auth.Identity) (any, *rpcError) {
	grants, err := s.store.ListActiveGrantsByIdentity(ctx, identity.Email)
	if err != nil {
		return nil, &rpcError{Code: errCodeInternal, Message: err.Error()}
	}

	type grantSummary struct {
		ID            string `json:"id"`
		Provider      string `json:"provider"`
		Role          string `json:"role"`
		ResourceScope string `json:"resource_scope"`
		ExpiresAt     string `json:"expires_at,omitempty"`
	}
	items := make([]grantSummary, 0, len(grants))
	for _, g := range grants {
		s := grantSummary{
			ID:            g.ID,
			Provider:      g.Provider,
			Role:          g.Role,
			ResourceScope: g.ResourceScope,
		}
		if g.ExpiresAt != nil {
			s.ExpiresAt = g.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		items = append(items, s)
	}

	txt, _ := json.Marshal(items)
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(txt)}}}, nil
}

func (s *Server) toolRevokeAccess(ctx context.Context, identity *auth.Identity, args map[string]any) (any, *rpcError) {
	requestID, ok := stringArg(args, "request_id")
	if !ok {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "request_id is required"}
	}
	reason, _ := stringArg(args, "reason")

	req, err := s.workflow.RevokeRequest(ctx, identity, requestID, reason)
	if err != nil {
		return nil, &rpcError{Code: errCodeInternal, Message: err.Error()}
	}

	txt, _ := json.Marshal(map[string]string{
		"status":     "revoked",
		"request_id": req.ID,
		"state":      string(req.State),
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

// int64Arg extracts an integer argument from a JSON-decoded map.
// JSON numbers decode as float64, so we accept both float64 and int64.
func int64Arg(args map[string]any, key string) (int64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}

// ─── Tool schema definitions ───────────────────────────────────────────────

func allTools() []toolDef {
	strProp := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	numProp := func(desc string) map[string]any {
		return map[string]any{"type": "number", "description": desc}
	}

	return []toolDef{
		{
			Name:        "request_access",
			Description: "Request temporary elevated access to a resource. Returns immediately with a request_id. If auto-approved, credentials are included in the response. Otherwise open the SSE stream to wait for approval.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":           strProp("The action to perform (e.g. \"s3:GetObject\", \"sts:AssumeRole\")"),
					"resource":         strProp("The resource ARN or identifier (e.g. \"arn:aws:s3:::my-bucket/*\")"),
					"duration_seconds": numProp("How long the access should last in seconds"),
					"reason":           strProp("Justification for the access request"),
					"ticket_ref":       strProp("Optional incident or ticket reference (e.g. \"INC-1234\")"),
				},
				"required": []string{"action", "resource", "duration_seconds", "reason"},
			},
		},
		{
			Name:        "list_my_active_grants",
			Description: "List all currently active (ACTIVE state) access grants for the authenticated identity.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "revoke_access",
			Description: "Revoke an active access grant before it expires. Only the original requester may revoke their own grant.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"request_id": strProp("The request ID to revoke (e.g. \"req_01J8KZ...\")"),
					"reason":     strProp("Optional reason for revoking early"),
				},
				"required": []string{"request_id"},
			},
		},
	}
}
