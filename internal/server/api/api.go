// Package api contains the REST and gRPC request handlers for the jitsudod control plane.
// Handlers are generated from the protobuf service definition and wired up in server.go.
//
// License: Elastic License 2.0 (ELv2)
package api

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	jitsudov1alpha1 "github.com/jitsudo-dev/jitsudo/internal/gen/proto/go/jitsudo/v1alpha1"
	"github.com/jitsudo-dev/jitsudo/internal/server/audit"
	"github.com/jitsudo-dev/jitsudo/internal/server/auth"
	"github.com/jitsudo-dev/jitsudo/internal/server/policy"
	"github.com/jitsudo-dev/jitsudo/internal/server/workflow"
	"github.com/jitsudo-dev/jitsudo/internal/store"
	"github.com/oklog/ulid/v2"
)

// Handler implements jitsudov1alpha1.JitsudoServiceServer.
type Handler struct {
	jitsudov1alpha1.UnimplementedJitsudoServiceServer
	workflow *workflow.Engine
	store    *store.Store
	audit    *audit.Logger
	policy   *policy.Engine
}

// NewHandler wires together the service dependencies.
func NewHandler(w *workflow.Engine, s *store.Store, a *audit.Logger, p *policy.Engine) *Handler {
	return &Handler{workflow: w, store: s, audit: a, policy: p}
}

// ── Elevation Request RPCs ────────────────────────────────────────────────────

func (h *Handler) CreateRequest(ctx context.Context, in *jitsudov1alpha1.CreateRequestInput) (*jitsudov1alpha1.CreateRequestResponse, error) {
	identity := auth.FromContext(ctx)
	if identity == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	req, err := h.workflow.CreateRequest(ctx, identity, in)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return &jitsudov1alpha1.CreateRequestResponse{Request: requestToProto(req)}, nil
}

func (h *Handler) GetRequest(ctx context.Context, in *jitsudov1alpha1.GetRequestInput) (*jitsudov1alpha1.GetRequestResponse, error) {
	if auth.FromContext(ctx) == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	req, err := h.store.GetRequest(ctx, in.GetId())
	if err != nil {
		return nil, storeErr(err)
	}
	return &jitsudov1alpha1.GetRequestResponse{Request: requestToProto(req)}, nil
}

func (h *Handler) ListRequests(ctx context.Context, in *jitsudov1alpha1.ListRequestsFilter) (*jitsudov1alpha1.ListRequestsResponse, error) {
	identity := auth.FromContext(ctx)
	if identity == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	f := store.ListFilter{
		RequesterIdentity: in.GetRequesterIdentity(),
	}
	if in.GetState() != jitsudov1alpha1.RequestState_REQUEST_STATE_UNSPECIFIED {
		f.State = protoStateToStore(in.GetState())
	}
	rows, err := h.store.ListRequests(ctx, f)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list requests: %v", err)
	}
	out := make([]*jitsudov1alpha1.ElevationRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, requestToProto(r))
	}
	return &jitsudov1alpha1.ListRequestsResponse{Requests: out}, nil
}

func (h *Handler) ApproveRequest(ctx context.Context, in *jitsudov1alpha1.ApproveRequestInput) (*jitsudov1alpha1.ApproveRequestResponse, error) {
	identity := auth.FromContext(ctx)
	if identity == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	req, err := h.workflow.ApproveRequest(ctx, identity, in.GetRequestId(), in.GetComment())
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	return &jitsudov1alpha1.ApproveRequestResponse{Request: requestToProto(req)}, nil
}

func (h *Handler) DenyRequest(ctx context.Context, in *jitsudov1alpha1.DenyRequestInput) (*jitsudov1alpha1.DenyRequestResponse, error) {
	identity := auth.FromContext(ctx)
	if identity == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	req, err := h.workflow.DenyRequest(ctx, identity, in.GetRequestId(), in.GetReason())
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	return &jitsudov1alpha1.DenyRequestResponse{Request: requestToProto(req)}, nil
}

func (h *Handler) GetCredentials(ctx context.Context, in *jitsudov1alpha1.GetCredentialsInput) (*jitsudov1alpha1.GetCredentialsResponse, error) {
	identity := auth.FromContext(ctx)
	if identity == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	creds, err := h.workflow.GetCredentials(ctx, identity, in.GetRequestId())
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	credList := make([]*jitsudov1alpha1.Credential, 0, len(creds))
	for k, v := range creds {
		credList = append(credList, &jitsudov1alpha1.Credential{Name: k, Value: v})
	}
	req, err := h.store.GetRequest(ctx, in.GetRequestId())
	if err != nil {
		return nil, storeErr(err)
	}
	grant := &jitsudov1alpha1.ElevationGrant{
		RequestId:   in.GetRequestId(),
		Credentials: credList,
	}
	if req.ExpiresAt != nil {
		grant.ExpiresAt = timestamppb.New(*req.ExpiresAt)
	}
	return &jitsudov1alpha1.GetCredentialsResponse{Grant: grant}, nil
}

