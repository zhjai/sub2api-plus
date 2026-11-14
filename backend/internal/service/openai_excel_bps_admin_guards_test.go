package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type bpsLocalStatsRepo struct {
	UsageLogRepository
	calls int
	err   error
}

func (r *bpsLocalStatsRepo) GetAccountWindowStats(_ context.Context, _ int64, _ time.Time) (*usagestats.AccountStats, error) {
	r.calls++
	return &usagestats.AccountStats{Requests: 3, Tokens: 12, Cost: 0.25}, r.err
}
func TestExcelBPSUsageKeepsLocalTotalsSeparateFromStoredQuota(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(502) }))
	defer proxy.Close()
	host, port, err := net.SplitHostPort(proxy.Listener.Addr().String())
	require.NoError(t, err)
	portNumber, err := strconv.Atoi(port)
	require.NoError(t, err)
	account := excelBPSAccount(t)
	proxyID := int64(1)
	account.ProxyID, account.Proxy = &proxyID, &Proxy{Protocol: "http", Host: host, Port: portNumber}
	account.Extra = map[string]any{"codex_5h_used_percent": 100.0, "codex_7d_used_percent": 100.0}
	stats := &bpsLocalStatsRepo{}
	svc := &AccountUsageService{usageLogRepo: stats}
	for _, force := range []bool{false, true} {
		usage, err := svc.getUsageForAccount(context.Background(), account, force)
		require.NoError(t, err)
		require.Equal(t, "bps_local", usage.Source)
		require.Equal(t, 100.0, usage.FiveHour.Utilization)
		require.Equal(t, 100.0, usage.SevenDay.Utilization)
		require.Equal(t, "account_codex_snapshot", usage.QuotaSource)
		require.NotEmpty(t, usage.QuotaRefreshError)
		require.EqualValues(t, 3, usage.BPSLocalUsage.FiveHour.Requests)
		require.Equal(t, 0.25, usage.BPSLocalUsage.SevenDay.Cost)
	}
	updates, err := svc.probeOpenAICodexSnapshot(context.Background(), account)
	require.NoError(t, err)
	require.Nil(t, updates)
	require.Zero(t, requests.Load())
	require.Equal(t, 4, stats.calls)
	stats.err = errors.New("local statistics unavailable")
	_, err = svc.getUsageForAccount(context.Background(), account, true)
	require.ErrorContains(t, err, "local statistics unavailable")
}

func TestExcelBPSNativeModelSyncBlockedBeforeTransport(t *testing.T) {
	transport := &httpUpstreamRecorder{}
	svc := &AccountTestService{httpUpstream: transport}
	account := excelBPSAccount(t)
	account.Credentials["model_mapping"] = map[string]any{"bps": "gpt-6-astra"}
	_, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	var syncErr *UpstreamModelSyncError
	require.ErrorAs(t, err, &syncErr)
	require.Equal(t, UpstreamModelSyncErrorConfiguration, syncErr.Kind)
	req, err := svc.buildOpenAIOAuthUpstreamModelsRequest(context.Background(), account)
	require.Error(t, err)
	require.Nil(t, req)
	require.Empty(t, transport.requests)
}

type bpsBulkGuardRepo struct {
	AccountRepository
	accounts []*Account
	writes   int
}

func (r *bpsBulkGuardRepo) GetByIDs(context.Context, []int64) ([]*Account, error) {
	return r.accounts, nil
}
func (r *bpsBulkGuardRepo) BulkUpdate(context.Context, []int64, AccountBulkUpdate) (int64, error) {
	r.writes++
	return int64(len(r.accounts)), nil
}
func TestExcelBPSBulkGrantGuardRunsBeforeAnyWrite(t *testing.T) {
	excel := excelBPSAccount(t)
	native := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"client_id": openai.ClientID}}
	for _, patch := range []map[string]any{
		{"client_id": openai.ClientID}, {"client_id": openai.ExcelClientID}, {"client_id": nil},
		{"access_token": "replacement"}, {"refresh_token": "replacement"}, {"id_token": "replacement"},
		{"chatgpt_account_id": "different"}, {"auth_mode": "agent_identity"},
	} {
		repo := &bpsBulkGuardRepo{accounts: []*Account{native, excel}}
		svc := &adminServiceImpl{accountRepo: repo}
		_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{native.ID, excel.ID}, Credentials: patch})
		require.Error(t, err)
		require.Zero(t, repo.writes)
	}
	repo := &bpsBulkGuardRepo{accounts: []*Account{native}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{native.ID}, Credentials: map[string]any{"access_token": excel.GetOpenAIAccessToken()}})
	require.Error(t, err)
	require.Zero(t, repo.writes)
	repo.accounts = []*Account{native, excel}
	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{native.ID, excel.ID}, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "gpt-6-astra"}}})
	require.NoError(t, err)
	require.Equal(t, 1, repo.writes)
	require.Len(t, result.SuccessIDs, 2)
}

func TestExcelBPSModelPickerRequiresRealDiscovery(t *testing.T) {
	svc := &AccountTestService{}
	account := excelBPSAccount(t)
	account.Credentials["model_mapping"] = map[string]any{"bps-model": "gpt-6-astra"}
	_, err := svc.FetchOpenAIAccountModels(context.Background(), account)
	require.ErrorContains(t, err, "discovery")
}

func TestExcelBPSNeverEnablesCodexAutoResetFromStoredSettings(t *testing.T) {
	account := excelBPSAccount(t)
	account.Extra = map[string]any{OpenAIAutoResetCreditEnabledExtraKey: true}
	require.False(t, ResolveOpenAIAutoResetCreditConfig(account).Enabled)
	account.Credentials["client_id"] = openai.ClientID
	require.True(t, ResolveOpenAIAutoResetCreditConfig(account).Enabled)
}
