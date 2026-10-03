//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type r7OwnerAccountRepo struct {
	AccountRepository
	err error
}

// Owner-lookup failures here apply to previous_response_id validation, not
// the current selected account's independent RPM configuration read.
func (r *r7OwnerAccountRepo) GetAccountRPMLimit(context.Context, int64) (int, error) {
	return 0, nil
}

func (r *r7OwnerAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return nil, r.err
}

func TestR7DeletedResponseOwnerIsBoundAbsenceNotOutage(t *testing.T) {
	for _, snapshot := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			err  error
		}{
			{name: "deleted", err: ErrAccountNotFound},
			{name: "wrapped_soft_deleted", err: fmt.Errorf("lookup: %w", ErrAccountNotFound)},
			{name: "nil_owner"},
			{name: "database_outage", err: errors.New("database unavailable")},
		} {
			t.Run(fmt.Sprintf("%s/snapshot_%v", tc.name, snapshot), func(t *testing.T) {
				ctx := t.Context()
				store := NewOpenAIWSStateStore(&stubGatewayCache{})
				require.NoError(t, store.BindResponseAccount(ctx, 23, "resp_deleted", 91, time.Hour))
				repo := &r7OwnerAccountRepo{err: tc.err}
				svc := &OpenAIGatewayService{openaiWSStateStore: store, accountRepo: repo}
				if snapshot {
					svc.accountRepo = nil
					svc.schedulerSnapshot = NewSchedulerSnapshotService(nil, nil, repo, nil, nil)
				}
				groupID := int64(23)
				owner, bound, err := svc.ResolveOpenAIPreviousResponseOwner(ctx, &groupID, "resp_deleted")
				require.Nil(t, owner)
				require.True(t, bound)
				if tc.name == "database_outage" {
					require.ErrorIs(t, err, tc.err)
				} else {
					require.NoError(t, err)
				}
				ownerID, err := store.GetResponseAccount(ctx, groupID, "resp_deleted")
				require.NoError(t, err)
				require.Equal(t, int64(91), ownerID, "unclassified ownership must not be deleted for reconstruction")
			})
		}
	}
}

func TestR7ForcedCodexLanguageAcrossBuilders(t *testing.T) {
	c, _ := newTestContext()
	c.Request.Header = http.Header{"User-Agent": {"generic-client"}, "accept-language": {"zh-CN"}}
	account := &Account{ID: 71, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-token", "base_url": "https://example.test/v1",
			credKeyHeaderOverrideEnabled: true,
			credKeyHeaderOverrides:       map[string]any{"aCcEpT-LaNgUaGe": "zh-TW", "User-Agent": "account-client"},
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.ForceCodexCLI = true
	svc := &OpenAIGatewayService{cfg: cfg}
	body := []byte(`{"model":"gpt-6-astra","stream":true}`)
	native, err := svc.buildUpstreamRequest(t.Context(), c, account, body, "test-token", false, "", true)
	require.NoError(t, err)
	requireR6EnglishLanguageWire(t, native.Header)
	passthrough, err := svc.buildUpstreamRequestOpenAIPassthrough(t.Context(), c, account, body, "test-token")
	require.NoError(t, err)
	requireR6EnglishLanguageWire(t, passthrough.Header)
	headers, _, err := svc.buildOpenAIWSHeaders(t.Context(), c, account, "test-token",
		OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, "", "", "", "", "")
	require.NoError(t, err)
	requireR6EnglishLanguageWire(t, headers)
	count, err := svc.buildInputTokensUpstreamRequest(t.Context(), c, account, body, "test-token")
	require.NoError(t, err)
	requireR6EnglishLanguageWire(t, count.Header)
}

func TestR7ReasoningEffortContextClearsPreviousTurn(t *testing.T) {
	ctx := WithRequestedReasoningEffort(t.Context(), "high")
	require.Equal(t, "high", *RequestedReasoningEffortFromContext(ctx))
	require.Nil(t, RequestedReasoningEffortFromContext(WithRequestedReasoningEffort(ctx, "")))
}

func TestR7CustomWeightValidationAndSaveIsolation(t *testing.T) {
	for _, rule := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			weights OpenAIEvalPolicyWeights
		}{
			{name: "overflow", weights: OpenAIEvalPolicyWeights{Cost: 1e308, Stability: 1e308}},
			{name: "zero"},
			{name: "negative", weights: OpenAIEvalPolicyWeights{Cost: -1}},
			{name: "nan", weights: OpenAIEvalPolicyWeights{Cost: math.NaN()}},
			{name: "infinity", weights: OpenAIEvalPolicyWeights{Cost: math.Inf(1)}},
		} {
			t.Run(fmt.Sprintf("%s/rule_%v", tc.name, rule), func(t *testing.T) {
				repo := &openAIEvalRepoFake{}
				svc := NewOpenAIEvalService(repo, nil, nil)
				cfg := &OpenAIEvalConfig{SchedulingPolicy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: tc.weights}
				if rule {
					cfg.SchedulingPolicy, cfg.CustomBalance = OpenAIEvalSchedulingPolicyStabilityFirst, OpenAIEvalPolicyWeights{}
					cfg.Policies = []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "gpt-6-astra", ReasoningEffort: "high", Policy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: &tc.weights}}
				}
				require.Error(t, svc.SaveConfig(t.Context(), cfg, 9))
				require.Nil(t, repo.config, "invalid weights must not reach persistence")
			})
		}
	}
	t.Cleanup(func() { SetOpenAIEvalEffectsEnabled(false); SetOpenAIEvalSchedulingPolicySnapshot(nil) })
	repo := &openAIEvalRepoFake{}
	svc := NewOpenAIEvalService(repo, nil, nil)
	cfg := &OpenAIEvalConfig{
		EffectsEnabled: true, SchedulingPolicy: OpenAIEvalSchedulingPolicyCustomBalance,
		CustomBalance: OpenAIEvalPolicyWeights{Cost: 3, Stability: 1},
		Policies:      []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "gpt-6-astra", ReasoningEffort: "high", Policy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: &OpenAIEvalPolicyWeights{Cost: 1, Stability: 3}}},
	}
	require.NoError(t, svc.SaveConfig(t.Context(), cfg, 9))
	reloaded, err := svc.GetConfig(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0.75, reloaded.CustomBalance.Cost)
	require.Equal(t, 0.25, reloaded.Policies[0].CustomBalance.Cost)
	low, ok := OpenAIEvalCustomBalanceForRequest("gpt-6-astra", "low")
	require.True(t, ok)
	high, ok := OpenAIEvalCustomBalanceForRequest("gpt-6-astra", "high")
	require.True(t, ok)
	require.Equal(t, reloaded.CustomBalance, low)
	require.Equal(t, *reloaded.Policies[0].CustomBalance, high)
}

