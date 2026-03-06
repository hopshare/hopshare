ALTER TABLE members
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS members_deleted_at_idx
    ON members (deleted_at)
    WHERE deleted_at IS NOT NULL;
