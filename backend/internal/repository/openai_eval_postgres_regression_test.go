package repository

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestR8OpenAIEvalPostgresTargetRemoval(t *testing.T) {
	dsn := os.Getenv("SUB2API_EVAL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SUB2API_EVAL_TEST_PG_DSN to an isolated PostgreSQL database")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	schema := fmt.Sprintf("eval_r8_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := db.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, cleanupErr)
	})
	_, err = db.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	for _, name := range []string{"240_openai_evaluations.sql", "241_openai_modeltrace_evaluation.sql", "242_openai_state_probe.sql", "243_openai_eval_sample_count.sql"} {
		raw, readErr := os.ReadFile("../../migrations/" + name)
		require.NoError(t, readErr)
		_, err = db.ExecContext(ctx, string(raw))
		require.NoError(t, err, name)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE accounts (id BIGINT PRIMARY KEY, extra JSONB NOT NULL);
		INSERT INTO accounts VALUES (17, '{"openai_bps_account_state":{"active":true,"degraded_streak":2}}')`)
	require.NoError(t, err)
	var originalState string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT extra::text FROM accounts WHERE id=17").Scan(&originalState))
	repo := NewOpenAIEvalRepository(db)
	cfg := &service.OpenAIEvalConfig{
		Accounts: []service.OpenAIEvalAccountConfig{evalRoute(17), evalRoute(18)}, BPSAutoEnabled: true,
		BPSAccounts: []service.OpenAIEvalBPSAccountConfig{{AccountID: 17, ProbeModel: "gpt-5.1", Mode: service.OpenAIEvalBPSModeAuto, IntervalSeconds: 300}},
	}
	cfg.Accounts[0].CandySchedule.SampleCount = 3
	cfg.Accounts[0].StateProbeSchedule = service.OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	require.NoError(t, repo.SaveConfig(ctx, cfg, 9))
	loaded, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	require.Len(t, loaded.Accounts, 2)
	require.Equal(t, 3, loaded.Accounts[0].CandySchedule.SampleCount)
	require.NotNil(t, loaded.Accounts[0].CandySchedule.NextRunAt)
	require.Empty(t, loaded.BPSAccounts, "retired BPS entries cannot schedule probes")
	require.False(t, loaded.BPSAutoEnabled)
	require.NotNil(t, loaded.Accounts[0].StateProbeSchedule.NextRunAt)
	targetNext, probeNext := *loaded.Accounts[0].CandySchedule.NextRunAt, *loaded.Accounts[0].StateProbeSchedule.NextRunAt
	for _, count := range []int{1, 0, 0} {
		loaded.Accounts = loaded.Accounts[:count]
		require.NoError(t, repo.SaveConfig(ctx, loaded, 9), "remove targets, including idempotent empty save")
		loaded, err = repo.GetConfig(ctx)
		require.NoError(t, err)
		require.Len(t, loaded.Accounts, count)
		require.Empty(t, loaded.BPSAccounts)
		if count == 1 {
			require.True(t, targetNext.Equal(*loaded.Accounts[0].CandySchedule.NextRunAt), "remaining target retains next run")
			require.True(t, probeNext.Equal(*loaded.Accounts[0].StateProbeSchedule.NextRunAt), "independent State Probe schedule must not be reset")
		}
		var state string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT extra::text FROM accounts WHERE id=17").Scan(&state))
		require.Equal(t, originalState, state, "target edits do not reset account-level BPS state")
	}
	var enabled int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM openai_eval_schedule_state WHERE enabled`).Scan(&enabled))
	require.Zero(t, enabled, "removing all test targets retires all automatic schedules")
	_, err = db.ExecContext(ctx, `UPDATE openai_eval_schedule_state SET sample_count=11`)
	require.Error(t, err, "migration enforces Candy sample count bounds")
}
