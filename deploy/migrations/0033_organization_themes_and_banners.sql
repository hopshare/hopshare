ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS theme TEXT NOT NULL DEFAULT 'default',
    ADD COLUMN IF NOT EXISTS banner_content_type TEXT,
    ADD COLUMN IF NOT EXISTS banner_data BYTEA;

UPDATE organizations
SET theme = 'default'
WHERE theme IS NULL
   OR BTRIM(theme) = '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'organizations_theme_check'
    ) THEN
        ALTER TABLE organizations
            ADD CONSTRAINT organizations_theme_check
            CHECK (theme IN ('default', 'bright', 'serious', 'fun'));
    END IF;
END $$;
