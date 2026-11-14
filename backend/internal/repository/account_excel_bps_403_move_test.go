package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestExcelBPSMoveOnlyUsesValidatedTargetAndAtomicCAS(t *testing.T) {
	cases := []struct {
		name        string
		target      any
		queryTarget bool
		targetFound bool
		conflict    bool
		writeFail   bool
	}{
		{"json_number", json.Number("71"), true, true, false, false},
		{"int", 71, true, true, false, false},
		{"int64", int64(71), true, true, false, false},
		{"float64", float64(71), true, true, false, false},
		{"unavailable_target", json.Number("71"), true, false, false, false},
		{"fraction", 71.5, false, false, false, false},
		{"fraction_number", json.Number("71.5"), false, false, false, false},
		{"overflow", json.Number("9223372036854775808"), false, false, false, false},
		{"float_overflow", math.Inf(1), false, false, false, false},
		{"zero", json.Number("0"), false, false, false, false},
		{"negative", -71, false, false, false, false},
		{"string", "71", false, false, false, false},
		{"concurrent_change", json.Number("71"), false, false, true, false},
		{"binding_write_failure", json.Number("71"), true, true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Infinity is not valid JSON metadata; its parser case is checked separately.
			if tc.name == "float_overflow" {
				_, ok := excelBPSGroupID(tc.target)
				require.False(t, ok)
				return
			}
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			defer client.Close()
			repo := &accountRepository{client: client}
			extra := map[string]any{service.ExcelBPSAutoDisableOn403Key: false, service.ExcelBPSAutoMoveOn403Key: true, service.ExcelBPS403TargetGroupIDKey: tc.target}
			expectedExtra, err := json.Marshal(extra)
			require.NoError(t, err)
			a := &service.Account{ID: 42, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "synthetic"}, Extra: extra}
			moveTime := "2026-11-14T07:00:00Z"
			updates := map[string]any{service.ExcelBPS403MoveRequestedAtKey: moveTime}
			mock.ExpectBegin()
			affected := int64(1)
			if tc.conflict {
				affected = 0
			}
			mock.ExpectExec(`UPDATE accounts SET extra.*credentials=\$4::jsonb.*schedulable=true AND status='active'`).
				WithArgs("{}", int64(42), string(expectedExtra), `{"access_token":"synthetic"}`).
				WillReturnResult(sqlmock.NewResult(0, affected))
			if tc.conflict {
				mock.ExpectRollback()
			} else {
				if tc.queryTarget {
					rows := sqlmock.NewRows([]string{"id"})
					if tc.targetFound {
						rows.AddRow(int64(71))
					}
					mock.ExpectQuery(`FROM "groups".*"id" = \$1.*"deleted_at" IS NULL.*"status" = \$2.*"platform" IN.*FOR SHARE`).
						WithArgs(int64(71), service.StatusActive, service.PlatformOpenAI, service.PlatformComposite).
						WillReturnRows(rows)
				}
				if tc.targetFound {
					mock.ExpectQuery(`FROM "account_groups".*"account_id" = \$1`).WithArgs(int64(42)).
						WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id"}).AddRow(int64(42), int64(19)))
					mock.ExpectExec(`DELETE FROM "account_groups".*"account_id" = \$1`).WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
					create := mock.ExpectExec(`INSERT INTO "account_groups"`)
					if tc.writeFail {
						create.WillReturnError(errors.New("synthetic binding failure"))
					} else {
						create.WillReturnResult(sqlmock.NewResult(0, 1))
					}
					if !tc.writeFail {
						mock.ExpectExec(`UPDATE accounts SET extra=extra \|\| \$1::jsonb WHERE id=\$2`).
							WithArgs(fmt.Sprintf(`{"openai_excel_bps_403_moved_at":%q,"openai_excel_bps_403_moved_group_id":71}`, moveTime), int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
						mock.ExpectExec(`INSERT INTO scheduler_outbox`).WillReturnResult(sqlmock.NewResult(1, 1))
					}
				}
				if tc.writeFail {
					mock.ExpectRollback()
				} else {
					mock.ExpectExec(`INSERT INTO scheduler_outbox`).WillReturnResult(sqlmock.NewResult(1, 1))
					mock.ExpectCommit()
				}
			}
			changed, err := repo.CompareAndSwapExcelBPSExtra(context.Background(), a, updates)
			if tc.writeFail {
				require.Error(t, err)
				require.False(t, changed)
			} else {
				require.NoError(t, err)
				require.Equal(t, !tc.conflict, changed)
			}
			require.Equal(t, map[string]any{service.ExcelBPS403MoveRequestedAtKey: moveTime}, updates, "CAS must not mutate caller intent")
			require.True(t, a.Schedulable)
			require.NotContains(t, a.Extra, service.ExcelBPS403DisabledAtKey)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
