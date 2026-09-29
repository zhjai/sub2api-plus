-- OpenAI Candy/Fingerprint evaluation configuration, run history, audit and leases.
-- Raw model responses and credentials are intentionally not persisted.

CREATE TABLE IF NOT EXISTS openai_eval_configs (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    config JSONB NOT NULL DEFAULT '{"effects_enabled":false,"accounts":[]}'::jsonb,
    updated_by BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO openai_eval_configs (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS openai_eval_schedule_state (
    account_id BIGINT NOT NULL,
    test_type VARCHAR(24) NOT NULL CHECK (test_type IN ('candy', 'fingerprint')),
    requested_model VARCHAR(200) NOT NULL,
    reasoning_effort VARCHAR(24) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    interval_seconds INTEGER NOT NULL DEFAULT 0 CHECK (interval_seconds >= 0),
    jitter_seconds INTEGER NOT NULL DEFAULT 0 CHECK (jitter_seconds >= 0),
    sample_mode VARCHAR(24) NOT NULL DEFAULT '',
    last_run_at TIMESTAMPTZ,
    next_run_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (account_id, test_type, requested_model, reasoning_effort)
);
CREATE INDEX IF NOT EXISTS idx_openai_eval_schedule_due
    ON openai_eval_schedule_state (next_run_at)
    WHERE enabled = TRUE;

CREATE TABLE IF NOT EXISTS openai_eval_runs (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL,
    test_type VARCHAR(24) NOT NULL CHECK (test_type IN ('candy', 'fingerprint')),
    requested_model VARCHAR(200) NOT NULL,
    upstream_model VARCHAR(200) NOT NULL DEFAULT '',
    reasoning_effort VARCHAR(24) NOT NULL DEFAULT '',
    data_version VARCHAR(100) NOT NULL,
    baseline_version VARCHAR(120) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL,
    outcome JSONB NOT NULL DEFAULT '{}'::jsonb,
    samples JSONB NOT NULL DEFAULT '[]'::jsonb,
    request_count INTEGER NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cost_estimate_usd DOUBLE PRECISION CHECK (cost_estimate_usd IS NULL OR cost_estimate_usd >= 0),
    duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    triggered_by BIGINT NOT NULL DEFAULT 0,
    trigger_source VARCHAR(24) NOT NULL CHECK (trigger_source IN ('manual', 'scheduled')),
    error_code VARCHAR(80) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retention_until TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days')
);

CREATE INDEX IF NOT EXISTS idx_openai_eval_runs_route_history
    ON openai_eval_runs (account_id, requested_model, reasoning_effort, test_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_openai_eval_runs_created_at ON openai_eval_runs (created_at);
CREATE INDEX IF NOT EXISTS idx_openai_eval_runs_retention ON openai_eval_runs (retention_until);

CREATE TABLE IF NOT EXISTS openai_eval_audit_events (
    id BIGSERIAL PRIMARY KEY,
    actor_id BIGINT NOT NULL DEFAULT 0,
    action VARCHAR(80) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_openai_eval_audit_created_at ON openai_eval_audit_events (created_at DESC);

CREATE TABLE IF NOT EXISTS openai_eval_leases (
    lease_key VARCHAR(512) PRIMARY KEY,
    owner VARCHAR(120) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_openai_eval_lease_expiry ON openai_eval_leases (expires_at);
