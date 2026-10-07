package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type openAIEvalRepository struct {
	db *sql.DB
}

func openAIEvalInitialNextRun(now time.Time, intervalSeconds, jitterSeconds int) (time.Time, error) {
	jitter := 0
	if jitterSeconds > 0 {
		jitter = rand.IntN(jitterSeconds + 1)
	}
	delay, err := service.OpenAIEvalIntervalDuration(intervalSeconds + jitter)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(delay), nil
}

func NewOpenAIEvalRepository(db *sql.DB) service.OpenAIEvalRepository {
	return &openAIEvalRepository{db: db}
}

func (r *openAIEvalRepository) GetConfig(ctx context.Context) (*service.OpenAIEvalConfig, error) {
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT config FROM openai_eval_configs WHERE id = 1`).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("load OpenAI evaluation config: %w", err)
	}
	var cfg service.OpenAIEvalConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("decode OpenAI evaluation config: %w", err)
	}
	if cfg.Accounts == nil {
		cfg.Accounts = []service.OpenAIEvalAccountConfig{}
	}
	if cfg.MaxRequestAttempts == 0 {
		cfg.MaxRequestAttempts = service.OpenAIEvalDefaultMaxRequestAttempts
	}
	schedules, err := r.db.QueryContext(ctx, `
		SELECT account_id, test_type, requested_model, reasoning_effort, sample_count, last_run_at, next_run_at
		FROM openai_eval_schedule_state`)
	if err != nil {
		return nil, fmt.Errorf("load OpenAI evaluation schedule state: %w", err)
	}
	defer func() { _ = schedules.Close() }()
	for schedules.Next() {
		var accountID int64
		var testType, model, effort string
		var sampleCount int
		var lastRun, nextRun sql.NullTime
		if err := schedules.Scan(&accountID, &testType, &model, &effort, &sampleCount, &lastRun, &nextRun); err != nil {
			return nil, fmt.Errorf("scan OpenAI evaluation schedule state: %w", err)
		}
		if testType == service.OpenAIEvalTypeStateProbe && effort == service.OpenAIEvalBPSAccountEffort {
			continue
		}
		for i := range cfg.Accounts {
			route := &cfg.Accounts[i]
			if route.AccountID != accountID || !strings.EqualFold(route.RequestedModel, model) || !strings.EqualFold(route.ReasoningEffort, effort) {
				continue
			}
			schedule := &route.CandySchedule
			if testType == service.OpenAIEvalTypeFingerprint {
				schedule = &route.FingerprintSchedule
			} else if testType == service.OpenAIEvalTypeModelTrace {
				schedule = &route.ModelTraceSchedule
			} else if testType == service.OpenAIEvalTypeStateProbe {
				schedule = &route.StateProbeSchedule
			}
			schedule.LastRunAt, schedule.NextRunAt = nil, nil
			schedule.SampleCount = sampleCount
			if lastRun.Valid {
				value := lastRun.Time
				schedule.LastRunAt = &value
			}
			if nextRun.Valid {
				value := nextRun.Time
				schedule.NextRunAt = &value
			}
			break
		}
	}
	if err := schedules.Err(); err != nil {
		return nil, fmt.Errorf("iterate OpenAI evaluation schedule state: %w", err)
	}
	return &cfg, nil
}

func (r *openAIEvalRepository) SaveConfig(ctx context.Context, cfg *service.OpenAIEvalConfig, actorID int64) error {
	if cfg == nil {
		return fmt.Errorf("OpenAI evaluation config is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin OpenAI evaluation config transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var before []byte
	if err := tx.QueryRowContext(ctx, `SELECT config FROM openai_eval_configs WHERE id = 1 FOR UPDATE`).Scan(&before); err != nil {
		return fmt.Errorf("lock OpenAI evaluation config: %w", err)
	}
	var previous service.OpenAIEvalConfig
	if err := json.Unmarshal(before, &previous); err != nil {
		return fmt.Errorf("decode previous OpenAI evaluation config: %w", err)
	}
	if cfg.Revision != 0 && cfg.Revision != previous.Revision {
		return service.ErrOpenAIEvalConfigRevisionConflict
	}
	service.RetireOpenAIEvalBPS(cfg)
	if cfg.MaxRequestAttempts == 0 {
		cfg.MaxRequestAttempts = previous.MaxRequestAttempts
		if cfg.MaxRequestAttempts == 0 {
			cfg.MaxRequestAttempts = service.OpenAIEvalDefaultMaxRequestAttempts
		}
	}
	cfg.Revision = previous.Revision + 1
	// Runtime is a GET projection, never configuration or audit input.
	cfg.BackgroundControls = append([]service.OpenAIEvalBackgroundControl(nil), cfg.BackgroundControls...)
	for i := range cfg.BackgroundControls {
		cfg.BackgroundControls[i].Runtime = nil
		cfg.BackgroundControls[i].RuntimeUnavailableReason = ""
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode OpenAI evaluation config: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE openai_eval_configs SET config = $1::jsonb, updated_by = $2, updated_at = NOW() WHERE id = 1`, payload, actorID); err != nil {
		return fmt.Errorf("save OpenAI evaluation config: %w", err)
	}
	// Lock the existing schedule keys before reconciling the submitted route
	// set.  A whole-table reset here used to clear next_run_at for every route,
	// so merely editing an unrelated setting silently rescheduled all probes.
	// Reconcile only keys that are actually absent from the submitted config;
	// unchanged keys retain their scheduler-owned timestamps in the upsert below.
	rows, err := tx.QueryContext(ctx, `
		SELECT account_id, test_type, requested_model, reasoning_effort
		FROM openai_eval_schedule_state
		FOR UPDATE`)
	if err != nil {
		return fmt.Errorf("lock OpenAI evaluation schedule keys: %w", err)
	}
	keep := make(map[string]struct{}, len(cfg.Accounts)*4)
	for _, route := range cfg.Accounts {
		for _, testType := range []string{service.OpenAIEvalTypeCandy, service.OpenAIEvalTypeFingerprint, service.OpenAIEvalTypeModelTrace, service.OpenAIEvalTypeStateProbe} {
			keep[openAIEvalScheduleKey(route.AccountID, testType, route.RequestedModel, route.ReasoningEffort)] = struct{}{}
		}
	}
	type existingScheduleKey struct {
		accountID int64
		testType  string
		model     string
		effort    string
	}
	existingKeys := make([]existingScheduleKey, 0)
	for rows.Next() {
		var key existingScheduleKey
		if err := rows.Scan(&key.accountID, &key.testType, &key.model, &key.effort); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan OpenAI evaluation schedule keys: %w", err)
		}
		existingKeys = append(existingKeys, key)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate OpenAI evaluation schedule keys: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close OpenAI evaluation schedule keys: %w", err)
	}
	for _, existing := range existingKeys {
		key := openAIEvalScheduleKey(existing.accountID, existing.testType, existing.model, existing.effort)
		if _, exists := keep[key]; exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE openai_eval_schedule_state
			SET enabled=FALSE, next_run_at=NULL, updated_at=NOW()
			WHERE account_id=$1 AND test_type=$2 AND requested_model=$3 AND reasoning_effort=$4`,
			existing.accountID, existing.testType, existing.model, existing.effort); err != nil {
			return fmt.Errorf("disable removed OpenAI evaluation schedule: %w", err)
		}
	}
	for _, route := range cfg.Accounts {
		for _, item := range []struct {
			testType string
			schedule service.OpenAIEvalSchedule
		}{{service.OpenAIEvalTypeCandy, route.CandySchedule}, {service.OpenAIEvalTypeFingerprint, route.FingerprintSchedule}, {service.OpenAIEvalTypeModelTrace, route.ModelTraceSchedule}, {service.OpenAIEvalTypeStateProbe, route.StateProbeSchedule}} {
			nextRun := any(nil)
			if item.schedule.Enabled {
				initial, err := openAIEvalInitialNextRun(time.Now().UTC(), item.schedule.IntervalSeconds, item.schedule.JitterSeconds)
				if err != nil {
					return fmt.Errorf("invalid OpenAI evaluation schedule interval: %w", err)
				}
				nextRun = initial
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO openai_eval_schedule_state (
					account_id, test_type, requested_model, reasoning_effort, enabled,
					interval_seconds, jitter_seconds, sample_mode, sample_count, next_run_at, updated_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NOW())
				ON CONFLICT (account_id, test_type, requested_model, reasoning_effort) DO UPDATE SET
					enabled=EXCLUDED.enabled,
					next_run_at=CASE
						WHEN openai_eval_schedule_state.enabled IS DISTINCT FROM EXCLUDED.enabled
							OR openai_eval_schedule_state.interval_seconds IS DISTINCT FROM EXCLUDED.interval_seconds
							OR openai_eval_schedule_state.jitter_seconds IS DISTINCT FROM EXCLUDED.jitter_seconds
							OR openai_eval_schedule_state.sample_mode IS DISTINCT FROM EXCLUDED.sample_mode
							OR openai_eval_schedule_state.sample_count IS DISTINCT FROM EXCLUDED.sample_count
							THEN EXCLUDED.next_run_at
						ELSE openai_eval_schedule_state.next_run_at
					END,
					interval_seconds=EXCLUDED.interval_seconds,
					jitter_seconds=EXCLUDED.jitter_seconds,
					sample_mode=EXCLUDED.sample_mode,
					sample_count=EXCLUDED.sample_count,
					updated_at=NOW()`,
				route.AccountID, item.testType, route.RequestedModel, route.ReasoningEffort,
				item.schedule.Enabled, item.schedule.IntervalSeconds, item.schedule.JitterSeconds,
				item.schedule.SampleMode, item.schedule.SampleCount, nextRun); err != nil {
				return fmt.Errorf("save OpenAI evaluation schedule: %w", err)
			}
		}
	}
	var after any
	if err := json.Unmarshal(payload, &after); err != nil {
		return fmt.Errorf("decode saved OpenAI evaluation config: %w", err)
	}
	var previousAudit any
	if err := json.Unmarshal(before, &previousAudit); err != nil {
		return fmt.Errorf("decode previous OpenAI evaluation config audit: %w", err)
	}
	audit, err := json.Marshal(map[string]any{"before": previousAudit, "after": after})
	if err != nil {
		return fmt.Errorf("encode OpenAI evaluation config audit: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO openai_eval_audit_events (actor_id, action, payload) VALUES ($1, 'config_updated', $2::jsonb)`, actorID, audit); err != nil {
		return fmt.Errorf("write OpenAI evaluation config audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit OpenAI evaluation config: %w", err)
	}
	return nil
}

