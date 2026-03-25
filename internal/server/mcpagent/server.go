// Copyright © 2026 Yu Technology Group, LLC d/b/a jitsudo
// SPDX-License-Identifier: Elastic-2.0

package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
	"github.com/jitsudo-dev/jitsudo/internal/server/notifications"
	"github.com/jitsudo-dev/jitsudo/internal/store"
)

// Server is the MCP agent-requestor HTTP server. It exposes:
//
//	POST /mcp/agent/messages  — JSON-RPC 2.0 tool endpoint
//	GET  /mcp/agent/sse       — SSE stream for access/resolved push notifications
type Server struct {
	workflow agentWorkflow
	store    agentStore
	verifier authVerifier
	broker   *Broker
}

// New creates a Server. All parameters are required.
func New(wf agentWorkflow, s agentStore, v authVerifier, b *Broker) *Server {
	return &Server{workflow: wf, store: s, verifier: v, broker: b}
}

// Handler returns an http.Handler with the two routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/agent/messages", s.handleMessages)
	mux.HandleFunc("/mcp/agent/sse", s.handleSSE)
	return mux
}

// ─── POST /mcp/agent/messages ─────────────────────────────────────────────

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	identity := authenticate(w, r, s.verifier)
	if identity == nil {
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, nil, errCodeParse, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeError(w, req.ID, errCodeInvalidReq, `jsonrpc must be "2.0"`)
		return
	}

	// Notifications have no id and require no response.
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, rErr := s.dispatchRPC(r.Context(), identity, req)
	if rErr != nil {
		s.writeError(w, req.ID, rErr.Code, rErr.Message)
		return
	}

	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Warn().Err(err).Msg("mcpagent: encode response")
	}
}

func (s *Server) dispatchRPC(ctx context.Context, identity *auth.Identity, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return initializeResult{
			ProtocolVersion: "2025-03-26",
			ServerInfo:      serverInfo{Name: "jitsudo-mcp-agent", Version: "1.0"},
			Capabilities:    capabilities{Tools: &struct{}{}},
		}, nil

	case "tools/list":
		return toolsListResult{Tools: allTools()}, nil

	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: errCodeInvalidParams, Message: "invalid params: " + err.Error()}
		}
		switch p.Name {
		case "request_access":
			return s.toolRequestAccess(ctx, identity, p.Arguments)
		case "list_my_active_grants":
			return s.toolListMyActiveGrants(ctx, identity)
		case "revoke_access":
			return s.toolRevokeAccess(ctx, identity, p.Arguments)
		default:
			return nil, &rpcError{Code: errCodeMethodUnknown, Message: fmt.Sprintf("unknown tool: %s", p.Name)}
		}

	default:
		return nil, &rpcError{Code: errCodeMethodUnknown, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}
}

func (s *Server) writeError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// ─── GET /mcp/agent/sse ───────────────────────────────────────────────────

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	identity := authenticate(w, r, s.verifier)
	if identity == nil {
		return
	}

	requestID := r.URL.Query().Get("request_id")
	if requestID == "" {
		http.Error(w, "request_id query parameter is required", http.StatusBadRequest)
		return
	}

	req, err := s.store.GetRequest(r.Context(), requestID)
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}
	if req.RequesterIdentity != identity.Email {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Already resolved — push the event immediately and close.
	if isResolved(req.State) {
		s.writeResolvedSSE(w, req)
		return
	}

	// Set SSE headers before subscribing so the client sees them immediately.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}

	ch, unsub := s.broker.Subscribe(requestID, identity.Email)
	defer unsub()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			if canFlush {
				flusher.Flush()
			}

		case ev := <-ch:
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: access/resolved\ndata: %s\n\n", data)
			if canFlush {
				flusher.Flush()
			}
			return
		}
	}
}

// writeResolvedSSE writes a single access/resolved SSE event for an already-
// terminal request and sets the appropriate response headers.
func (s *Server) writeResolvedSSE(w http.ResponseWriter, req *store.RequestRow) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ev := sseEvent{
		RequestID: req.ID,
		Outcome:   outcomeFor(eventTypeForState(req.State)),
		State:     string(req.State),
		ExpiresAt: req.ExpiresAt,
	}
	if req.State == store.StateActive {
		ev.Credentials = req.CredentialsJSON
	}

	data, _ := json.Marshal(ev)
	fmt.Fprintf(w, "event: access/resolved\ndata: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// isResolved returns true for terminal states where no further approval action
// is possible and the SSE response can be sent immediately.
func isResolved(state store.RequestState) bool {
	switch state {
	case store.StateActive, store.StateRejected, store.StateExpired, store.StateRevoked:
		return true
	}
	return false
}

// eventTypeForState maps a terminal request state to the notification EventType
// so writeResolvedSSE can reuse outcomeFor.
func eventTypeForState(state store.RequestState) notifications.EventType {
	switch state {
	case store.StateActive:
		return notifications.EventApproved
	case store.StateRejected:
		return notifications.EventDenied
	case store.StateExpired:
		return notifications.EventExpired
	case store.StateRevoked:
		return notifications.EventRevoked
	}
	return ""
}