// ── Policy RPCs ───────────────────────────────────────────────────────────────

func (h *Handler) ListPolicies(ctx context.Context, in *jitsudov1alpha1.ListPoliciesInput) (*jitsudov1alpha1.ListPoliciesResponse, error) {
	if auth.FromContext(ctx) == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	rows, err := h.store.ListPolicies(ctx, nil)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list policies: %v", err)
	}
	out := make([]*jitsudov1alpha1.Policy, 0, len(rows))
	for _, p := range rows {
		out = append(out, policyToProto(p))
	}
	return &jitsudov1alpha1.ListPoliciesResponse{Policies: out}, nil
}

func (h *Handler) GetPolicy(ctx context.Context, in *jitsudov1alpha1.GetPolicyInput) (*jitsudov1alpha1.GetPolicyResponse, error) {
	if auth.FromContext(ctx) == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	p, err := h.store.GetPolicy(ctx, in.GetId())
	if err != nil {
		return nil, storeErr(err)
	}
	return &jitsudov1alpha1.GetPolicyResponse{Policy: policyToProto(p)}, nil
}

func (h *Handler) ApplyPolicy(ctx context.Context, in *jitsudov1alpha1.ApplyPolicyInput) (*jitsudov1alpha1.ApplyPolicyResponse, error) {
	identity := auth.FromContext(ctx)
	if identity == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	row := &store.PolicyRow{
		ID:          "pol_" + ulid.Make().String(),
		Name:        in.GetName(),
		Type:        protoPolicyTypeToStore(in.GetType()),
		Rego:        in.GetRego(),
		Description: in.GetDescription(),
		Enabled:     in.GetEnabled(),
		UpdatedBy:   identity.Email,
	}
	saved, err := h.store.UpsertPolicy(ctx, row)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "apply policy: %v", err)
	}
	// Reload OPA engine so the new policy takes effect immediately.
	if err := h.policy.Reload(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "policy reload: %v", err)
	}
	return &jitsudov1alpha1.ApplyPolicyResponse{Policy: policyToProto(saved)}, nil
}

func (h *Handler) DeletePolicy(ctx context.Context, in *jitsudov1alpha1.DeletePolicyInput) (*jitsudov1alpha1.DeletePolicyResponse, error) {
	if auth.FromContext(ctx) == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	if err := h.store.DeletePolicy(ctx, in.GetId()); err != nil {
		return nil, storeErr(err)
	}
	if err := h.policy.Reload(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "policy reload: %v", err)
	}
	return &jitsudov1alpha1.DeletePolicyResponse{}, nil
}

// ── Audit RPC ─────────────────────────────────────────────────────────────────

func (h *Handler) QueryAudit(ctx context.Context, in *jitsudov1alpha1.QueryAuditInput) (*jitsudov1alpha1.QueryAuditResponse, error) {
	if auth.FromContext(ctx) == nil {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	f := store.AuditFilter{
		ActorIdentity: in.GetActorIdentity(),
		RequestID:     in.GetRequestId(),
		Provider:      in.GetProvider(),
		Limit:         int(in.GetPageSize()),
	}
	if in.GetSince() != nil {
		t := in.GetSince().AsTime()
		f.Since = &t
	}
	if in.GetUntil() != nil {
		t := in.GetUntil().AsTime()
		f.Until = &t
	}
	rows, err := h.store.QueryAuditEvents(ctx, f)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "query audit: %v", err)
	}
	out := make([]*jitsudov1alpha1.AuditEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, auditEventToProto(r))
	}
	return &jitsudov1alpha1.QueryAuditResponse{Events: out}, nil
}

// ── Proto conversion helpers ──────────────────────────────────────────────────

