-- Principal enrollment table.
-- Stores the trust tier (0-4) and last-seen timestamp for each principal.
-- All values default to tier 0 (unknown/unverified); admins promote principals explicitly.
CREATE TABLE principals (
    identity     TEXT PRIMARY KEY,
    trust_tier   INTEGER NOT NULL DEFAULT 0
                     CHECK (trust_tier BETWEEN 0 AND 4),
    enrolled_by  TEXT    NOT NULL DEFAULT '',
    enrolled_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    notes        TEXT    NOT NULL DEFAULT ''
);
