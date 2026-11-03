package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelDimensionRepositoryLatestUsableAcrossEffort(t *testing.T) {
	repo, mock := openAIEvalSaveConfigDB(t)
	now := time.Now()
	keys := []service.OpenAIEvalEvidenceKey{{AccountID: 1, RequestedModel: "gpt-6.1-sol", TestType: "candy"}}
	rows := sqlmock.NewRows([]string{"id", "account_id", "test_type", "requested_model", "reasoning_effort", "data_version", "baseline_version", "status", "outcome", "samples", "finished_at", "trigger_source", "error_code"}).
		AddRow(3, 1, "candy", "gpt-6.1-sol", "max", service.OpenAIEvalQualityDataVersion, "", "pass", []byte(`{}`), []byte(`[{"valid":false,"answer":"21"}]`), now.Add(-time.Second), "scheduled", "").
		AddRow(2, 1, "candy", "gpt-6.1-sol", "low", service.OpenAIEvalQualityDataVersion, "", "warning", []byte(`{}`), []byte(`[{"valid":true,"answer":"28"}]`), now.Add(-time.Minute), "manual", "").
		AddRow(1, 1, "candy", "gpt-6.1-sol", "high", service.OpenAIEvalQualityDataVersion, "", "pass", []byte(`{}`), []byte(`[{"valid":true,"answer":"21"}]`), now.Add(-2*time.Minute), "scheduled", "")
	// Match the model-only join through WHERE: no effort predicate is permitted.
	pattern := `(?s)JOIN openai_eval_runs r ON r.account_id=k.account_id\s+AND lower\(btrim\(r.requested_model\)\)=lower\(btrim\(k.requested_model\)\) AND r.test_type=k.test_type\s+WHERE.*ORDER BY r.finished_at DESC,r.id DESC`
	mock.ExpectQuery(pattern).WithArgs(evalJSONArgument{keys}, now, service.OpenAIEvalQualityDataVersion, now.Add(-service.OpenAIEvalQualityMaxTTL)).WillReturnRows(rows)
	got, err := repo.LatestModelQualityRuns(context.Background(), keys, now)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.EqualValues(t, 2, got[0].ID)
	require.Equal(t, "low", got[0].ReasoningEffort)
	require.NoError(t, mock.ExpectationsWereMet())
}
