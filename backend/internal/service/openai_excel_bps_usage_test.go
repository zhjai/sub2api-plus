package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestExcelBPSQuotaUsesReadOnlyActualGrant(t *testing.T) {
	for _, status := range []int{200, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			a := excelBPSAccount(t)
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{a.ID: a}}
			tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(a): a.GetOpenAIAccessToken()}}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "/backend-api/wham/usage", r.URL.Path)
				require.Equal(t, "Bearer "+a.GetOpenAIAccessToken(), r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status == 200 {
					_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":27,"limit_window_seconds":18000,"reset_after_seconds":300}}}`))
				} else {
					_, _ = w.Write([]byte(`{"error":{"message":"usage not permitted for this grant"}}`))
				}
			}))
			defer server.Close()
			quota := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), newQuotaRedirectingFactory(server), nil)
			usageSvc := &AccountUsageService{cache: NewUsageCache(), usageLogRepo: &bpsLocalStatsRepo{}, openAIQuotaService: quota}
			usage, err := usageSvc.getOpenAIUsage(context.Background(), a, true)
			require.NoError(t, err)
			require.NotNil(t, usage.BPSLocalUsage)
			if status == 200 {
				require.Equal(t, 27.0, usage.FiveHour.Utilization)
				require.Equal(t, "chatgpt_wham_usage", usage.QuotaSource)
				require.NotNil(t, usage.QuotaUpdatedAt)
				require.Empty(t, usage.QuotaRefreshError)
			} else {
				require.Nil(t, usage.FiveHour)
				require.Nil(t, usage.SevenDay)
				require.Contains(t, usage.QuotaRefreshError, "403")
			}
			require.Equal(t, 1, calls, "BPS-only quota read must not query native reset cards")
			_, _, _, _, err = quota.prepareUpstreamCall(context.Background(), a.ID)
			require.Error(t, err, "native mutations keep their own eligibility guard")
		})
	}
}

func TestOpenAIUsageLocalStatsDoNotInventQuota(t *testing.T) {
	svc := &AccountUsageService{usageLogRepo: &bpsLocalStatsRepo{}}
	a := &Account{ID: 51, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	u, err := svc.getOpenAIUsage(context.Background(), a, false)
	require.NoError(t, err)
	require.Nil(t, u.FiveHour)
	require.Nil(t, u.SevenDay)
	// An authoritative zero is still a valid snapshot.
	a.Extra = map[string]any{"codex_5h_used_percent": 0.0, "codex_5h_window_minutes": 300, "codex_usage_updated_at": time.Now().Format(time.RFC3339)}
	u, err = svc.getOpenAIUsage(context.Background(), a, false)
	require.NoError(t, err)
	require.NotNil(t, u.FiveHour)
	require.Zero(t, u.FiveHour.Utilization)
	require.NotNil(t, u.FiveHour.WindowStats)
}

func TestExcelBPSQuotaErrorsRedactBothGrants(t *testing.T) {
	a := excelBPSAccount(t)
	a.Credentials = map[string]any{"access_token": "native-secret", "refresh_token": "native-refresh", ExcelBPSCredentialsKey: a.Credentials}
	repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{a.ID: a}}
	svc := &OpenAIQuotaService{accountRepo: repo}
	grant := a.ExcelBPSCredentialAccount()
	require.NotNil(t, grant)
	raw := "native-secret native-refresh " + grant.GetOpenAIAccessToken() + " " + grant.GetCredential("refresh_token")
	redacted := svc.redactQuotaErrorBody(context.Background(), a.ID, raw)
	require.NotContains(t, redacted, "native-secret")
	require.NotContains(t, redacted, "native-refresh")
	require.NotContains(t, redacted, grant.GetOpenAIAccessToken())
	require.Contains(t, redacted, "[REDACTED]")
}

type dualBPSStatsRepo struct {
	bpsLocalStatsRepo
	bpsCalls int
}

func (r *dualBPSStatsRepo) GetAccountBPSWindowStats(context.Context, int64, time.Time) (*usagestats.AccountStats, error) {
	r.bpsCalls++
	return &usagestats.AccountStats{Requests: 1, Tokens: 4, Cost: .05}, nil
}

