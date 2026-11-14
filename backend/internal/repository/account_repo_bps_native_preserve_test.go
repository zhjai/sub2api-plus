package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBPSNativeLockedUpdatePreservesConcurrentGrant(t *testing.T) {
	for _, scenario := range []string{"attached", "rotated", "removed", "workspace_changed"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			defer client.Close()
			account := &service.Account{ID: 27, Name: "native", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Credentials: map[string]any{"client_id": openai.ClientID, "access_token": "new-native", "chatgpt_account_id": "workspace"}}
			if scenario == "rotated" || scenario == "removed" {
				account.Credentials[service.ExcelBPSCredentialsKey] = map[string]any{"access_token": "stale"}
			}
			if scenario == "workspace_changed" {
				account.Credentials["chatgpt_account_id"] = "different"
			}
			claims, _ := json.Marshal(map[string]any{"client_id": openai.ExcelClientID, "exp": time.Now().Add(time.Hour).Unix(), "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "workspace"}})
			grant := map[string]any{"client_id": openai.ExcelClientID, "access_token": "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".test", "refresh_token": "rotated-refresh", "chatgpt_account_id": "workspace"}
			current := map[string]any{"client_id": openai.ClientID, "access_token": "old-native", "chatgpt_account_id": "workspace"}
			if scenario != "removed" {
				current[service.ExcelBPSCredentialsKey] = grant
			}
			currentJSON, _ := json.Marshal(current)
			mock.ExpectBegin()
			tx, err := client.Tx(context.Background())
			require.NoError(t, err)
			mock.ExpectQuery(`(?s)SELECT.*FOR NO KEY UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"identity", "ollama_group", "ollama_proxy", "enabled", "rate_sync", "snapshot", "ollama_session", "ollama_auto", "ollama_snapshot", "opencode_group", "opencode_auto", "opencode_snapshot", "grok_models", "model_metadata"}).AddRow(false, false, true, nil, nil, nil, nil, nil, nil, false, nil, nil, nil, nil))
			mock.ExpectQuery(`SELECT.*FROM "accounts"`).WithArgs(int64(27)).WillReturnRows(sqlmock.NewRows([]string{"id", "platform", "type", "credentials", "extra"}).AddRow(27, service.PlatformOpenAI, service.AccountTypeOAuth, currentJSON, []byte(`{}`)))
			stop := errors.New("synthetic write reached")
			if scenario != "workspace_changed" {
				mock.ExpectExec(`UPDATE "accounts"`).WillReturnError(stop)
			}
			mock.ExpectRollback()
			repo := &accountRepository{client: client}
			_, err = repo.updateLockedAccount(context.Background(), tx.Client(), account, nil, nil, nil)
			require.NoError(t, tx.Rollback())
			if scenario == "workspace_changed" {
				require.ErrorContains(t, err, "same workspace")
			} else {
				require.ErrorIs(t, err, stop)
			}
			if scenario == "removed" {
				require.NotContains(t, account.Credentials, service.ExcelBPSCredentialsKey)
			} else {
				require.Equal(t, grant, account.Credentials[service.ExcelBPSCredentialsKey])
			}
			require.Equal(t, "new-native", account.GetOpenAIAccessToken())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
