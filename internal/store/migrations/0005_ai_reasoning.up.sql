-- Stores AI agent reasoning for Tier 2 (ai_review) approval decisions.
-- Populated by the MCP approver interface; NULL for human and auto approvals.
ALTER TABLE elevation_requests
    ADD COLUMN ai_reasoning_json TEXT;
