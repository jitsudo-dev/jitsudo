-- Seed permissive development policies.
-- ONLY applied when JITSUDOD_SEED_POLICIES=true.
-- These allow all requests and all approvals — for local development only.

INSERT INTO policies (id, name, type, rego, description, enabled, updated_by)
VALUES (
    'pol_dev_eligibility',
    'dev-allow-all-eligibility',
    'ELIGIBILITY',
    E'package jitsudo.eligibility\n\ndefault allow := true\nreason := "development: all requests allowed"',
    'Development-only permissive eligibility policy. Remove before production.',
    TRUE,
    'seed'
),
(
    'pol_dev_approval',
    'dev-allow-all-approval',
    'APPROVAL',
    E'package jitsudo.approval\n\ndefault allow := true\nreason := "development: all approvals allowed"',
    'Development-only permissive approval policy. Remove before production.',
    TRUE,
    'seed'
)
ON CONFLICT (name) DO NOTHING;
