-- State Probe is diagnostic only; it shares the existing evaluation history.
ALTER TABLE openai_eval_schedule_state
    DROP CONSTRAINT IF EXISTS openai_eval_schedule_state_test_type_check;
ALTER TABLE openai_eval_schedule_state
    ADD CONSTRAINT openai_eval_schedule_state_test_type_check
    CHECK (test_type IN ('candy', 'fingerprint', 'modeltrace', 'state_probe'));

ALTER TABLE openai_eval_runs
    DROP CONSTRAINT IF EXISTS openai_eval_runs_test_type_check;
ALTER TABLE openai_eval_runs
    ADD CONSTRAINT openai_eval_runs_test_type_check
    CHECK (test_type IN ('candy', 'fingerprint', 'modeltrace', 'state_probe'));
