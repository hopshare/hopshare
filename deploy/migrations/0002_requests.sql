-- Request lifecycle tables: requests + credit transfers.

CREATE TABLE requests (
    id BIGSERIAL PRIMARY KEY,
    organization_id BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,

    title TEXT NOT NULL,
    details TEXT,
    estimated_hours INT NOT NULL,

    needed_by_kind TEXT NOT NULL CHECK (needed_by_kind IN ('anytime', 'on', 'around', 'no_later_than')),
    needed_by_date DATE,
    expires_at TIMESTAMPTZ,

    status TEXT NOT NULL CHECK (status IN ('open', 'accepted', 'canceled', 'expired', 'completed')),

    accepted_by BIGINT REFERENCES members(id),
    accepted_at TIMESTAMPTZ,
    canceled_by BIGINT REFERENCES members(id),
    canceled_at TIMESTAMPTZ,
    completed_by BIGINT REFERENCES members(id),
    completed_at TIMESTAMPTZ,
    completed_hours INT,
    completion_comment TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (accepted_at IS NULL OR accepted_at >= created_at),
    CHECK (canceled_at IS NULL OR canceled_at >= created_at),
    CHECK (completed_at IS NULL OR completed_at >= created_at)
);

CREATE INDEX requests_org_status_created_at_idx
    ON requests (organization_id, status, created_at DESC);

CREATE INDEX requests_org_created_by_created_at_idx
    ON requests (organization_id, created_by, created_at DESC);

CREATE INDEX requests_org_accepted_by_created_at_idx
    ON requests (organization_id, accepted_by, created_at DESC);

CREATE TABLE request_transactions (
    id BIGSERIAL PRIMARY KEY,
    organization_id BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    request_id BIGINT NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
    from_member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    to_member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    hours INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (from_member_id <> to_member_id)
);

-- One credit transfer per completed request.
CREATE UNIQUE INDEX request_transactions_request_id_uniq
    ON request_transactions (request_id);

CREATE INDEX request_transactions_org_to_idx
    ON request_transactions (organization_id, to_member_id);

CREATE INDEX request_transactions_org_from_idx
    ON request_transactions (organization_id, from_member_id);

