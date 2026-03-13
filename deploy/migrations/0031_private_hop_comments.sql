ALTER TABLE hop_comments
    ADD COLUMN IF NOT EXISTS private_to_member_id BIGINT REFERENCES members(id) ON DELETE CASCADE;

ALTER TABLE hop_comments
    DROP CONSTRAINT IF EXISTS hop_comments_private_distinct_member_chk;

ALTER TABLE hop_comments
    ADD CONSTRAINT hop_comments_private_distinct_member_chk
    CHECK (private_to_member_id IS NULL OR private_to_member_id <> member_id);

CREATE INDEX IF NOT EXISTS hop_comments_hop_private_to_created_idx
    ON hop_comments (hop_id, private_to_member_id, created_at DESC);