func TestR7CustomPolicyRankingUsesModelEffortWeights(t *testing.T) {
	t.Cleanup(func() { SetOpenAIEvalEffectsEnabled(false); SetOpenAIEvalSchedulingPolicySnapshot(nil) })
	SetOpenAIEvalEffectsEnabled(true)
	SetOpenAIEvalSchedulingPolicySnapshot(&OpenAIEvalConfig{
		EffectsEnabled:   true,
		SchedulingPolicy: OpenAIEvalSchedulingPolicyCustomBalance,
		CustomBalance:    OpenAIEvalPolicyWeights{Cost: 1},
		Policies:         []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "gpt-6-astra", ReasoningEffort: "high", Policy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: &OpenAIEvalPolicyWeights{Stability: 1}}},
	})
	now := time.Now()
	cheap := upstreamCostTestAccount(701, UpstreamBillingProbeStatusOK, 0.06, now, time.Hour)
	stable := upstreamCostTestAccount(702, UpstreamBillingProbeStatusOK, 0.12, now, time.Hour)
	stats := newOpenAIAccountRuntimeStats()
	slow, fast := 20000, 100
	for _, effort := range []string{"high", "low"} {
		for i := 0; i < 5; i++ {
			stats.reportForRequest(cheap.ID, "gpt-6-astra", effort, false, &slow)
			stats.reportForRequest(stable.ID, "gpt-6-astra", effort, true, &fast)
		}
	}
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{cfg: newSchedulerTestOpenAIWSV2Config()}, stats: stats}
	loads := map[int64]*AccountLoadInfo{cheap.ID: {AccountID: cheap.ID}, stable.ID: {AccountID: stable.ID}}
	for _, tc := range []struct {
		effort    string
		preferred int64
	}{{"low", cheap.ID}, {"high", stable.ID}} {
		plan := scheduler.buildOpenAIAccountLoadPlan(t.Context(), OpenAIAccountScheduleRequest{
			RequestedModel: "mapped-model", ClientRequestedModel: "gpt-6-astra", RequestedReasoningEffort: tc.effort, UseUpstreamTokenCost: true,
		}, []*Account{cheap, stable}, loads)
		scores := map[int64]float64{}
		for _, candidate := range plan.candidates {
			scores[candidate.account.ID] = candidate.score
		}
		other := cheap.ID
		if tc.preferred == cheap.ID {
			other = stable.ID
		}
		require.Greater(t, scores[tc.preferred], scores[other], tc.effort)
	}
}

type r7EpochWriteFailureCache struct {
	stubGatewayCache
}

func (c *r7EpochWriteFailureCache) SetSessionAccountID(ctx context.Context, groupID int64, key string, accountID int64, ttl time.Duration) error {
	if key == openAIWSResponseEpochCacheKey("resp_r7_epoch", 703) {
		return errors.New("epoch storage unavailable")
	}
	return c.stubGatewayCache.SetSessionAccountID(ctx, groupID, key, accountID, ttl)
}

func TestR7EpochWriteFailureDoesNotInventCrossInstanceIdentity(t *testing.T) {
	cache := &r7EpochWriteFailureCache{}
	first := NewOpenAIWSStateStore(cache)
	other := NewOpenAIWSStateStore(cache)
	require.Error(t, first.BindResponseRouteEpoch(t.Context(), 0, "resp_r7_epoch", 703, 2, time.Hour))
	epoch, found, err := first.GetResponseRouteEpoch(t.Context(), 0, "resp_r7_epoch", 703)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(2), epoch)
	_, found, err = other.GetResponseRouteEpoch(t.Context(), 0, "resp_r7_epoch", 703)
	require.NoError(t, err)
	require.False(t, found)
	account := &Account{ID: 703, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_opaque_upstream": true}}
	require.NoError(t, other.BindResponseAccount(t.Context(), 0, "resp_r7_epoch", account.ID, time.Hour))
	svc := &OpenAIGatewayService{openaiWSStateStore: other, accountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}}
	c, _ := gin.CreateTestContext(nil)
	_, err = svc.PrepareOpenAIOpaqueRouteEpochAttempt(t.Context(), c, 0, "session", "gpt-6-astra", "high", account, "resp_r7_epoch")
	require.ErrorIs(t, err, ErrOpenAIOpaqueRouteEpochBindingUnavailable)
}
