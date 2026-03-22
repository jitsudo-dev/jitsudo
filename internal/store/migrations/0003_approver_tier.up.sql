-- Add approval-tier routing column to elevation_requests.
-- 'human' is the backwards-compatible default — all existing rows keep human-approval semantics.
-- Valid values: 'auto' (policy-driven), 'ai_review' (MCP agent, Milestone 4), 'human' (default).
ALTER TABLE elevation_requests
    ADD COLUMN approver_tier TEXT NOT NULL DEFAULT 'human'
        CHECK (approver_tier IN ('auto', 'ai_review', 'human'));
