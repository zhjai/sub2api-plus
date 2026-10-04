//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorMarkCheckedPreservesConfigurationRevision(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows int64
		err  error
	}{
		{name: "checked", rows: 1},
		{name: "missing", rows: 0},
		{name: "database_error", err: sql.ErrConnDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := NewChannelMonitorRepository(client, db)
			checkedAt := time.Now().UTC()
			// Only runtime state is written: config edits racing this update must
			// retain their own updated_at and invalidate older probe evidence.
			expect := mock.ExpectExec(regexp.QuoteMeta("UPDATE channel_monitors SET last_checked_at = $1 WHERE id = $2")).WithArgs(checkedAt, int64(7))
			if tc.err != nil {
				expect.WillReturnError(tc.err)
			} else {
				expect.WillReturnResult(sqlmock.NewResult(0, tc.rows))
			}
			err = repo.MarkChecked(context.Background(), 7, checkedAt)
			if tc.err != nil {
				require.True(t, errors.Is(err, tc.err))
			} else if tc.rows == 0 {
				require.ErrorIs(t, err, service.ErrChannelMonitorNotFound)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
