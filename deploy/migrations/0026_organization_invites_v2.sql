ALTER TABLE organization_invitations
    ADD COLUMN IF NOT EXISTS token_id TEXT,
    ADD COLUMN IF NOT EXISTS token_hash TEXT,
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS sent_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS accepted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS accepted_member_id BIGINT REFERENCES members(id) ON DELETE SET NULL;

UPDATE organization_invitations
SET accepted_at = COALESCE(accepted_at, responded_at)
WHERE status = 'accepted' AND accepted_at IS NULL;

ALTER TABLE organization_invitations
    ADD CONSTRAINT organization_invitations_token_hash_len_check
    CHECK (token_hash IS NULL OR char_length(token_hash) = 64);

DROP INDEX IF EXISTS organization_invitations_pending_email_idx;
CREATE UNIQUE INDEX IF NOT EXISTS organization_invitations_pending_email_lower_idx
    ON organization_invitations (organization_id, LOWER(invited_email))
    WHERE status = 'pending' AND invited_email IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS organization_invitations_token_id_key
    ON organization_invitations (token_id)
    WHERE token_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS organization_invitations_org_sent_at_idx
    ON organization_invitations (organization_id, sent_at DESC)
    WHERE sent_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS organization_invitations_status_expires_idx
    ON organization_invitations (status, expires_at)
    WHERE status = 'pending' AND expires_at IS NOT NULL;
