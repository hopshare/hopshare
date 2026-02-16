CREATE TABLE skill_aliases (
    id BIGSERIAL PRIMARY KEY,
    skill_id BIGINT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    alias TEXT NOT NULL,
    normalized_alias TEXT NOT NULL,
    created_by BIGINT REFERENCES members(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (char_length(btrim(alias)) BETWEEN 1 AND 80),
    CHECK (char_length(btrim(normalized_alias)) BETWEEN 1 AND 80)
);

CREATE UNIQUE INDEX skill_aliases_skill_normalized_alias_unique_idx
    ON skill_aliases (skill_id, normalized_alias);

CREATE INDEX skill_aliases_normalized_alias_idx
    ON skill_aliases (normalized_alias);

WITH default_skills AS (
    SELECT id, LOWER(btrim(name)) AS normalized_name
    FROM skills
    WHERE organization_id IS NULL
)
INSERT INTO skill_aliases (skill_id, alias, normalized_alias)
SELECT ds.id, v.alias, v.normalized_alias
FROM default_skills ds
JOIN (
    VALUES
        ('cooking', 'cooking', 'cooking'),
        ('cooking', 'cook', 'cook'),
        ('cooking', 'meal prep', 'meal prep'),
        ('driving', 'driving', 'driving'),
        ('driving', 'drive', 'drive'),
        ('driving', 'ride', 'ride'),
        ('yard work', 'yard work', 'yard work'),
        ('yard work', 'yard', 'yard'),
        ('yard work', 'lawn', 'lawn'),
        ('tutoring', 'tutoring', 'tutoring'),
        ('tutoring', 'tutor', 'tutor'),
        ('tutoring', 'homework', 'homework'),
        ('pet care', 'pet care', 'pet care'),
        ('pet care', 'pet', 'pet'),
        ('pet care', 'dog walk', 'dog walk'),
        ('reading', 'reading', 'reading'),
        ('reading', 'read', 'read'),
        ('shopping', 'shopping', 'shopping'),
        ('shopping', 'shop', 'shop'),
        ('shopping', 'groceries', 'groceries'),
        ('moving', 'moving', 'moving'),
        ('moving', 'move', 'move'),
        ('moving', 'couch', 'couch'),
        ('moving', 'furniture', 'furniture'),
        ('moving', 'boxes', 'boxes'),
        ('moving', 'lift', 'lift'),
        ('moving', 'haul', 'haul'),
        ('finances', 'finances', 'finances'),
        ('finances', 'finance', 'finance'),
        ('finances', 'budget', 'budget'),
        ('taxes', 'taxes', 'taxes'),
        ('taxes', 'tax', 'tax'),
        ('taxes', 'tax prep', 'tax prep'),
        ('computers', 'computers', 'computers'),
        ('computers', 'computer', 'computer'),
        ('computers', 'pc', 'pc'),
        ('computers', 'tech support', 'tech support'),
        ('child care', 'child care', 'child care'),
        ('child care', 'childcare', 'childcare'),
        ('child care', 'babysitting', 'babysitting'),
        ('keeping company', 'keeping company', 'keeping company'),
        ('keeping company', 'companionship', 'companionship'),
        ('keeping company', 'visit', 'visit'),
        ('handyman jobs', 'handyman jobs', 'handyman jobs'),
        ('handyman jobs', 'handyman', 'handyman'),
        ('handyman jobs', 'repair', 'repair')
) AS v(skill_name, alias, normalized_alias)
    ON ds.normalized_name = v.skill_name
ON CONFLICT DO NOTHING;
