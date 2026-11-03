package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func detectorMigrationConfig() service.OpenAIEvalConfig {
	route := func(id int64, model string) service.OpenAIEvalAccountConfig {
		return service.OpenAIEvalAccountConfig{AccountID: id, RequestedModel: model, ReasoningEffort: "high", ModelTraceSchedule: service.OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 600, JitterSeconds: 30}}
	}
	cfg := service.OpenAIEvalConfig{Accounts: []service.OpenAIEvalAccountConfig{route(1, "gpt-6-astra"), route(2, "unknown-model"), route(3, "gpt-6-astra")}}
	cfg.Accounts[2].FingerprintSchedule = service.OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}
	return cfg
}

func TestReconcileDetectorSchedulesOnlyRepairsEligibleLegacyPlans(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	raw, err := json.Marshal(detectorMigrationConfig())
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT config FROM openai_eval_configs WHERE id=1 FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"config"}).AddRow(raw))
	mock.ExpectExec(`(?s)INSERT INTO openai_eval_schedule_state.*WHERE NOT openai_eval_schedule_state.enabled`).WithArgs(int64(1), "gpt-6-astra", "high", 600, 30, nonNilTimeArgument{}).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.ReconcileDetectorSchedules(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReconcileDetectorSchedulesRollsBackOnFailure(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT config FROM openai_eval_configs`).WillReturnError(fmt.Errorf("unavailable"))
	mock.ExpectRollback()
	require.ErrorContains(t, repo.ReconcileDetectorSchedules(t.Context()), "unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}

// Exercise the actual upgrade state, startup reconciliation and runner claim
// SQL, including idempotency, without sending any model requests.
func TestReconcileDetectorSchedulesPostgresUpgrade(t *testing.T) {
	dsn := os.Getenv("SUB2API_EVAL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	schema := fmt.Sprintf("eval_detector_%d", time.Now().UnixNano())
	_, err = db.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, e := db.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, e) })
	_, err = db.Exec("SET search_path TO " + schema)
	require.NoError(t, err)
	for _, name := range []string{"240_openai_evaluations.sql", "241_openai_modeltrace_evaluation.sql", "242_openai_state_probe.sql", "243_openai_eval_sample_count.sql", "247_openai_eval_completion_claims.sql", "249_openai_eval_pelican_artifacts.sql"} {
		raw, e := os.ReadFile("../../migrations/" + name)
		require.NoError(t, e)
		_, e = db.Exec(string(raw))
		require.NoError(t, e, name)
	}
	cfg := detectorMigrationConfig()
	cfg.Revision = 7
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE openai_eval_configs SET config=$1::jsonb`, raw)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO openai_eval_schedule_state(account_id,test_type,requested_model,reasoning_effort,enabled,interval_seconds,next_run_at)
 VALUES(1,'modeltrace','gpt-6-astra','high',FALSE,600,NULL),
 (1,'fingerprint','gpt-6-astra','high',FALSE,86400,NULL),
 (3,'fingerprint','gpt-6-astra','high',TRUE,3600,NOW()+INTERVAL '1 hour')`)
	require.NoError(t, err)
	repo := NewOpenAIEvalRepository(db).(*openAIEvalRepository)
	svc := service.NewOpenAIEvalService(repo, nil, nil)
	require.NoError(t, svc.Initialize(t.Context()))
	var enabled bool
	var interval, jitter int
	var next time.Time
	require.NoError(t, db.QueryRow(`SELECT enabled,interval_seconds,jitter_seconds,next_run_at FROM openai_eval_schedule_state WHERE account_id=1 AND test_type='fingerprint'`).Scan(&enabled, &interval, &jitter, &next))
	require.True(t, enabled)
	require.Equal(t, 600, interval)
	require.Equal(t, 30, jitter)
	require.NoError(t, svc.Initialize(t.Context()))
	var unchanged time.Time
	require.NoError(t, db.QueryRow(`SELECT next_run_at FROM openai_eval_schedule_state WHERE account_id=1 AND test_type='fingerprint'`).Scan(&unchanged))
	require.True(t, next.Equal(unchanged))
	var saved []byte
	require.NoError(t, db.QueryRow(`SELECT config FROM openai_eval_configs`).Scan(&saved))
	require.JSONEq(t, string(raw), string(saved))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM openai_eval_schedule_state WHERE account_id=2`).Scan(&count))
	require.Zero(t, count)
	_, err = db.Exec(`UPDATE openai_eval_schedule_state SET next_run_at=NOW()-INTERVAL '1 second' WHERE account_id=1 AND test_type='fingerprint'`)
	require.NoError(t, err)
	due, err := repo.ClaimDueSchedulesForCompletion(t.Context(), time.Now().UTC(), 20)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, "fingerprint", due[0].TestType)
	require.EqualValues(t, 1, due[0].AccountID)
	// Also recover the missing-row upgrade case.
	_, err = db.Exec(`DELETE FROM openai_eval_schedule_state WHERE account_id=1 AND test_type='fingerprint'`)
	require.NoError(t, err)
	require.NoError(t, repo.ReconcileDetectorSchedules(t.Context()))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM openai_eval_schedule_state WHERE account_id=1 AND test_type='fingerprint' AND enabled`).Scan(&count))
	require.Equal(t, 1, count)
}