func openAIEvalScheduleKey(accountID int64, testType, model, effort string) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s", accountID, testType, strings.ToLower(strings.TrimSpace(model)), strings.ToLower(strings.TrimSpace(effort)))
}

func (r *openAIEvalRepository) CreateRun(ctx context.Context, run *service.OpenAIEvalRun) (int64, error) {
	if run == nil {
		return 0, fmt.Errorf("OpenAI evaluation run is required")
	}
	outcome, err := marshalOpenAIEvalRunOutcome(run)
	if err != nil {
		return 0, fmt.Errorf("encode OpenAI evaluation outcome: %w", err)
	}
	samples, err := json.Marshal(run.Samples)
	if err != nil {
		return 0, fmt.Errorf("encode OpenAI evaluation samples: %w", err)
	}
	var id int64
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO openai_eval_runs (
			account_id, test_type, requested_model, upstream_model, reasoning_effort,
			data_version, baseline_version, status, outcome, samples, request_count,
			input_tokens, output_tokens, cost_estimate_usd, duration_ms, started_at,
			finished_at, triggered_by, trigger_source, error_code
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::jsonb,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		RETURNING id`,
		run.AccountID, run.TestType, run.RequestedModel, run.UpstreamModel, run.ReasoningEffort,
		run.DataVersion, run.BaselineVersion, run.Status, outcome, samples, run.RequestCount,
		run.InputTokens, run.OutputTokens, nullableEvalCost(run.CostEstimateUSD), run.DurationMS, run.StartedAt,
		nullTime(run.FinishedAt), run.TriggeredBy, run.TriggerSource, run.Error,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create OpenAI evaluation run: %w", err)
	}
	return id, nil
}

func (r *openAIEvalRepository) FinishRun(ctx context.Context, id int64, run *service.OpenAIEvalRun) error {
	if run == nil {
		return fmt.Errorf("OpenAI evaluation run result is required")
	}
	outcome, err := marshalOpenAIEvalRunOutcome(run)
	if err != nil {
		return fmt.Errorf("encode OpenAI evaluation outcome: %w", err)
	}
	samples, err := json.Marshal(run.Samples)
	if err != nil {
		return fmt.Errorf("encode OpenAI evaluation samples: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE openai_eval_runs SET status=$2, outcome=$3::jsonb, samples=$4::jsonb,
			request_count=$5, input_tokens=$6, output_tokens=$7, cost_estimate_usd=$8,
			duration_ms=$9, finished_at=$10, error_code=$11, upstream_model=$12, baseline_version=$13
		WHERE id=$1`, id, run.Status, outcome, samples, run.RequestCount, run.InputTokens,
		run.OutputTokens, nullableEvalCost(run.CostEstimateUSD), run.DurationMS, run.FinishedAt,
		run.Error, run.UpstreamModel, run.BaselineVersion)
	if err != nil {
		return fmt.Errorf("finish OpenAI evaluation run: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("OpenAI evaluation run %d not found", id)
	}
	return nil
}

func (r *openAIEvalRepository) UpdateRunProgress(ctx context.Context, id int64, run *service.OpenAIEvalRun) error {
	if run == nil || id <= 0 {
		return fmt.Errorf("OpenAI evaluation run progress is required")
	}
	outcome, err := marshalOpenAIEvalRunOutcome(run)
	if err != nil {
		return fmt.Errorf("encode OpenAI evaluation progress: %w", err)
	}
	samples, err := json.Marshal(run.Samples)
	if err != nil {
		return fmt.Errorf("encode OpenAI evaluation progress samples: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE openai_eval_runs SET status=$2, outcome=$3::jsonb, samples=$4::jsonb,
			request_count=$5, input_tokens=$6, output_tokens=$7, duration_ms=$8,
			upstream_model=$9, baseline_version=$10
		WHERE id=$1 AND finished_at IS NULL`, id, run.Status, outcome, samples,
		run.RequestCount, run.InputTokens, run.OutputTokens, run.DurationMS,
		run.UpstreamModel, run.BaselineVersion)
	if err != nil {
		return fmt.Errorf("update OpenAI evaluation progress: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("running OpenAI evaluation %d not found", id)
	}
	return nil
}

var _ service.OpenAIEvalProgressRepository = (*openAIEvalRepository)(nil)

func (r *openAIEvalRepository) ListRuns(ctx context.Context, filter service.OpenAIEvalRunFilter) ([]service.OpenAIEvalRun, error) {
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, test_type, requested_model, upstream_model, reasoning_effort,
			data_version, baseline_version, status, outcome, samples, request_count,
			input_tokens, output_tokens, cost_estimate_usd, duration_ms, started_at,
			finished_at, triggered_by, trigger_source, error_code
		FROM openai_eval_runs
	WHERE ($1 = 0 OR account_id = $1)
	  AND ($2 = '' OR requested_model = $2)
	  AND ($3 = '' OR reasoning_effort = $3)
	  AND ($4 = '' OR test_type = $4)
	ORDER BY created_at DESC, id DESC LIMIT $5`,
		filter.AccountID, filter.RequestedModel, filter.ReasoningEffort, filter.TestType, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("list OpenAI evaluation runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := make([]service.OpenAIEvalRun, 0)
	for rows.Next() {
		var run service.OpenAIEvalRun
		var outcomeRaw, samplesRaw []byte
		var finishedAt sql.NullTime
		var cost sql.NullFloat64
		if err := rows.Scan(
			&run.ID, &run.AccountID, &run.TestType, &run.RequestedModel, &run.UpstreamModel, &run.ReasoningEffort,
			&run.DataVersion, &run.BaselineVersion, &run.Status, &outcomeRaw, &samplesRaw, &run.RequestCount,
			&run.InputTokens, &run.OutputTokens, &cost, &run.DurationMS, &run.StartedAt,
			&finishedAt, &run.TriggeredBy, &run.TriggerSource, &run.Error,
		); err != nil {
			return nil, fmt.Errorf("scan OpenAI evaluation run: %w", err)
		}
		if finishedAt.Valid {
			run.FinishedAt = finishedAt.Time
		}
		if cost.Valid {
			run.CostEstimateUSD = &cost.Float64
		}
		if err := unmarshalOpenAIEvalRunOutcome(outcomeRaw, &run); err != nil {
			return nil, fmt.Errorf("decode OpenAI evaluation outcome: %w", err)
		}
		if err := json.Unmarshal(samplesRaw, &run.Samples); err != nil {
			return nil, fmt.Errorf("decode OpenAI evaluation samples: %w", err)
		}
		if run.Status == "running" {
			run.CompletedSamples = run.Outcome.SampleCount
			run.ExpectedSamples = run.Outcome.ExpectedCount
			run.SampleCount = run.Outcome.ExpectedCount
			run.Phase = run.Outcome.Reason
		} else {
			run.CompletedSamples = len(run.Samples)
			run.ExpectedSamples = run.Outcome.ExpectedCount
			run.SampleCount = run.Outcome.ExpectedCount
		}
		result = append(result, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate OpenAI evaluation runs: %w", err)
	}
	return result, nil
}

func (r *openAIEvalRepository) ListAuditEvents(ctx context.Context, limit int) ([]service.OpenAIEvalAuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, actor_id, action, payload, created_at FROM openai_eval_audit_events ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list OpenAI evaluation audit: %w", err)
	}
	defer func() { _ = rows.Close() }()
	events := make([]service.OpenAIEvalAuditEvent, 0)
	for rows.Next() {
		var event service.OpenAIEvalAuditEvent
		var payload []byte
		if err := rows.Scan(&event.ID, &event.ActorID, &event.Action, &payload, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan OpenAI evaluation audit: %w", err)
		}
		if err := json.Unmarshal(payload, &event.Payload); err != nil {
			return nil, fmt.Errorf("decode OpenAI evaluation audit: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate OpenAI evaluation audit: %w", err)
	}
	return events, nil
}

func (r *openAIEvalRepository) RecordAuditEvent(ctx context.Context, actorID int64, action string, payload map[string]any) error {
	action = strings.TrimSpace(action)
	if action == "" {
		return fmt.Errorf("OpenAI evaluation audit action is required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode OpenAI evaluation audit payload: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO openai_eval_audit_events (actor_id, action, payload) VALUES ($1, $2, $3::jsonb)`, actorID, action, encoded); err != nil {
		return fmt.Errorf("write OpenAI evaluation audit: %w", err)
	}
	return nil
}

func (r *openAIEvalRepository) ClaimDueSchedules(ctx context.Context, now time.Time, limit int) ([]service.OpenAIEvalScheduledRun, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin OpenAI evaluation schedule claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT account_id, test_type, requested_model, reasoning_effort, sample_mode, sample_count, interval_seconds, jitter_seconds
		FROM openai_eval_schedule_state
		WHERE enabled = TRUE AND next_run_at <= $1 AND reasoning_effort <> '__bps_account__'
		ORDER BY next_run_at, account_id
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select due OpenAI evaluation schedules: %w", err)
	}
	type claimed struct {
		service.OpenAIEvalScheduledRun
		interval int
		jitter   int
	}
	items := make([]claimed, 0)
	for rows.Next() {
		var item claimed
		if err := rows.Scan(&item.AccountID, &item.TestType, &item.RequestedModel, &item.ReasoningEffort, &item.SampleMode, &item.SampleCount, &item.interval, &item.jitter); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan due OpenAI evaluation schedule: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate due OpenAI evaluation schedules: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close due OpenAI evaluation schedules: %w", err)
	}
	result := make([]service.OpenAIEvalScheduledRun, 0, len(items))
	for _, item := range items {
		jitter := 0
		if item.jitter > 0 {
			var seed int64
			if err := tx.QueryRowContext(ctx, `SELECT floor(random() * ($1 + 1))::bigint`, item.jitter).Scan(&seed); err != nil {
				return nil, fmt.Errorf("calculate OpenAI evaluation schedule jitter: %w", err)
			}
			jitter = int(seed)
		}
		delay, err := service.OpenAIEvalIntervalDuration(item.interval + jitter)
		if err != nil {
			return nil, fmt.Errorf("advance OpenAI evaluation schedule: %w", err)
		}
		next := now.Add(delay)
		if _, err := tx.ExecContext(ctx, `
			UPDATE openai_eval_schedule_state
			SET last_run_at=$5, next_run_at=$6, updated_at=NOW()
			WHERE account_id=$1 AND test_type=$2 AND requested_model=$3 AND reasoning_effort=$4`,
			item.AccountID, item.TestType, item.RequestedModel, item.ReasoningEffort, now, next); err != nil {
			return nil, fmt.Errorf("advance OpenAI evaluation schedule: %w", err)
		}
		result = append(result, item.OpenAIEvalScheduledRun)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit OpenAI evaluation schedule claim: %w", err)
	}
	return result, nil
}

func (r *openAIEvalRepository) AcquireLease(ctx context.Context, key, owner string, ttl time.Duration) (bool, error) {
	if key == "" || owner == "" || ttl <= 0 {
		return false, fmt.Errorf("lease key, owner, and positive TTL are required")
	}
	var acquired bool
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO openai_eval_leases (lease_key, owner, expires_at, updated_at)
		VALUES ($1, $2, NOW() + ($3 * INTERVAL '1 second'), NOW())
		ON CONFLICT (lease_key) DO UPDATE SET owner=EXCLUDED.owner, expires_at=EXCLUDED.expires_at, updated_at=NOW()
		WHERE openai_eval_leases.expires_at <= NOW()
		RETURNING TRUE`, key, owner, ttl.Seconds()).Scan(&acquired)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("acquire OpenAI evaluation lease: %w", err)
	}
	return acquired, nil
}

func (r *openAIEvalRepository) RenewLease(ctx context.Context, key, owner string, ttl time.Duration) (bool, error) {
	if key == "" || owner == "" || ttl <= 0 {
		return false, fmt.Errorf("lease key, owner, and positive TTL are required")
	}
	var renewed bool
	err := r.db.QueryRowContext(ctx, `
		UPDATE openai_eval_leases
		SET expires_at=NOW() + ($3 * INTERVAL '1 second'), updated_at=NOW()
		WHERE lease_key=$1 AND owner=$2 AND expires_at > NOW()
		RETURNING TRUE`, key, owner, ttl.Seconds()).Scan(&renewed)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("renew OpenAI evaluation lease: %w", err)
	}
	return renewed, nil
}

func (r *openAIEvalRepository) ReleaseLease(ctx context.Context, key, owner string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM openai_eval_leases WHERE lease_key=$1 AND owner=$2`, key, owner)
	if err != nil {
		return fmt.Errorf("release OpenAI evaluation lease: %w", err)
	}
	return nil
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func nullableEvalCost(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
