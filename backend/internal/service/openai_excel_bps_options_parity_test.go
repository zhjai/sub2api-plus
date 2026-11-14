package service

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExcelBPSDualGrantRoutesOnlyExplicitMappedModels(t *testing.T) {
	bps := excelBPSAccount(t)
	a := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "native", "client_id": "codex", "chatgpt_account_id": bps.GetCredential("chatgpt_account_id"), "model_mapping": map[string]any{"alias": "gpt-6-astra"}, ExcelBPSCredentialsKey: bps.Credentials}}
	require.Nil(t, a.BPSAccountForModel("alias"), "saving an independent grant must not enable the route")
	a.Extra = map[string]any{ExcelBPSEnabledKey: true, ExcelBPSModelsKey: []any{"gpt-6-astra"}}
	projected := a.BPSAccountForModel("alias")
	require.NotNil(t, projected)
	require.True(t, projected.IsExcelOAuth())
	require.Equal(t, "native", a.GetOpenAIAccessToken())
	require.Nil(t, a.BPSAccountForModel("other"))
	fallback := ExcelBPSNativeFallbackAccount(projected)
	require.NotNil(t, fallback)
	require.Equal(t, "native", fallback.GetOpenAIAccessToken())
	require.False(t, fallback.IsExcelBPSEnabled())
	require.Nil(t, ExcelBPSNativeFallbackAccount(bps))
	require.Equal(t, openai.ExcelClientID, projected.GetCredential("client_id"))
	require.NotContains(t, projected.Credentials, ExcelBPSCredentialsKey)
}
func TestExcelBPSOptionsDefaultsAndManualPreservation(t *testing.T) {
	a := excelBPSAccount(t)
	a.Extra = map[string]any{ExcelBPSAutoRecoverOn403Key: false, ExcelBPSCacheCreationAsInputKey: false}
	ApplyExcelBPSDefaults(a)
	require.Equal(t, false, a.Extra[ExcelBPSAutoRecoverOn403Key])
	require.Equal(t, false, a.Extra[ExcelBPSCacheCreationAsInputKey])
	require.Equal(t, true, a.Extra[ExcelBPSAutoDisableOn403Key])
	require.Equal(t, time.Hour, a.ExcelBPS403RecoveryInterval())
	require.Equal(t, false, a.Extra[ExcelBPSAutoMoveOn403Key])
	for _, v := range []any{0, 1.5, "60", 10081, true} {
		a.Extra[ExcelBPS403RecoveryIntervalMinutesKey] = v
		require.Error(t, ValidateExcelBPSAccountOptions(a))
	}
}
func TestExcelBPSRecoveryOnlyServer403AndManualActive(t *testing.T) {
	now := time.Now()
	a := excelBPSAccount(t)
	a.Status = StatusActive
	a.Schedulable = true
	require.False(t, a.ExcelBPS403RecoveryDue(now))
	a.Extra = map[string]any{ExcelBPS403DisabledAtKey: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), ExcelBPS403DisabledReasonKey: "upstream_403"}
	require.True(t, a.ExcelBPS403RecoveryDue(now))
	require.False(t, a.IsExcelBPSEnabled())
	a.Extra[ExcelBPSEnabledKey] = false
	require.False(t, a.ExcelBPS403RecoveryDue(now))
	a.Extra[ExcelBPSEnabledKey] = true
	a.Schedulable = false
	require.False(t, a.ExcelBPS403RecoveryDue(now))
	a.Schedulable = true
	a.Extra[ExcelBPS403LastProbeAtKey] = now.Add(-59 * time.Minute).Format(time.RFC3339Nano)
	require.False(t, a.ExcelBPS403RecoveryDue(now))
	a.Extra[ExcelBPS403LastProbeAtKey] = now.Add(-61 * time.Minute).Format(time.RFC3339Nano)
	require.True(t, a.ExcelBPS403RecoveryDue(now))
}
func TestExcelBPSRecoveryNeedsExactCompletedResult(t *testing.T) {
	complete := `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"nonce"}]}]}}` + "\n\n"
	require.NoError(t, validateExcelBPSRecoveryResponse([]byte(complete), "nonce"))
	for _, wire := range []string{"", strings.ReplaceAll(complete, "completed", "incomplete"), strings.ReplaceAll(complete, "nonce", "wrong"), `data: {"type":"response.output_text.delta","delta":"nonce"}`} {
		require.Error(t, validateExcelBPSRecoveryResponse([]byte(wire), "nonce"))
	}
}
func TestExcelBPSBillingDownstreamAndLocalBucketsMatch(t *testing.T) {
	raw := `data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":30,"input_tokens_details":{"cached_tokens":20,"cache_write_tokens":30}}}}` + "\n\n"
	a := excelBPSAccount(t)
	reader, measurement := NewExcelBPSBillingReader(a, io.NopCloser(strings.NewReader(raw)))
	wire, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, 30, measurement.UpstreamUsage.CacheCreationInputTokens)
	payload := strings.TrimSpace(strings.TrimPrefix(string(wire), "data:"))
	usage, ok := extractOpenAIUsageFromJSONBytes([]byte(payload))
	require.True(t, ok)
	require.Equal(t, 100, usage.InputTokens)
	require.Equal(t, 20, usage.CacheReadInputTokens)
	require.Zero(t, usage.CacheCreationInputTokens)
	require.Equal(t, int64(30), gjson.Get(payload, "response.usage.upstream_usage.cache_creation_input_tokens").Int())
	a.Extra = map[string]any{ExcelBPSCacheCreationAsInputKey: false}
	untouched := NormalizeExcelBPSUsageJSON(a, []byte(payload), nil)
	require.Equal(t, []byte(payload), untouched)
}
func TestExcelBPSNestedGrantIsSecretAndPreserved(t *testing.T) {
	require.True(t, IsSensitiveCredentialKey(ExcelBPSCredentialsKey))
	existing := map[string]any{"access_token": "native", "client_id": "codex", "model_mapping": map[string]any{"a": "b"}, ExcelBPSCredentialsKey: map[string]any{"access_token": "secret"}}
	edited := MergePreservingSensitiveCreds(existing, map[string]any{"client_id": "codex"})
	require.Contains(t, edited, ExcelBPSCredentialsKey)
	attached := MergePreservingSensitiveCreds(existing, map[string]any{ExcelBPSCredentialsKey: map[string]any{"access_token": "next"}})
	require.Equal(t, "native", attached["access_token"])
	require.Equal(t, "codex", attached["client_id"])
	require.Contains(t, attached, "model_mapping")
}

type parityBPSStateRepo struct {
	AccountRepository
	account *Account
	writes  int
}

func (r *parityBPSStateRepo) CompareAndSwapExcelBPSExtra(_ context.Context, a *Account, patch map[string]any) (bool, error) {
	r.writes++
	r.account = a
	return true, nil
}
func TestExcelBPS403DisablesOnlyChannelWithoutSchedulableMutation(t *testing.T) {
	a := excelBPSAccount(t)
	a.Status = StatusActive
	a.Schedulable = true
	repo := &parityBPSStateRepo{}
	svc := &RateLimitService{accountRepo: repo}
	require.True(t, svc.handleExcelBPS403(context.Background(), a, []byte(`{"error":{"code":"permission_denied"}}`), "gpt-6-astra"))
	require.Equal(t, 1, repo.writes)
	require.True(t, a.Schedulable)
	require.Equal(t, StatusActive, a.Status)
	require.False(t, a.IsExcelBPSEnabled())
	require.False(t, a.ExcelBPS403RecoveryDue(time.Now()))
}
