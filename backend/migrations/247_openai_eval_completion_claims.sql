-- Completion-based automatic schedules; independent leases survive long runs
-- and expire after worker crashes without replaying historical missed ticks.
ALTER TABLE openai_eval_schedule_state
 ADD COLUMN IF NOT EXISTS claim_owner TEXT,
 ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ;
