-- Preserve case-insensitive public model/effort lookup without scanning history.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_openai_eval_runs_normalized_latest
  ON openai_eval_runs (account_id, lower(btrim(requested_model)), lower(btrim(reasoning_effort)), test_type, finished_at DESC, id DESC)
  WHERE trigger_source IN ('manual', 'scheduled') AND finished_at IS NOT NULL AND status <> 'running';
