package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/stretchr/testify/require"
)

type bpsCapabilityInventory struct {
	AccountRepository
	accounts []Account
	err      error
}

func (r bpsCapabilityInventory) ListByGroup(context.Context, int64) ([]Account, error) {
	return r.accounts, r.err
}

func TestExcelBPSPreSendLastResortRequiresManualOptInAndNoCompleteRoute(t *testing.T) {
	account := excelBPSAccount(t)
	account.Extra = map[string]any{ExcelBPSHistoricalEncryptedKey: true}
	raw := []byte(`{"model":"gpt-6-astra","input":[{"role":"assistant","content":[{"type":"encrypted_content","encrypted_content":"old"}]},{"role":"user","content":"next"}]}`)
	_, err := applyExcelBPSOptions(raw, account)
	var capability *basispoints.ContextCapabilityError
	require.ErrorAs(t, err, &capability)
	groupID := int64(1)
	svc := &OpenAIGatewayService{accountRepo: bpsCapabilityInventory{accounts: []Account{*account}}}
	require.True(t, svc.ExcelBPSCanUseLastResort(t.Context(), &groupID, account, "gpt-6-astra", raw))
	out, err := applyExcelBPSOptionsForContext(WithExcelBPSLastResort(t.Context()), raw, account)
	require.NoError(t, err)
	require.Contains(t, string(out), "prior facts may be missing")
	account.Extra[ExcelBPSHistoricalEncryptedKey] = false
	require.False(t, svc.ExcelBPSCanUseLastResort(t.Context(), &groupID, account, "gpt-6-astra", raw))
	account.Extra[ExcelBPSHistoricalEncryptedKey] = true
	native := Account{ID: 992, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"chatgpt_account_id": "workspace-test", "access_token": "native-fixture"}}
	until := time.Now().Add(time.Hour)
	native.RateLimitResetAt = &until
	svc.accountRepo = bpsCapabilityInventory{accounts: []Account{*account, native}}
	require.False(t, svc.ExcelBPSCanUseLastResort(t.Context(), &groupID, account, "gpt-6-astra", raw), "a temporary native rate limit must not authorize context loss")
	svc.accountRepo = bpsCapabilityInventory{err: errors.New("state outage")}
	require.False(t, svc.ExcelBPSCanUseLastResort(t.Context(), &groupID, account, "gpt-6-astra", raw))
}

func TestExcelBPSPreSendNativeFallbackRetainsPrimaryCredentials(t *testing.T) {
	grant := excelBPSAccount(t)
	primary := Account{ID: grant.ID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "native-token", ExcelBPSCredentialsKey: grant.Credentials}, Extra: map[string]any{ExcelBPSEnabledKey: true}}
	raw := []byte(`{"model":"gpt-6-astra","tools":[{"type":"web_search"}],"input":"search"}`)
	routed, capability := ExcelBPSPreSendRoute(t.Context(), &primary, "gpt-6-astra", raw)
	require.Nil(t, capability)
	require.NotNil(t, routed)
	require.Equal(t, primary.ID, routed.ID)
	require.Equal(t, "native-token", routed.GetOpenAIAccessToken())
	require.False(t, routed.IsExcelBPSEnabled())
	require.True(t, primary.IsExcelBPSEnabled())
	_, capability = ExcelBPSPreSendRoute(t.Context(), grant, "gpt-6-astra", raw)
	require.NotNil(t, capability)
	require.False(t, capability.RequiresCompatibleState)
}

