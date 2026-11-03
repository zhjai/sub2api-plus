package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ReconcileDetectorSchedules repairs schedules projected from a retired
// ModelTrace plan before the runner starts. Lock the same config row as
// SaveConfig so a concurrent administrator edit cannot be overwritten.
// Already enabled fingerprint schedules retain their due time and claims.
// The saved config and immutable run history are not rewritten.
func (r *openAIEvalRepository) ReconcileDetectorSchedules(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT config FROM openai_eval_configs WHERE id=1 FOR UPDATE`).Scan(&raw); err != nil {
		return err
	}
	var cfg service.OpenAIEvalConfig
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	legacy := make([]bool, len(cfg.Accounts))
	for i, route := range cfg.Accounts {
		legacy[i] = route.ModelTraceSchedule.Enabled && !route.FingerprintSchedule.Enabled
	}
	service.NormalizeOpenAIEvalDetectorConfig(&cfg)
	for i, route := range cfg.Accounts {
		schedule := route.FingerprintSchedule
		if !legacy[i] || !schedule.Enabled {
			continue
		}
		if schedule.IntervalSeconds < 300 || int64(schedule.IntervalSeconds) > service.OpenAIEvalMaxIntervalSeconds || schedule.JitterSeconds < 0 || int64(schedule.IntervalSeconds)+int64(schedule.JitterSeconds) > service.OpenAIEvalMaxIntervalSeconds {
			return fmt.Errorf("invalid migrated fingerprint schedule for account %d", route.AccountID)
		}
		next, nextErr := openAIEvalInitialNextRun(time.Now().UTC(), schedule.IntervalSeconds, schedule.JitterSeconds)
		if nextErr != nil {
			return nextErr
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO openai_eval_schedule_state
   (account_id,test_type,requested_model,reasoning_effort,enabled,interval_seconds,jitter_seconds,sample_mode,sample_count,next_run_at,updated_at)
   VALUES ($1,'fingerprint',$2,$3,TRUE,$4,$5,'',0,$6,NOW())
   ON CONFLICT (account_id,test_type,requested_model,reasoning_effort) DO UPDATE SET
    enabled=TRUE,interval_seconds=EXCLUDED.interval_seconds,jitter_seconds=EXCLUDED.jitter_seconds,
    sample_mode='',sample_count=0,next_run_at=EXCLUDED.next_run_at,updated_at=NOW()
   WHERE NOT openai_eval_schedule_state.enabled`, route.AccountID, route.RequestedModel, route.ReasoningEffort, schedule.IntervalSeconds, schedule.JitterSeconds, next)
		if err != nil {
			return fmt.Errorf("reconcile migrated fingerprint schedule: %w", err)
		}
	}
	return tx.Commit()
}

var _ service.OpenAIEvalDetectorScheduleRepository = (*openAIEvalRepository)(nil)
