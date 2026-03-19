-- jitsudo initial schema
-- License: Elastic License 2.0 (ELv2)

-- Request lifecycle states
CREATE TYPE request_state AS ENUM (
    'PENDING',
    'APPROVED',
    'REJECTED',
    'ACTIVE',
    'EXPIRED',
    'REVOKED'
);

-- Policy types
CREATE TYPE policy_type AS ENUM (
    'ELIGIBILITY',
    'APPROVAL'
);

-- Core request table: one row per elevation request, state column drives the machine.
CREATE TABLE elevation_requests (
    id                  TEXT PRIMARY KEY,
    state               request_state NOT NULL DEFAULT 'PENDING',
    requester_identity  TEXT NOT NULL,
    provider            TEXT NOT NULL,
    role                TEXT NOT NULL,
    resource_scope      TEXT NOT NULL DEFAULT '',
    duration_seconds    BIGINT NOT NULL,
    reason              TEXT NOT NULL DEFAULT '',
    break_glass         BOOLEAN NOT NULL DEFAULT FALSE,
    metadata            JSONB NOT NULL DEFAULT '{}',
    approver_identity   TEXT,
    approver_comment    TEXT,
    expires_at          TIMESTAMPTZ,
    revoke_token        TEXT,
    credentials_json    JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_elevation_requests_state ON elevation_requests(state);
CREATE INDEX idx_elevation_requests_requester ON elevation_requests(requester_identity);
CREATE INDEX idx_elevation_requests_expires_at
    ON elevation_requests(expires_at)
    WHERE state = 'ACTIVE';

-- Policies: Rego source stored in DB, loaded into OPA at startup and on upsert.
CREATE TABLE policies (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    type        policy_type NOT NULL,
    rego        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    updated_by  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_policies_type_enabled ON policies(type, enabled);

-- Audit log: append-only with SHA-256 hash chain.
CREATE TABLE audit_events (
    id              BIGSERIAL PRIMARY KEY,
    timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_identity  TEXT NOT NULL,
    action          TEXT NOT NULL,
    request_id      TEXT,
    provider        TEXT NOT NULL DEFAULT '',
    resource_scope  TEXT NOT NULL DEFAULT '',
    outcome         TEXT NOT NULL,
    details_json    TEXT NOT NULL DEFAULT '{}',
    prev_hash       TEXT NOT NULL DEFAULT '',
    hash            TEXT NOT NULL
);

CREATE INDEX idx_audit_events_request_id ON audit_events(request_id);
CREATE INDEX idx_audit_events_actor ON audit_events(actor_identity);
CREATE INDEX idx_audit_events_timestamp ON audit_events(timestamp);
