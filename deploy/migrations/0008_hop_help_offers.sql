CREATE TABLE hop_help_offers (
    hop_id BIGINT NOT NULL REFERENCES hops(id) ON DELETE CASCADE,
    member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    offered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status TEXT CHECK (status IN ('accepted', 'denied')),
    accepted_at TIMESTAMPTZ,
    denied_at TIMESTAMPTZ,
    PRIMARY KEY (hop_id, member_id),
    CHECK (
        (status IS NULL AND accepted_at IS NULL AND denied_at IS NULL)
        OR (status = 'accepted' AND accepted_at IS NOT NULL AND denied_at IS NULL)
        OR (status = 'denied' AND denied_at IS NOT NULL AND accepted_at IS NULL)
    )
);
