package repository

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalInitialNextRunUsesAdditiveJitter(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for range 100 {
		next, err := openAIEvalInitialNextRun(now, 300, 150)
		require.NoError(t, err)
		delay := next.Sub(now)
		require.GreaterOrEqual(t, delay, 300*time.Second)
		require.LessOrEqual(t, delay, 450*time.Second)
	}
}

func TestOpenAIEvalUpdateRunProgressPersistsOnlySanitizedCounters(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	run := &service.OpenAIEvalRun{
		Status: "running", RequestCount: 5, InputTokens: 12, OutputTokens: 7, DurationMS: 850,
		UpstreamModel: "gpt-upstream", BaselineVersion: "baseline-v1",
		Outcome: service.OpenAIEvalOutcome{Status: "running", Reason: "sampling", SampleCount: 5, ExpectedCount: 60},
		Samples: []service.OpenAIEvalSampleRecord{{ProbeID: "cell-1", NormalizedAnswer: "42", Valid: true}},
	}
	mock.ExpectExec(`UPDATE\s+openai_eval_runs\s+SET\s+status=\$2`).
		WithArgs(int64(71), "running", sqlmock.AnyArg(), sqlmock.AnyArg(), 5, int64(12), int64(7), int64(850), "gpt-upstream", "baseline-v1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateRunProgress(context.Background(), 71, run))
	require.NoError(t, mock.ExpectationsWereMet())
}

type nonNilTimeArgument struct{}

func (nonNilTimeArgument) Match(value driver.Value) bool {
	_, ok := value.(time.Time)
	return ok
}

func openAIEvalSaveConfigDB(t *testing.T) (*openAIEvalRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &openAIEvalRepository{db: db}, mock
}

func expectOpenAIEvalConfigSavePreamble(mock sqlmock.Sqlmock, before string, actorID int64) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT\s+config\s+FROM\s+openai_eval_configs\s+WHERE\s+id\s*=\s*1\s+FOR\s+UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"config"}).AddRow([]byte(before)))
	mock.ExpectExec(`UPDATE\s+openai_eval_configs\s+SET\s+config\s*=\s*\$1::jsonb`).
		WithArgs(sqlmock.AnyArg(), actorID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectOpenAIEvalConfigSaveAuditAndCommit(mock sqlmock.Sqlmock, actorID int64) {
	mock.ExpectExec(`INSERT\s+INTO\s+openai_eval_audit_events`).
		WithArgs(actorID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func expectOpenAIEvalScheduleUpsert(mock sqlmock.Sqlmock, accountID int64, testType string, schedule service.OpenAIEvalSchedule, next sqlmock.Argument) {
	mock.ExpectExec(`INSERT\s+INTO\s+openai_eval_schedule_state`).
		WithArgs(accountID, testType, "gpt-6-astra", "high", schedule.Enabled, schedule.IntervalSeconds, schedule.JitterSeconds, schedule.SampleMode, schedule.SampleCount, next).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func evalRoute(accountID int64) service.OpenAIEvalAccountConfig {
	return service.OpenAIEvalAccountConfig{
		AccountID:       accountID,
		RequestedModel:  "gpt-6-astra",
		ReasoningEffort: "high",
		CandySchedule: service.OpenAIEvalSchedule{
			Enabled: true, IntervalSeconds: 900, JitterSeconds: 30, SampleMode: "default",
		},
	}
}

func scheduleStateRows(rows ...[]driver.Value) *sqlmock.Rows {
	result := sqlmock.NewRows([]string{"account_id", "test_type", "requested_model", "reasoning_effort"})
	for _, row := range rows {
		result.AddRow(row...)
	}
	return result
}

func TestOpenAIEvalSaveConfigPreservesUnchangedNextRunAt(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	config := service.OpenAIEvalConfig{Accounts: []service.OpenAIEvalAccountConfig{evalRoute(17)}}
	expectOpenAIEvalConfigSavePreamble(mock, `{"accounts":[]}`, 9)
	mock.ExpectQuery(`SELECT\s+account_id,\s+test_type,\s+requested_model,\s+reasoning_effort`).
		WillReturnRows(scheduleStateRows(
			[]driver.Value{int64(17), service.OpenAIEvalTypeCandy, "gpt-6-astra", "high"},
			[]driver.Value{int64(17), service.OpenAIEvalTypeFingerprint, "gpt-6-astra", "high"},
			[]driver.Value{int64(17), service.OpenAIEvalTypeModelTrace, "gpt-6-astra", "high"},
			[]driver.Value{int64(17), service.OpenAIEvalTypeStateProbe, "gpt-6-astra", "high"},
		))
	expectOpenAIEvalScheduleUpsert(mock, 17, service.OpenAIEvalTypeCandy, config.Accounts[0].CandySchedule, nonNilTimeArgument{})
	for _, testType := range []string{service.OpenAIEvalTypeFingerprint, service.OpenAIEvalTypeModelTrace, service.OpenAIEvalTypeStateProbe} {
		expectOpenAIEvalScheduleUpsert(mock, 17, testType, service.OpenAIEvalSchedule{}, nil)
	}
	expectOpenAIEvalConfigSaveAuditAndCommit(mock, 9)

	require.NoError(t, repo.SaveConfig(t.Context(), &config, 9))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIEvalSaveConfigDisablesRemovedRoutes(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	config := service.OpenAIEvalConfig{Accounts: []service.OpenAIEvalAccountConfig{evalRoute(17)}}
	expectOpenAIEvalConfigSavePreamble(mock, `{"accounts":[]}`, 11)
	mock.ExpectQuery(`SELECT\s+account_id,\s+test_type,\s+requested_model,\s+reasoning_effort`).
		WillReturnRows(scheduleStateRows([]driver.Value{int64(99), service.OpenAIEvalTypeCandy, "gpt-6-astra", "high"}))
	mock.ExpectExec(`UPDATE\s+openai_eval_schedule_state\s+SET\s+enabled\s*=\s*FALSE`).
		WithArgs(int64(99), service.OpenAIEvalTypeCandy, "gpt-6-astra", "high").
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectOpenAIEvalScheduleUpsert(mock, 17, service.OpenAIEvalTypeCandy, config.Accounts[0].CandySchedule, nonNilTimeArgument{})
	for _, testType := range []string{service.OpenAIEvalTypeFingerprint, service.OpenAIEvalTypeModelTrace, service.OpenAIEvalTypeStateProbe} {
		expectOpenAIEvalScheduleUpsert(mock, 17, testType, service.OpenAIEvalSchedule{}, nil)
	}
	expectOpenAIEvalConfigSaveAuditAndCommit(mock, 11)

	require.NoError(t, repo.SaveConfig(t.Context(), &config, 11))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIEvalSaveConfigSchedulesNewRoute(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	config := service.OpenAIEvalConfig{Accounts: []service.OpenAIEvalAccountConfig{evalRoute(23)}}
	expectOpenAIEvalConfigSavePreamble(mock, `{"accounts":[]}`, 13)
	mock.ExpectQuery(`SELECT\s+account_id,\s+test_type,\s+requested_model,\s+reasoning_effort`).
		WillReturnRows(scheduleStateRows())
	expectOpenAIEvalScheduleUpsert(mock, 23, service.OpenAIEvalTypeCandy, config.Accounts[0].CandySchedule, nonNilTimeArgument{})
	for _, testType := range []string{service.OpenAIEvalTypeFingerprint, service.OpenAIEvalTypeModelTrace, service.OpenAIEvalTypeStateProbe} {
		expectOpenAIEvalScheduleUpsert(mock, 23, testType, service.OpenAIEvalSchedule{}, nil)
	}
	expectOpenAIEvalConfigSaveAuditAndCommit(mock, 13)

	require.NoError(t, repo.SaveConfig(t.Context(), &config, 13))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIEvalSaveConfigIncrementsRevision(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	config := service.OpenAIEvalConfig{Revision: 4, Accounts: []service.OpenAIEvalAccountConfig{evalRoute(31)}}
	expectOpenAIEvalConfigSavePreamble(mock, `{"revision":4,"accounts":[]}`, 17)
	mock.ExpectQuery(`SELECT\s+account_id,\s+test_type,\s+requested_model,\s+reasoning_effort`).
		WillReturnRows(scheduleStateRows())
	expectOpenAIEvalScheduleUpsert(mock, 31, service.OpenAIEvalTypeCandy, config.Accounts[0].CandySchedule, nonNilTimeArgument{})
	for _, testType := range []string{service.OpenAIEvalTypeFingerprint, service.OpenAIEvalTypeModelTrace, service.OpenAIEvalTypeStateProbe} {
		expectOpenAIEvalScheduleUpsert(mock, 31, testType, service.OpenAIEvalSchedule{}, nil)
	}
	expectOpenAIEvalConfigSaveAuditAndCommit(mock, 17)

	require.NoError(t, repo.SaveConfig(t.Context(), &config, 17))
	require.Equal(t, int64(5), config.Revision)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIEvalSaveConfigRejectsStaleRevision(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	config := service.OpenAIEvalConfig{Revision: 2}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT\s+config\s+FROM\s+openai_eval_configs\s+WHERE\s+id\s*=\s*1\s+FOR\s+UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"config"}).AddRow([]byte(`{"revision":3,"accounts":[]}`)))
	mock.ExpectRollback()

	require.ErrorIs(t, repo.SaveConfig(t.Context(), &config, 17), service.ErrOpenAIEvalConfigRevisionConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDisableLegacyBPSForRemovedAccountPreventsCompatibilityFallback(t *testing.T) {
	previous := &service.OpenAIEvalConfig{BPSAccounts: []service.OpenAIEvalBPSAccountConfig{
		{AccountID: 41, Mode: service.OpenAIEvalBPSModeAuto},
		{AccountID: 42, Mode: service.OpenAIEvalBPSModeAuto},
	}}
	next := &service.OpenAIEvalConfig{
		BPSAccounts: []service.OpenAIEvalBPSAccountConfig{{AccountID: 42, Mode: service.OpenAIEvalBPSModeAuto}},
		Accounts: []service.OpenAIEvalAccountConfig{
			{AccountID: 41, RequestedModel: "gpt-5.4", BPSMode: service.OpenAIEvalBPSModeAuto, BPSAuto: true},
			{AccountID: 41, RequestedModel: "gpt-6-astra", BPSMode: service.OpenAIEvalBPSModeForceOn, BPSAuto: true},
			{AccountID: 42, RequestedModel: "gpt-5.4", BPSMode: service.OpenAIEvalBPSModeAuto, BPSAuto: true},
		},
	}

	disableLegacyBPSForRemovedAccounts(previous, next)

	require.Equal(t, service.OpenAIEvalBPSModeForceOff, next.Accounts[0].BPSMode)
	require.False(t, next.Accounts[0].BPSAuto)
	require.Equal(t, service.OpenAIEvalBPSModeForceOff, next.Accounts[1].BPSMode)
	require.False(t, next.Accounts[1].BPSAuto)
	require.Equal(t, service.OpenAIEvalBPSModeAuto, next.Accounts[2].BPSMode)
	require.True(t, next.Accounts[2].BPSAuto)
}
