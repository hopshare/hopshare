-- Normalize member emails and enforce case-insensitive uniqueness for login identity.
UPDATE members
SET email = LOWER(TRIM(email))
WHERE email <> LOWER(TRIM(email));

ALTER TABLE members
	DROP CONSTRAINT IF EXISTS members_email_key;

CREATE UNIQUE INDEX IF NOT EXISTS members_email_lower_key
	ON members (LOWER(email));

-- Username-based identity has been retired in favor of email identity.
ALTER TABLE members
	DROP CONSTRAINT IF EXISTS members_username_key;

ALTER TABLE members
	DROP COLUMN IF EXISTS username;
