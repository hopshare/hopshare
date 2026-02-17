CREATE TABLE hour_balance_adjustments (
    id BIGSERIAL PRIMARY KEY,
    organization_id BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    admin_member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
    hours_delta INT NOT NULL,
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (hours_delta <> 0),
    CHECK (LENGTH(BTRIM(reason)) > 0)
);

CREATE INDEX hour_balance_adjustments_org_member_created_at_idx
    ON hour_balance_adjustments (organization_id, member_id, created_at DESC);

CREATE INDEX hour_balance_adjustments_admin_created_at_idx
    ON hour_balance_adjustments (admin_member_id, created_at DESC);
