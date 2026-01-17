CREATE TABLE messages (
    id BIGSERIAL PRIMARY KEY,
    recipient_member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    sender_member_id BIGINT REFERENCES members(id) ON DELETE SET NULL,
    sender_name TEXT NOT NULL,
    subject TEXT NOT NULL,
    body TEXT NOT NULL,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX messages_recipient_created_at_idx
    ON messages (recipient_member_id, created_at DESC);

CREATE INDEX messages_recipient_unread_idx
    ON messages (recipient_member_id)
    WHERE read_at IS NULL;
