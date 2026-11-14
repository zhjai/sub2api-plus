package repository

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestExcelBPSRecoveryCASRejectsConcurrentEditsAndClaimsBeforeSend(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		t.Run(string(rune('0'+affected)), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			defer client.Close()
			repo := &accountRepository{client: client}
			a := &service.Account{ID: 42, Credentials: map[string]any{"access_token": "synthetic"}, Extra: map[string]any{service.ExcelBPSEnabledKey: true, service.ExcelBPS403DisabledReasonKey: "upstream_403", "unrelated_metadata": "ignored"}}
			mock.ExpectBegin()
			mock.ExpectExec(`UPDATE accounts SET extra.*credentials=\$4::jsonb.*schedulable=true AND status='active'`).WithArgs(sqlmock.AnyArg(), int64(42), `{"openai_excel_bps":true,"openai_excel_bps_403_disabled_reason":"upstream_403"}`, `{"access_token":"synthetic"}`).WillReturnResult(sqlmock.NewResult(0, affected))
			if affected > 0 {
				mock.ExpectExec(`INSERT INTO scheduler_outbox`).WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			changed, err := repo.CompareAndSwapExcelBPSExtra(context.Background(), a, map[string]any{service.ExcelBPS403LastProbeAtKey: time.Now().UTC().Format(time.RFC3339Nano)})
			require.NoError(t, err)
			require.Equal(t, affected > 0, changed)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
func TestExcelBPSGrantCASCannotOverwritePrimaryCredentials(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	repo := &accountRepository{client: client}
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE accounts SET credentials=jsonb_set\(credentials,'\{excel_bps_credentials\}',\$1::jsonb,true\).*credentials->'excel_bps_credentials'=\$4::jsonb`).WithArgs(`{"access_token":"new"}`, sqlmock.AnyArg(), int64(42), `{"access_token":"old"}`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO scheduler_outbox`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	changed, err := repo.ReplaceExcelBPSGrant(context.Background(), 42, map[string]any{"access_token": "old"}, map[string]any{"access_token": "new"})
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, mock.ExpectationsWereMet())
}
