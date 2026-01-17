ALTER TABLE members
    ADD COLUMN current_organization BIGINT REFERENCES organizations(id) ON DELETE SET NULL;
