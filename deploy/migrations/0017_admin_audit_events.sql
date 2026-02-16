CREATE TABLE admin_audit_events (
    id BIGSERIAL PRIMARY KEY,
    actor_member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    reason TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (LENGTH(BTRIM(action)) > 0),
    CHECK (LENGTH(BTRIM(target)) > 0),
    CHECK (reason IS NULL OR LENGTH(BTRIM(reason)) > 0)
);

CREATE INDEX admin_audit_events_actor_created_at_idx
    ON admin_audit_events (actor_member_id, created_at DESC);

CREATE INDEX admin_audit_events_action_created_at_idx
    ON admin_audit_events (action, created_at DESC);