func TestDualBPSUsageKeepsEndpointTotalsSeparateFromAccountWindows(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			a := dualBPSEvalFixture(t)
			a.Extra[ExcelBPSEnabledKey] = enabled
			a.Extra[ExcelBPSModelsKey] = []any{"different-model"}
			a.Extra["codex_5h_used_percent"] = 41.0
			a.Extra["codex_7d_used_percent"] = 17.0
			a.Extra["codex_usage_updated_at"] = time.Now().Format(time.RFC3339)
			stats := &dualBPSStatsRepo{}
			svc := &AccountUsageService{usageLogRepo: stats}
			usage, err := svc.getOpenAIUsage(context.Background(), a, false)
			require.NoError(t, err)
			require.NotEqual(t, "bps_local", usage.Source)
			require.Equal(t, "account_codex_snapshot", usage.QuotaSource)
			require.EqualValues(t, 1, usage.BPSLocalUsage.FiveHour.Requests)
			require.EqualValues(t, 1, usage.BPSLocalUsage.SevenDay.Requests)
			require.EqualValues(t, 3, usage.FiveHour.WindowStats.Requests)
			require.EqualValues(t, 3, usage.SevenDay.WindowStats.Requests)
			require.Equal(t, 41.0, usage.FiveHour.Utilization)
			require.Equal(t, 2, stats.bpsCalls)
			require.Equal(t, 2, stats.calls)
		})
	}
}

func TestDualBPSUsageNeverLabelsUnfilteredTotalsAsBPS(t *testing.T) {
	a := dualBPSEvalFixture(t)
	stats := &bpsLocalStatsRepo{}
	svc := &AccountUsageService{usageLogRepo: stats}
	usage, err := svc.getOpenAIUsage(context.Background(), a, false)
	require.NoError(t, err)
	require.Nil(t, usage.BPSLocalUsage)
	require.Nil(t, usage.FiveHour)
	require.Zero(t, stats.calls)
}

func TestDualBPSQuotaUsesNativeReadOnlyGrantAndPreservesFailedSnapshot(t *testing.T) {
	for _, status := range []int{200, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			a := dualBPSEvalFixture(t)
			sampled := time.Now().Add(-time.Hour).Truncate(time.Second)
			a.Extra["codex_5h_used_percent"] = 41.0
			a.Extra["codex_usage_updated_at"] = sampled.Format(time.RFC3339)
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{a.ID: a}}
			tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(a): a.GetOpenAIAccessToken()}}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, http.MethodGet, r.Method, "quota refresh must never generate inference")
				require.Equal(t, "Bearer native-test-access", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != "/backend-api/wham/usage" {
					require.Equal(t, "/backend-api/wham/rate-limit-reset-credits", r.URL.Path)
					_, _ = w.Write([]byte(`{"credits":[]}`))
					return
				}
				w.WriteHeader(status)
				if status == 200 {
					_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":27,"limit_window_seconds":18000,"reset_after_seconds":300}}}`))
				} else {
					_, _ = w.Write([]byte(`{"error":"denied"}`))
				}
			}))
			defer server.Close()
			quota := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), newQuotaRedirectingFactory(server), nil)
			svc := &AccountUsageService{cache: NewUsageCache(), usageLogRepo: &dualBPSStatsRepo{}, openAIQuotaService: quota}
			usage, err := svc.getOpenAIUsage(context.Background(), a, true)
			require.NoError(t, err)
			require.EqualValues(t, 1, usage.BPSLocalUsage.FiveHour.Requests)
			require.EqualValues(t, 3, usage.FiveHour.WindowStats.Requests)
			if status == 200 {
				require.Equal(t, "chatgpt_wham_usage", usage.QuotaSource)
				require.Equal(t, 27.0, usage.FiveHour.Utilization)
				require.Empty(t, usage.QuotaRefreshError)
				require.Equal(t, 2, calls)
			} else {
				require.Equal(t, "account_codex_snapshot", usage.QuotaSource)
				require.Equal(t, 41.0, usage.FiveHour.Utilization)
				require.True(t, sampled.Equal(*usage.QuotaUpdatedAt))
				require.Contains(t, usage.QuotaRefreshError, "403")
				require.Equal(t, 1, calls)
			}
		})
	}
}