func requestToProto(r *store.RequestRow) *jitsudov1alpha1.ElevationRequest {
	p := &jitsudov1alpha1.ElevationRequest{
		Id:                r.ID,
		State:             storeStateToProto(r.State),
		RequesterIdentity: r.RequesterIdentity,
		Provider:          r.Provider,
		Role:              r.Role,
		ResourceScope:     r.ResourceScope,
		DurationSeconds:   r.DurationSeconds,
		Reason:            r.Reason,
		BreakGlass:        r.BreakGlass,
		Metadata:          r.Metadata,
		ApproverIdentity:  r.ApproverIdentity,
		ApproverComment:   r.ApproverComment,
		CreatedAt:         timestamppb.New(r.CreatedAt),
		UpdatedAt:         timestamppb.New(r.UpdatedAt),
	}
	if r.ExpiresAt != nil {
		p.ExpiresAt = timestamppb.New(*r.ExpiresAt)
	}
	return p
}

func policyToProto(p *store.PolicyRow) *jitsudov1alpha1.Policy {
	return &jitsudov1alpha1.Policy{
		Id:          p.ID,
		Name:        p.Name,
		Type:        storePolicyTypeToProto(p.Type),
		Rego:        p.Rego,
		Description: p.Description,
		Enabled:     p.Enabled,
		CreatedAt:   timestamppb.New(p.CreatedAt),
		UpdatedAt:   timestamppb.New(p.UpdatedAt),
	}
}

func auditEventToProto(e *store.AuditEventRow) *jitsudov1alpha1.AuditEvent {
	return &jitsudov1alpha1.AuditEvent{
		Id:            e.ID,
		Timestamp:     timestamppb.New(e.Timestamp),
		ActorIdentity: e.ActorIdentity,
		Action:        e.Action,
		RequestId:     e.RequestID,
		Provider:      e.Provider,
		ResourceScope: e.ResourceScope,
		Outcome:       e.Outcome,
		DetailsJson:   e.DetailsJSON,
		PrevHash:      e.PrevHash,
		Hash:          e.Hash,
	}
}

func storeStateToProto(s store.RequestState) jitsudov1alpha1.RequestState {
	switch s {
	case store.StatePending:
		return jitsudov1alpha1.RequestState_REQUEST_STATE_PENDING
	case store.StateApproved:
		return jitsudov1alpha1.RequestState_REQUEST_STATE_APPROVED
	case store.StateRejected:
		return jitsudov1alpha1.RequestState_REQUEST_STATE_REJECTED
	case store.StateActive:
		return jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE
	case store.StateExpired:
		return jitsudov1alpha1.RequestState_REQUEST_STATE_EXPIRED
	case store.StateRevoked:
		return jitsudov1alpha1.RequestState_REQUEST_STATE_REVOKED
	default:
		return jitsudov1alpha1.RequestState_REQUEST_STATE_UNSPECIFIED
	}
}

func protoStateToStore(s jitsudov1alpha1.RequestState) store.RequestState {
	switch s {
	case jitsudov1alpha1.RequestState_REQUEST_STATE_PENDING:
		return store.StatePending
	case jitsudov1alpha1.RequestState_REQUEST_STATE_APPROVED:
		return store.StateApproved
	case jitsudov1alpha1.RequestState_REQUEST_STATE_REJECTED:
		return store.StateRejected
	case jitsudov1alpha1.RequestState_REQUEST_STATE_ACTIVE:
		return store.StateActive
	case jitsudov1alpha1.RequestState_REQUEST_STATE_EXPIRED:
		return store.StateExpired
	case jitsudov1alpha1.RequestState_REQUEST_STATE_REVOKED:
		return store.StateRevoked
	default:
		return ""
	}
}

func storePolicyTypeToProto(t store.PolicyType) jitsudov1alpha1.PolicyType {
	switch t {
	case store.PolicyTypeEligibility:
		return jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY
	case store.PolicyTypeApproval:
		return jitsudov1alpha1.PolicyType_POLICY_TYPE_APPROVAL
	default:
		return jitsudov1alpha1.PolicyType_POLICY_TYPE_UNSPECIFIED
	}
}

func protoPolicyTypeToStore(t jitsudov1alpha1.PolicyType) store.PolicyType {
	switch t {
	case jitsudov1alpha1.PolicyType_POLICY_TYPE_ELIGIBILITY:
		return store.PolicyTypeEligibility
	case jitsudov1alpha1.PolicyType_POLICY_TYPE_APPROVAL:
		return store.PolicyTypeApproval
	default:
		return store.PolicyTypeEligibility
	}
}

func storeErr(err error) error {
	if err == store.ErrNotFound {
		return status.Error(codes.NotFound, err.Error())
	}
	return status.Errorf(codes.Internal, "%v", err)
}

// timePtr is a helper used in timestamp conversions.
func timePtr(t time.Time) *time.Time {
	return &t
}

// Ensure timePtr is referenced (avoids unused warning if not used directly).
var _ = fmt.Sprintf
var _ = timePtr
