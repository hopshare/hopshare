ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS url_name TEXT;

WITH normalized AS (
    SELECT
        id,
        COALESCE(NULLIF(TRIM(BOTH '-' FROM regexp_replace(lower(name), '[^a-z0-9]+', '-', 'g')), ''), 'organization') AS base_slug
    FROM organizations
),
bounded AS (
    SELECT
        id,
        COALESCE(NULLIF(TRIM(BOTH '-' FROM LEFT(base_slug, 63)), ''), 'organization') AS base_slug
    FROM normalized
),
ranked AS (
    SELECT
        id,
        base_slug,
        ROW_NUMBER() OVER (PARTITION BY base_slug ORDER BY id) AS slug_ordinal
    FROM bounded
)
UPDATE organizations o
SET url_name = CASE
    WHEN r.slug_ordinal = 1 THEN r.base_slug
    ELSE COALESCE(NULLIF(TRIM(BOTH '-' FROM LEFT(r.base_slug, GREATEST(1, 63 - LENGTH('-' || r.slug_ordinal::TEXT)))), ''), 'organization') || '-' || r.slug_ordinal::TEXT
END
FROM ranked r
WHERE o.id = r.id
  AND (o.url_name IS NULL OR o.url_name = '');

ALTER TABLE organizations
    ALTER COLUMN url_name SET NOT NULL;

ALTER TABLE organizations
    ADD CONSTRAINT organizations_url_name_format_check
    CHECK (LENGTH(url_name) BETWEEN 1 AND 63 AND url_name ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$');

CREATE UNIQUE INDEX IF NOT EXISTS organizations_url_name_key
    ON organizations (url_name);
