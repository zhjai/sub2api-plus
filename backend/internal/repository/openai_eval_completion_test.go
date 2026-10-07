package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalCompletionClaimsUseFairTargetsAndOwnership(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mock.ExpectQuery(`(?s)WITH ranked AS .*row_number\(\) OVER \(PARTITION BY test_type ORDER BY next_run_at.*FOR UPDATE OF s SKIP LOCKED.*ORDER BY turn,previous_due`).WithArgs(now, 20, sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"account_id", "test_type", "requested_model", "reasoning_effort", "sample_mode", "sample_count"}).AddRow(1, "candy", "gpt-5.4", "", "", 1).AddRow(1, "fingerprint", "gpt-5.4", "", "quick", 0))
	items, err := repo.ClaimDueSchedulesForCompletion(ctx, now, 20)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.NotEmpty(t, items[0].ClaimOwner)
	item := items[0]
	mock.ExpectExec(`(?s)UPDATE openai_eval_schedule_state SET claimed_until.*claim_owner=\$5 AND claimed_until>NOW`).WithArgs(item.AccountID, item.TestType, item.RequestedModel, item.ReasoningEffort, item.ClaimOwner).WillReturnResult(sqlmock.NewResult(0, 1))
	renewed, err := repo.RenewScheduleClaim(ctx, item)
	require.NoError(t, err)
	require.True(t, renewed)
	// Completion uses current enabled/interval/jitter and never historical due time.
	finish := now.Add(10 * time.Minute)
	mock.ExpectExec(`(?s)UPDATE openai_eval_schedule_state.*CASE WHEN enabled THEN \$6::timestamptz.*interval_seconds.*claim_owner=NULL,claimed_until=NULL.*claim_owner=\$5`).WithArgs(item.AccountID, item.TestType, item.RequestedModel, item.ReasoningEffort, item.ClaimOwner, finish).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.CompleteSchedule(ctx, item, finish))
	require.NoError(t, mock.ExpectationsWereMet())
	var _ service.OpenAIEvalCompletionRepository = repo
}

func TestOpenAIEvalBackgroundRuntimeIsNeverPersisted(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	cfg := &service.OpenAIEvalConfig{Accounts: []service.OpenAIEvalAccountConfig{}, BackgroundControls: []service.OpenAIEvalBackgroundControl{{AccountID: 1, Runtime: &service.OpenAIEvalBackgroundRuntime{SentLastHour: 42}, RuntimeUnavailableReason: "fixture_unavailable"}}}
	want := *cfg
	want.Revision = 1
	want.MaxRequestAttempts = service.OpenAIEvalDefaultMaxRequestAttempts
	want.BackgroundControls = []service.OpenAIEvalBackgroundControl{{AccountID: 1}}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT\s+config\s+FROM\s+openai_eval_configs`).WillReturnRows(sqlmock.NewRows([]string{"config"}).AddRow([]byte(`{"accounts":[]}`)))
	mock.ExpectExec(`UPDATE\s+openai_eval_configs`).WithArgs(evalJSONArgument{want}, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT\s+account_id,\s+test_type`).WillReturnRows(scheduleStateRows())
	expectOpenAIEvalConfigSaveAuditAndCommit(mock, 1)
	require.NoError(t, repo.SaveConfig(context.Background(), cfg, 1))
	require.NoError(t, mock.ExpectationsWereMet())
	require.Nil(t, cfg.BackgroundControls[0].Runtime)
	require.Empty(t, cfg.BackgroundControls[0].RuntimeUnavailableReason)
}
