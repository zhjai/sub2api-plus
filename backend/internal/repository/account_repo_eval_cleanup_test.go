package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDeletedAccountEvalCleanupIsInsideAccountDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	failure := errors.New("synthetic later account deletion failure")
	mock.ExpectQuery(`SELECT .* FROM "account_groups"`).WithArgs(int64(17)).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id", "priority", "created_at"}))
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE openai_eval_configs`).WithArgs(int64(17)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE openai_eval_schedule_state`).WithArgs(int64(17)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(`INSERT INTO openai_eval_audit_events`).WithArgs(int64(17)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM "account_groups"`).WithArgs(int64(17)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM scheduled_test_plans`).WithArgs(int64(17)).WillReturnError(failure)
	mock.ExpectRollback()
	require.ErrorIs(t, repo.Delete(t.Context(), 17), failure)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeletedAccountEvalCleanupPropagatesTransactionFailure(t *testing.T) {
	for _, stage := range []string{"config", "schedules", "audit"} {
		t.Run(stage, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectBegin()
			failure := errors.New("synthetic transaction failure")
			config := mock.ExpectExec(`UPDATE openai_eval_configs`).WithArgs(int64(17))
			if stage == "config" {
				config.WillReturnError(failure)
			} else {
				config.WillReturnResult(sqlmock.NewResult(0, 1))
				schedule := mock.ExpectExec(`UPDATE openai_eval_schedule_state`).WithArgs(int64(17))
				if stage == "schedules" {
					schedule.WillReturnError(failure)
				} else {
					schedule.WillReturnResult(sqlmock.NewResult(0, 2))
					mock.ExpectExec(`INSERT INTO openai_eval_audit_events`).WithArgs(int64(17)).WillReturnError(failure)
				}
			}
			mock.ExpectRollback()
			tx, err := db.BeginTx(t.Context(), nil)
			require.NoError(t, err)
			require.ErrorIs(t, removeDeletedAccountEvalConfig(t.Context(), tx, 17), failure)
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestDeletedAccountEvalCleanupPostgresAtomicAndHistory(t *testing.T) {
	dsn := os.Getenv("SUB2API_EVAL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SUB2API_EVAL_TEST_PG_DSN to an isolated PostgreSQL database")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	schema := fmt.Sprintf("eval_delete_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, err)
	})
	_, err = db.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	for _, name := range []string{"240_openai_evaluations.sql", "241_openai_modeltrace_evaluation.sql", "242_openai_state_probe.sql", "243_openai_eval_sample_count.sql"} {
		raw, err := os.ReadFile("../../migrations/" + name)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(raw))
		require.NoError(t, err, name)
	}
	repo := NewOpenAIEvalRepository(db)
	cfg := &service.OpenAIEvalConfig{
		Accounts:             []service.OpenAIEvalAccountConfig{evalRoute(17), evalRoute(18)},
		AccountPriorityRules: []service.OpenAIEvalAccountPriorityRule{{AccountID: 17, Priority: 1}, {AccountID: 18, Priority: 2}},
	}
	cfg.Accounts[0].StateProbeSchedule = service.OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	require.NoError(t, repo.SaveConfig(ctx, cfg, 9))
	stale, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	var next time.Time
	require.NoError(t, db.QueryRowContext(ctx, `SELECT next_run_at FROM openai_eval_schedule_state WHERE account_id=18 AND test_type='candy'`).Scan(&next))
	_, err = db.ExecContext(ctx, `UPDATE openai_eval_configs SET config = config || '{"future_field":{"keep":true},"bps_accounts":[{"account_id":17},{"account_id":18}]}'::jsonb;
		INSERT INTO openai_eval_runs (account_id,test_type,requested_model,data_version,status,started_at,trigger_source)
		VALUES (17,'candy','gpt-6-astra','fixture','pass',NOW(),'manual');
		CREATE TABLE accounts (id BIGINT PRIMARY KEY);
		INSERT INTO accounts VALUES (17),(18)`)
	require.NoError(t, err)
	readConfig := func() map[string]json.RawMessage {
		var raw []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT config FROM openai_eval_configs WHERE id=1`).Scan(&raw))
		var decoded map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &decoded))
		return decoded
	}
	before := readConfig()
	// A later statement failing must roll back config, schedules and audit together.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, removeDeletedAccountEvalConfig(ctx, tx, 17))
	_, err = tx.ExecContext(ctx, `SELECT 1 / 0`)
	require.Error(t, err)
	require.NoError(t, tx.Rollback())
	require.Equal(t, before, readConfig())
	var enabled int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM openai_eval_schedule_state WHERE account_id=17 AND enabled`).Scan(&enabled))
	require.Equal(t, 2, enabled)
	var audits int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM openai_eval_audit_events WHERE action='deleted_account_removed'`).Scan(&audits))
	require.Zero(t, audits)

	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, removeDeletedAccountEvalConfig(ctx, tx, 17))
	_, err = tx.ExecContext(ctx, `DELETE FROM accounts WHERE id=17`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	after := readConfig()
	require.JSONEq(t, `{"keep":true}`, string(after["future_field"]))
	for _, field := range []string{"accounts", "account_priority_rules", "bps_accounts"} {
		var items []struct {
			AccountID int64 `json:"account_id"`
		}
		require.NoError(t, json.Unmarshal(after[field], &items))
		require.Len(t, items, 1, field)
		require.Equal(t, int64(18), items[0].AccountID, field)
	}
	var revision int64
	require.NoError(t, json.Unmarshal(after["revision"], &revision))
	require.Equal(t, stale.Revision+1, revision)
	require.ErrorIs(t, repo.SaveConfig(ctx, stale, 9), service.ErrOpenAIEvalConfigRevisionConflict)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM openai_eval_schedule_state WHERE account_id=17 AND (enabled OR next_run_at IS NOT NULL)`).Scan(&enabled))
	require.Zero(t, enabled)
	var preservedNext time.Time
	require.NoError(t, db.QueryRowContext(ctx, `SELECT next_run_at FROM openai_eval_schedule_state WHERE account_id=18 AND test_type='candy'`).Scan(&preservedNext))
	require.True(t, next.Equal(preservedNext), "unrelated account is not rescheduled")
	var history int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM openai_eval_runs WHERE account_id=17`).Scan(&history))
	require.Equal(t, 1, history)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM openai_eval_audit_events WHERE action='deleted_account_removed'`).Scan(&audits))
	require.Equal(t, 1, audits)

	// Idempotent deletion of absent references changes neither revision nor audit.
	require.NoError(t, removeDeletedAccountEvalConfig(ctx, db, 17))
	require.Equal(t, after, readConfig())
	// Retained old BPS schedule rows cannot cause an automatic upstream request.
	_, err = db.ExecContext(ctx, `INSERT INTO openai_eval_schedule_state
		(account_id,test_type,requested_model,reasoning_effort,enabled,interval_seconds,next_run_at)
		VALUES (18,'state_probe','gpt-6-astra','__bps_account__',TRUE,300,NOW()-INTERVAL '1 minute')`)
	require.NoError(t, err)
	due, err := repo.ClaimDueSchedules(ctx, time.Now(), 20)
	require.NoError(t, err)
	require.Empty(t, due, "only future native schedules and a retired BPS schedule remain")
}