func TestExcelBPSLastResortSeparatesDurableEligibilityFromTemporaryCapacity(t *testing.T) {
	bps := excelBPSAccount(t)
	bps.Status, bps.Schedulable = StatusActive, true
	bps.Extra = map[string]any{ExcelBPSHistoricalEncryptedKey: true}
	raw := []byte(`{"model":"gpt-6-astra","input":[{"role":"assistant","content":[{"type":"encrypted_content","encrypted_content":"old"}]},{"role":"user","content":"continue"}]}`)
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	cases := []struct {
		name   string
		change func(*Account)
		allow  bool
	}{
		{"eligible native", func(*Account) {}, false},
		{"manual scheduling disabled", func(a *Account) { a.Schedulable = false }, true},
		{"account disabled", func(a *Account) { a.Status = StatusDisabled }, true},
		{"expired account with enforced expiry", func(a *Account) { a.ExpiresAt = &past; a.AutoPauseOnExpired = true }, true},
		{"expired account without expiry gate", func(a *Account) { a.ExpiresAt = &past }, false},
		{"future enforced expiry", func(a *Account) { a.ExpiresAt = &future; a.AutoPauseOnExpired = true }, false},
		{"refreshable credential expiry", func(a *Account) { a.Credentials["expires_at"] = past.Format(time.RFC3339) }, false},
		{"explicit model exclusion", func(a *Account) { a.Credentials["model_mapping"] = map[string]any{"another-model": "gpt-6.1-sol"} }, true},
		{"matching model mapping", func(a *Account) { a.Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "gpt-6-astra"} }, false},
		{"temporary rate limit", func(a *Account) { a.RateLimitResetAt = &future }, false},
		{"temporary overload", func(a *Account) { a.OverloadUntil = &future }, false},
		{"temporary cooldown", func(a *Account) { a.TempUnschedulableUntil = &future }, false},
		{"uncertain error status", func(a *Account) { a.Status = StatusError }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			native := Account{ID: 992, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"chatgpt_account_id": "workspace-test", "access_token": "native-fixture"}}
			tc.change(&native)
			svc := &OpenAIGatewayService{accountRepo: bpsCapabilityInventory{accounts: []Account{*bps, native}}}
			groupID := int64(1)
			require.Equal(t, tc.allow, svc.ExcelBPSCanUseLastResort(t.Context(), &groupID, bps, "gpt-6-astra", raw))
		})
	}
}

func TestExcelBPSLastResortExcludesOnlyKnownIncompatibleWorkspace(t *testing.T) {
	oldHistory := []byte(`{"model":"gpt-6-astra","input":[{"role":"assistant","content":[{"type":"encrypted_content","encrypted_content":"old"}]},{"role":"user","content":"continue"}]}`)
	currentTask := []byte(`{"model":"gpt-6-astra","input":[{"role":"user","content":[{"type":"encrypted_content","encrypted_content":"current"}]}]}`)
	hosted := []byte(`{"model":"gpt-6-astra","input":"search","tools":[{"type":"web_search"}]}`)
	future := time.Now().Add(time.Hour)
	cases := []struct {
		name, workspace string
		body            []byte
		manual, allow   bool
		busy            func(*Account)
	}{
		{"known different workspace", "different-workspace", oldHistory, true, true, nil},
		{"compatible available", "workspace-test", oldHistory, true, false, nil},
		{"compatible rate limited", "workspace-test", oldHistory, true, false, func(a *Account) { a.RateLimitResetAt = &future }},
		{"compatible overloaded", "workspace-test", oldHistory, true, false, func(a *Account) { a.OverloadUntil = &future }},
		{"compatible cooling", "workspace-test", oldHistory, true, false, func(a *Account) { a.TempUnschedulableUntil = &future }},
		{"unknown identity remains conservative", "", oldHistory, true, false, nil},
		{"different workspace without manual optin", "different-workspace", oldHistory, false, false, nil},
		{"different workspace current task protected", "different-workspace", currentTask, true, false, nil},
		{"hosted capability without issuer bound state", "different-workspace", hosted, true, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origin := excelBPSAccount(t)
			origin.Status, origin.Schedulable = StatusActive, true
			origin.Extra = map[string]any{ExcelBPSHistoricalEncryptedKey: tc.manual, ExcelBPSHostedToolsPolicyKey: "omit"}
			native := Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "native-fixture", "chatgpt_account_id": tc.workspace}}
			if tc.busy != nil {
				tc.busy(&native)
			}
			if tc.workspace == "different-workspace" {
				require.False(t, ExcelBPSCompatibleContextAccount(origin, &native))
			}
			if tc.workspace == "workspace-test" {
				require.True(t, ExcelBPSCompatibleContextAccount(origin, &native))
			}
			svc := &OpenAIGatewayService{accountRepo: bpsCapabilityInventory{accounts: []Account{*origin, native}}}
			group := int64(1)
			require.Equal(t, tc.allow, svc.ExcelBPSCanUseLastResort(t.Context(), &group, origin, "gpt-6-astra", tc.body))
		})
	}
}
