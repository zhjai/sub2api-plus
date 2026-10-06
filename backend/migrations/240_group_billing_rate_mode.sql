-- Customer billing multiplier source for groups.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS billing_rate_mode VARCHAR(20) NOT NULL DEFAULT 'group';

UPDATE groups
SET billing_rate_mode = 'group'
WHERE billing_rate_mode IS NULL
   OR billing_rate_mode NOT IN ('group', 'account');

ALTER TABLE groups
    DROP CONSTRAINT IF EXISTS chk_groups_billing_rate_mode;

ALTER TABLE groups
    ADD CONSTRAINT chk_groups_billing_rate_mode
    CHECK (billing_rate_mode IN ('group', 'account'));
