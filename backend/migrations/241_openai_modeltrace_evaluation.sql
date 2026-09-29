-- Allow the ModelTrace attribution evaluation alongside Candy/Fingerprint.
-- Keep this as a forward migration so existing installations retain their
-- schedule and run history.

ALTER TABLE openai_eval_schedule_state
    DROP CONSTRAINT IF EXISTS openai_eval_schedule_state_test_type_check;
ALTER TABLE openai_eval_schedule_state
    ADD CONSTRAINT openai_eval_schedule_state_test_type_check
    CHECK (test_type IN ('candy', 'fingerprint', 'modeltrace'));

ALTER TABLE openai_eval_runs
    DROP CONSTRAINT IF EXISTS openai_eval_runs_test_type_check;
ALTER TABLE openai_eval_runs
    ADD CONSTRAINT openai_eval_runs_test_type_check
    CHECK (test_type IN ('candy', 'fingerprint', 'modeltrace'));
