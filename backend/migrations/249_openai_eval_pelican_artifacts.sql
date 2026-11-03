-- New diagnostics preserve all historical runs and the bytes of earlier migrations.
ALTER TABLE openai_eval_runs DROP CONSTRAINT IF EXISTS openai_eval_runs_test_type_check;
ALTER TABLE openai_eval_runs ADD CONSTRAINT openai_eval_runs_test_type_check
    CHECK (test_type IN ('candy', 'fingerprint', 'modeltrace', 'state_probe', 'pelican'));

CREATE TABLE IF NOT EXISTS openai_eval_artifacts (
    run_id BIGINT PRIMARY KEY REFERENCES openai_eval_runs(id) ON DELETE CASCADE,
    kind VARCHAR(24) NOT NULL CHECK (kind = 'pelican_html'),
    content TEXT NOT NULL CHECK (octet_length(content) <= 300000),
    sha256 VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Old ModelTrace history stays readable; only its retired runner is disabled.
UPDATE openai_eval_schedule_state
SET enabled = FALSE, next_run_at = NULL
WHERE test_type = 'modeltrace';
