ALTER TABLE messages
    ADD COLUMN message_type TEXT NOT NULL DEFAULT 'information'
    CHECK (message_type IN ('information', 'action'));

ALTER TABLE messages
    ADD COLUMN hop_id BIGINT REFERENCES hops(id) ON DELETE SET NULL;

ALTER TABLE messages
    ADD COLUMN action_status TEXT
    CHECK (action_status IN ('accepted', 'declined'));

ALTER TABLE messages
    ADD COLUMN action_taken_at TIMESTAMPTZ;

CREATE INDEX messages_action_hop_idx
    ON messages (hop_id)
    WHERE message_type = 'action';
