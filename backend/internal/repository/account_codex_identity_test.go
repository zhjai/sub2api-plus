package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestCodexIdentityBindingChecksRealKeyAndDuplicateRows(t *testing.T) {
	for _, scenario := range []string{"valid", "missing_key", "disabled_key", "expired_key", "quota", "principal_changed", "disabled_owner", "duplicate_same_key", "duplicate_other_key", "stale_namespace"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			a := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "shared"}, Extra: map[string]any{service.CodexIdentityModeKey: service.CodexIdentityPreserveClient, service.CodexIdentityAPIKeyIDKey: 77}}
			require.NoError(t, service.NormalizeCodexIdentityConfig(a, nil))
			status := "active"
			var expiry any
			quota, used := 0., 0.
			if scenario == "disabled_key" {
				status = "disabled"
			}
			if scenario == "expired_key" {
				expiry = time.Now().Add(-time.Hour)
			}
			if scenario == "quota" {
				quota, used = 1, 1
			}
			keyRows := sqlmock.NewRows([]string{"id", "user_id", "status", "deleted_at", "expires_at", "quota", "quota_used"})
			if scenario != "missing_key" {
				keyRows.AddRow(77, 9, status, nil, expiry, quota, used)
			}
			mock.ExpectQuery(`SELECT .* FROM "api_keys"`).WillReturnRows(keyRows)
			keyUnavailable := scenario == "missing_key" || scenario == "disabled_key" || scenario == "expired_key" || scenario == "quota"
			if !keyUnavailable && scenario != "principal_changed" {
				ownerStatus := "active"
				if scenario == "disabled_owner" {
					ownerStatus = "disabled"
				}
				mock.ExpectQuery(`SELECT .* FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"id", "status", "deleted_at"}).AddRow(9, ownerStatus, nil))
				if scenario != "disabled_owner" {
					rows := sqlmock.NewRows([]string{"id", "platform", "type", "credentials", "extra"})
					if scenario != "valid" {
						credentials := `{"chatgpt_account_id":"shared"}`
						if scenario == "stale_namespace" {
							credentials = `{"chatgpt_account_id":"different"}`
						}
						extra := map[string]any{service.CodexIdentityModeKey: service.CodexIdentityPreserveClient, service.CodexIdentityNamespaceKey: service.CodexIdentityNamespace(a), service.CodexIdentityAPIKeyIDKey: 77}
						if scenario == "duplicate_other_key" {
							extra[service.CodexIdentityAPIKeyIDKey] = 78
						}
						raw, _ := json.Marshal(extra)
						rows.AddRow(42, "openai", "oauth", []byte(credentials), raw)
					}
					mock.ExpectQuery(`SELECT .* FROM "accounts"`).WillReturnRows(rows)
				}
			}
			userID := int64(9)
			if scenario == "principal_changed" {
				userID = 10
			}
			err = validateCodexIdentityBinding(context.Background(), client, a, 77, userID)
			if scenario == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCodexIdentityConcurrentSaveConflictIsBadRequest(t *testing.T) {
	err := translatePersistenceError(&pq.Error{Code: "23505", Constraint: "accounts_codex_identity_namespace_unique", Message: `duplicate key violates unique constraint "accounts_codex_identity_namespace_unique"`}, nil, nil)
	require.ErrorContains(t, err, "actual credential namespace")
}

func TestCodexIdentityUnchangedSaveDoesNotRequireLiveKey(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_edit", true: "new_key_binding"}[changed], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			a := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Credentials: map[string]any{"chatgpt_account_id": "shared"},
				Extra:       map[string]any{service.CodexIdentityModeKey: service.CodexIdentityPreserveClient, service.CodexIdentityAPIKeyIDKey: 77}}
			require.NoError(t, service.NormalizeCodexIdentityConfig(a, nil))
			credentials, err := json.Marshal(a.Credentials)
			require.NoError(t, err)
			extra, err := json.Marshal(a.Extra)
			require.NoError(t, err)
			mock.ExpectQuery(`SELECT .* FROM "accounts"`).WillReturnRows(sqlmock.NewRows(
				[]string{"id", "platform", "type", "credentials", "extra"}).AddRow(41, "openai", "oauth", credentials, extra))
			if changed {
				a.Extra[service.CodexIdentityAPIKeyIDKey] = 78
				mock.ExpectQuery(`SELECT .* FROM "api_keys"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
			}
			err = normalizeAccountCodexIdentity(context.Background(), client, a)
			if changed {
				require.Equal(t, "CODEX_IDENTITY_API_KEY_UNAVAILABLE", infraerrors.Reason(err))
			} else {
				require.NoError(t, err, "unchanged grants must not query key liveness during unrelated edits")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCodexIdentityFingerprintPatchAndBulkConflictIsBadRequest(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		t.Run(map[bool]string{false: "patch", true: "bulk"}[bulk], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := newAccountRepositoryWithSQL(client, db, nil)
			mock.ExpectBegin()
			mock.ExpectExec(`UPDATE accounts SET extra =`).WillReturnError(&pq.Error{
				Code: "23514", Constraint: "accounts_codex_identity_config_check",
				Message: `violates check constraint "accounts_codex_identity_config_check"`,
			})
			mock.ExpectRollback()
			extra := map[string]any{"codex_fingerprint_mode": "device"}
			if bulk {
				_, err = repo.BulkUpdate(context.Background(), []int64{41}, service.AccountBulkUpdate{Extra: extra})
			} else {
				err = repo.UpdateExtra(context.Background(), 41, extra)
			}
			require.Equal(t, 400, infraerrors.Code(err))
			require.Equal(t, "CODEX_IDENTITY_FINGERPRINT_CONFLICT", infraerrors.Reason(err))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
