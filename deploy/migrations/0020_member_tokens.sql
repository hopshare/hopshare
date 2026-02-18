CREATE TABLE member_tokens (
    id BIGSERIAL PRIMARY KEY,
    token_id TEXT NOT NULL UNIQUE,
    member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    requested_ip INET,
    CHECK (purpose IN ('password_reset', 'email_verification')),
    CHECK (char_length(token_id) >= 16),
    CHECK (char_length(token_hash) = 64),
    CHECK (expires_at > created_at)
);

CREATE INDEX member_tokens_member_purpose_created_idx
    ON member_tokens (member_id, purpose, created_at DESC);

CREATE INDEX member_tokens_active_expires_idx
    ON member_tokens (purpose, expires_at)
    WHERE used_at IS NULL;
