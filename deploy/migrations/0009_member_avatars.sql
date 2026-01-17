ALTER TABLE members
    ADD COLUMN IF NOT EXISTS avatar_content_type TEXT,
    ADD COLUMN IF NOT EXISTS avatar_data BYTEA;
