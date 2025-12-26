-- Add last-login tracking and store organization logos as BLOBs.

ALTER TABLE members
    ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS logo_content_type TEXT,
    ADD COLUMN IF NOT EXISTS logo_data BYTEA;

ALTER TABLE organizations
    DROP COLUMN IF EXISTS logo_url;

