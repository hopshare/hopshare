ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS timebank_min_balance INT NOT NULL DEFAULT -5,
    ADD COLUMN IF NOT EXISTS timebank_max_balance INT NOT NULL DEFAULT 10,
    ADD COLUMN IF NOT EXISTS timebank_starting_balance INT NOT NULL DEFAULT 5;

ALTER TABLE hour_balance_adjustments
    ADD COLUMN IF NOT EXISTS is_starting_balance BOOLEAN NOT NULL DEFAULT FALSE;
