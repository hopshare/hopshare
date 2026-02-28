CREATE TABLE member_sessions (
    id BIGSERIAL PRIMARY KEY,
    token_id TEXT NOT NULL UNIQUE,
    token_hash TEXT NOT NULL,
    member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_activity_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ,
    idle_expires_at TIMESTAMPTZ,
    CHECK (char_length(token_id) = 32),
    CHECK (char_length(token_hash) = 64),
    CHECK (absolute_expires_at IS NULL OR absolute_expires_at > created_at),
    CHECK (idle_expires_at IS NULL OR idle_expires_at > created_at)
);

CREATE INDEX member_sessions_member_created_idx
    ON member_sessions (member_id, created_at DESC);

CREATE INDEX member_sessions_absolute_expires_idx
    ON member_sessions (absolute_expires_at)
    WHERE absolute_expires_at IS NOT NULL;

CREATE INDEX member_sessions_idle_expires_idx
    ON member_sessions (idle_expires_at)
    WHERE idle_expires_at IS NOT NULL;
