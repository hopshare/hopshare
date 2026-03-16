ALTER TABLE hops
    ADD COLUMN IF NOT EXISTS hop_kind TEXT NOT NULL DEFAULT 'ask';

ALTER TABLE hops
    DROP CONSTRAINT IF EXISTS hops_hop_kind_check;

ALTER TABLE hops
    ADD CONSTRAINT hops_hop_kind_check
    CHECK (hop_kind IN ('ask', 'offer'));

ALTER TABLE hops
    RENAME COLUMN created_by TO created_user;

ALTER TABLE hops
    RENAME COLUMN accepted_by TO matched_user;

ALTER TABLE hops
    RENAME COLUMN needed_by_kind TO when_kind;

ALTER TABLE hops
    RENAME COLUMN needed_by_date TO when_at;

ALTER INDEX IF EXISTS hops_org_created_by_created_at_idx
    RENAME TO hops_org_created_user_created_at_idx;

ALTER INDEX IF EXISTS hops_org_accepted_by_created_at_idx
    RENAME TO hops_org_matched_user_created_at_idx;
