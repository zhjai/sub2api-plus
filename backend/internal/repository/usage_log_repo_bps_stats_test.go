//go:build unit

package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"regexp"
	"testing"
	"time"
)

func TestGetAccountBPSWindowStatsFiltersActualUpstreamEndpoint(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE account_id = $1 AND created_at >= $2 AND upstream_endpoint LIKE '/basispoints/api/%'")).
		WithArgs(int64(42), start).
		WillReturnRows(sqlmock.NewRows([]string{"requests", "tokens", "cost", "standard_cost", "user_cost"}).AddRow(2, 16, .25, .5, .75))
	stats, err := repo.GetAccountBPSWindowStats(context.Background(), 42, start)
	require.NoError(t, err)
	require.EqualValues(t, 2, stats.Requests)
	require.EqualValues(t, 16, stats.Tokens)
	require.Equal(t, .25, stats.Cost)
	require.Equal(t, .5, stats.StandardCost)
	require.Equal(t, .75, stats.UserCost)
	require.NoError(t, mock.ExpectationsWereMet())
}
