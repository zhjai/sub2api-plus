-- Persist the requested Candy sample count independently from fingerprint mode.
-- Existing schedules default to one sample, matching the manual-run default.
ALTER TABLE openai_eval_schedule_state
    ADD COLUMN IF NOT EXISTS sample_count INTEGER NOT NULL DEFAULT 1;

ALTER TABLE openai_eval_schedule_state
    DROP CONSTRAINT IF EXISTS openai_eval_schedule_state_sample_count_check;
ALTER TABLE openai_eval_schedule_state
    ADD CONSTRAINT openai_eval_schedule_state_sample_count_check
    CHECK (sample_count >= 0 AND sample_count <= 10);
