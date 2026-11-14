package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type bpsNativeGuardRepo struct {
	AccountRepository
	account *Account
	writes  int
}

func (r *bpsNativeGuardRepo) GetByID(context.Context, int64) (*Account, error) {
	a := *r.account
	a.Credentials = shallowCopyMap(a.Credentials)
	return &a, nil
}

var bpsNativeReachedUpdate = errors.New("synthetic update reached")

func (r *bpsNativeGuardRepo) Update(context.Context, *Account) error {
	r.writes++
	return bpsNativeReachedUpdate
}
func (r *bpsNativeGuardRepo) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	return nil, nil
}

func TestBPSNativeCredentialUpdateChecksPreservedWorkspace(t *testing.T) {
	for _, workspace := range []string{"workspace-test", "different-workspace"} {
		t.Run(workspace, func(t *testing.T) {
			a := dualBPSEvalFixture(t)
			grant := shallowCopyMap(a.Credentials[ExcelBPSCredentialsKey].(map[string]any))
			grant["refresh_token"] = "synthetic-refresh"
			a.Credentials[ExcelBPSCredentialsKey] = grant
			repo := &bpsNativeGuardRepo{account: a}
			svc := &adminServiceImpl{accountRepo: repo}
			_, err := svc.UpdateAccount(t.Context(), a.ID, &UpdateAccountInput{Credentials: map[string]any{"chatgpt_account_id": workspace, "access_token": "new-native"}})
			if workspace == "workspace-test" {
				require.ErrorIs(t, err, bpsNativeReachedUpdate)
				require.Equal(t, 1, repo.writes)
			} else {
				require.ErrorContains(t, err, "same workspace")
				require.Zero(t, repo.writes)
			}
		})
	}
}

func TestBPSNativeExistingExpiredGrantAllowsUnrelatedEdit(t *testing.T) {
	a := dualBPSEvalFixture(t)
	grant := shallowCopyMap(a.Credentials[ExcelBPSCredentialsKey].(map[string]any))
	grant["access_token"] = excelTestJWT(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	grant["refresh_token"] = "synthetic-refresh"
	a.Credentials[ExcelBPSCredentialsKey] = grant
	require.NoError(t, ValidateExcelBPSIndependentWorkspace(a))
	require.Error(t, ValidateExcelBPSIndependentGrant(a), "explicit replacement still requires a valid new grant")
	repo := &bpsNativeGuardRepo{account: a}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.UpdateAccount(t.Context(), a.ID, &UpdateAccountInput{Credentials: map[string]any{"model_mapping": map[string]any{"alias": "gpt-6-astra"}}})
	require.ErrorIs(t, err, bpsNativeReachedUpdate)
	require.Equal(t, 1, repo.writes)
}
