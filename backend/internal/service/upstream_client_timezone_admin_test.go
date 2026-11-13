//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUpstreamClientTimezoneAdminCreateRejectsInvalid(t *testing.T) {
	_, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, map[string]any{UpstreamClientTimezoneModeKey: "manual", UpstreamClientTimezoneKey: "Mars/Olympus"})
	require.Error(t, err)
	_, err = buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, map[string]any{ExcelBPSHostedToolsPolicyKey: "unknown"})
	require.Error(t, err)
}
func TestUpstreamClientTimezoneAdminUpdatePreservesAndValidates(t *testing.T) {
	account := timezoneTestAccount("manual", "America/New_York")
	account.Type = AccountTypeOAuth
	account.Status = StatusActive
	account.Extra[ExcelBPSHistoricalEncryptedKey] = true
	repo := &updateAccountCredsRepoStub{account: account}
	svc := &adminServiceImpl{accountRepo: repo}
	updated, err := svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{Extra: map[string]any{"unrelated": true}})
	require.NoError(t, err)
	require.Equal(t, "manual", updated.Extra[UpstreamClientTimezoneModeKey])
	require.Equal(t, "America/New_York", updated.Extra[UpstreamClientTimezoneKey])
	require.Equal(t, true, updated.Extra[ExcelBPSHistoricalEncryptedKey])
	_, err = svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{Extra: map[string]any{UpstreamClientTimezoneKey: "Local"}})
	require.Error(t, err)
	require.Equal(t, 1, repo.updateCalls)
}
func TestUpstreamClientTimezoneAdminBulkValidatesBeforeWrite(t *testing.T) {
	account := timezoneTestAccount("manual", "America/New_York")
	account.Type = AccountTypeOAuth
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{account}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Extra: map[string]any{UpstreamClientTimezoneKey: "Local"}})
	require.Error(t, err)
	require.Zero(t, repo.bulkUpdateCalls)
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, Extra: map[string]any{UpstreamClientTimezoneKey: "UTC"}})
	require.NoError(t, err)
	require.Equal(t, 1, repo.bulkUpdateCalls)
}
