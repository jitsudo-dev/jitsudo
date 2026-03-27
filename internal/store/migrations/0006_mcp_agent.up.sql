-- Migration 0006: pending timeout support for elevation requests.
--
-- These columns are general request lifecycle fields — not MCP-specific.
-- Any request type (human CLI or MCP agent) can have a policy-configured
-- timeout that fires if no approval decision is made within timeout_seconds.
--
-- pending_timeout_at:     when the pending timeout sweeper should fire for this request.
--                         NULL means no timeout applies (request waits indefinitely).
-- pending_timeout_action: what the sweeper does when timeout fires:
--                         'deny'         → transition PENDING → REJECTED
--                         'auto_approve' → auto-approve as if tier=auto
--                         'escalate'     → re-route to human approval queue

ALTER TABLE elevation_requests
    ADD COLUMN pending_timeout_at     TIMESTAMPTZ,
    ADD COLUMN pending_timeout_action TEXT
        CHECK (pending_timeout_action IN ('deny', 'auto_approve', 'escalate'));

-- Partial index covering only rows where the sweeper has work to do.
CREATE INDEX idx_elevation_requests_pending_timeout
    ON elevation_requests(pending_timeout_at)
    WHERE state = 'PENDING' AND pending_timeout_at IS NOT NULL;
