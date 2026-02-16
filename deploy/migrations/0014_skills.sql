CREATE TABLE skills (
    id BIGSERIAL PRIMARY KEY,
    organization_id BIGINT REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_by BIGINT REFERENCES members(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (char_length(btrim(name)) BETWEEN 1 AND 80)
);

CREATE UNIQUE INDEX skills_default_name_unique_idx
    ON skills (LOWER(btrim(name)))
    WHERE organization_id IS NULL;

CREATE UNIQUE INDEX skills_org_name_unique_idx
    ON skills (organization_id, LOWER(btrim(name)))
    WHERE organization_id IS NOT NULL;

CREATE TABLE member_skills (
    member_id BIGINT NOT NULL REFERENCES members(id) ON DELETE CASCADE,
    skill_id BIGINT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    selected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (member_id, skill_id)
);

CREATE INDEX member_skills_skill_id_idx
    ON member_skills (skill_id);

INSERT INTO skills (organization_id, name)
VALUES
    (NULL, 'Cooking'),
    (NULL, 'Driving'),
    (NULL, 'Yard Work'),
    (NULL, 'Tutoring'),
    (NULL, 'Pet Care'),
    (NULL, 'Reading'),
    (NULL, 'Shopping'),
    (NULL, 'Moving'),
    (NULL, 'Finances'),
    (NULL, 'Taxes'),
    (NULL, 'Computers'),
    (NULL, 'Child Care'),
    (NULL, 'Keeping Company'),
    (NULL, 'Handyman Jobs')
ON CONFLICT DO NOTHING;
