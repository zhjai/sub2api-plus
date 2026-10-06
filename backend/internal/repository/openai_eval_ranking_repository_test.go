package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRankingLatestCompletedEvidenceExactEmptyEffortAndTerminalOrdering(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	keys := []service.OpenAIEvalEvidenceKey{{AccountID: 17, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "", TestType: "candy"}}
	pattern := `(?s)SELECT r.id,.*FROM jsonb_to_recordset\(\$1::jsonb\).*JOIN LATERAL.*WHERE account_id=k.account_id.*NOT \(outcome @> '\{"diagnostic_only": true\}'::jsonb\).*ORDER BY finished_at DESC, id DESC LIMIT 1`
	now := time.Now()
	rows := sqlmock.NewRows([]string{"id", "account_id", "test_type", "requested_model", "reasoning_effort", "data_version", "baseline_version", "status", "outcome", "samples", "finished_at", "trigger_source", "error_code"}).
		AddRow(11, 17, "candy", "gpt-6.1-sol", "", service.OpenAIEvalDataVersion, "", "error", []byte(`{}`), []byte(`[]`), now, "scheduled", "timeout")
	mock.ExpectQuery(pattern).WithArgs(evalJSONArgument{keys}).WillReturnRows(rows)
	runs, err := repo.LatestCompletedRuns(context.Background(), keys)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, "error", runs[0].Status)
	require.Empty(t, runs[0].ReasoningEffort)
	require.NoError(t, mock.ExpectationsWereMet())
	_, err = repo.LatestCompletedRuns(context.Background(), make([]service.OpenAIEvalEvidenceKey, 601))
	require.ErrorContains(t, err, "600")
}

func TestRankingLatestMalformedEvidenceStaysUnknown(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	rows := sqlmock.NewRows([]string{"id", "account_id", "test_type", "requested_model", "reasoning_effort", "data_version", "baseline_version", "status", "outcome", "samples", "finished_at", "trigger_source", "error_code"}).
		AddRow(11, 17, "candy", "gpt-6.1-sol", "", service.OpenAIEvalDataVersion, "", "pass", []byte(`not-json`), []byte(`[]`), time.Now(), "scheduled", "")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT r.id, r.account_id")).WillReturnRows(rows)
	runs, err := repo.LatestCompletedRuns(context.Background(), []service.OpenAIEvalEvidenceKey{{AccountID: 17}})
	require.NoError(t, err)
	require.Equal(t, "insufficient", runs[0].Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRankingLatestQueryExcludesDiagnosticOnlyEvidence(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	pattern := `(?s)FROM jsonb_to_recordset.*NOT \(outcome @> '\{"diagnostic_only": true\}'::jsonb\)`
	rows := sqlmock.NewRows([]string{"id", "account_id", "test_type", "requested_model", "reasoning_effort", "data_version", "baseline_version", "status", "outcome", "samples", "finished_at", "trigger_source", "error_code"}).
		AddRow(12, 17, "candy", "gpt-6.1-sol", "", service.OpenAIEvalDataVersion, "", "pass", []byte(`{"status":"pass","diagnostic_only":false}`), []byte(`[]`), time.Now(), "manual", "")
	mock.ExpectQuery(pattern).WillReturnRows(rows)
	runs, err := repo.LatestCompletedRuns(context.Background(), []service.OpenAIEvalEvidenceKey{{AccountID: 17, RequestedModel: "gpt-6.1-sol", TestType: service.OpenAIEvalTypeCandy}})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.False(t, runs[0].DiagnosticOnly)
	require.NoError(t, mock.ExpectationsWereMet())
}
