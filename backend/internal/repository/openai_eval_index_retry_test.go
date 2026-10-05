package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalNormalizedLatestIndexDropsInvalidIndexBeforeRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("SELECT EXISTS \\(").WithArgs(openAIEvalNormalizedLatestIndex).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("DROP INDEX CONCURRENTLY IF EXISTS idx_openai_eval_runs_normalized_latest").WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, prepareNonTransactionalMigration(context.Background(), db, openAIEvalNormalizedLatestIndexMigration))
	require.NoError(t, mock.ExpectationsWereMet())
}
