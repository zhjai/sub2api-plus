package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestUpdateOpenAIBPSModelStateLocksAndMergesCurrentState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)

	model := "gpt-6-astra"
	key := service.OpenAIBPSModelStateExtraKeyFor(model)
	updatedAt := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT COALESCE(extra -> ($2::text), '{}'::jsonb)")+`.*`+regexp.QuoteMeta("FOR UPDATE")).
		WithArgs(int64(17), key).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow([]byte(`{"active":true,"degraded_streak":3,"healthy_streak":0,"updated_at":"2026-09-30T11:00:00Z"}`)))
	mock.ExpectExec(`(?s)`+regexp.QuoteMeta("UPDATE accounts")+`.*`+regexp.QuoteMeta("jsonb_set")+`.*`+regexp.QuoteMeta("WHERE id = $1 AND deleted_at IS NULL")).
		WithArgs(int64(17), key, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(17), nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = repo.UpdateOpenAIBPSModelState(context.Background(), 17, model, func(current service.OpenAIBPSModelState) (service.OpenAIBPSModelState, bool) {
		require.True(t, current.Active)
		require.Equal(t, 3, current.DegradedStreak)
		current.Active = false
		current.DisabledReason = "upstream_403"
		current.UpdatedAt = updatedAt
		return current, true
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateOpenAIBPSAccountStateMigratesLegacyModelState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)

	legacyKey := service.OpenAIBPSModelStateExtraKeyFor("gpt-6-astra")
	accountKey := service.OpenAIBPSAccountStateExtraKey()
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT COALESCE(extra, '{}'::jsonb)") + `.*` + regexp.QuoteMeta("FOR UPDATE")).
		WithArgs(int64(18)).
		WillReturnRows(sqlmock.NewRows([]string{"extra"}).AddRow([]byte(`{"` + legacyKey + `":{"active":true,"degraded_streak":3,"updated_at":"2026-09-30T11:00:00Z"}}`)))
	mock.ExpectExec(`(?s)`+regexp.QuoteMeta("UPDATE accounts")+`.*`+regexp.QuoteMeta("jsonb_set")+`.*`+regexp.QuoteMeta("WHERE id = $1 AND deleted_at IS NULL")).
		WithArgs(int64(18), accountKey, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(18), nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = repo.UpdateOpenAIBPSAccountState(context.Background(), 18, func(current service.OpenAIBPSAccountState) (service.OpenAIBPSAccountState, bool) {
		require.False(t, current.Active)
		require.Zero(t, current.DegradedStreak)
		return current, false
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
