//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExcelBPSPreviousResponseSchedulerAdmission(t *testing.T) {
	for _, advanced := range []string{"true", "false"} {
		for _, continuation := range []bool{false, true} {
			for _, nativeAvailable := range []bool{false, true} {
				t.Run(fmt.Sprintf("advanced=%s/continuation=%t/native=%t", advanced, continuation, nativeAvailable), func(t *testing.T) {
					t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
					bps := *excelBPSAccount(t)
					bps.Status, bps.Schedulable = StatusActive, true
					bps.Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "gpt-6-astra"}
					accounts := []Account{bps}
					native := Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
						Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 100}
					if nativeAvailable {
						accounts = append(accounts, native)
					}
					var acquired, released []int64
					svc := &OpenAIGatewayService{
						accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: &schedulerTestGatewayCache{},
						cfg: &config.Config{}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced),
						concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired, releasedIDs: &released}),
					}
					previousID := ""
					if continuation {
						previousID = "resp_bps_admission"
					}
					groupID := int64(10131)
					selection, _, err := svc.SelectAccountWithSchedulerForCapability(t.Context(), &groupID, previousID, "", "gpt-6-astra", nil,
						OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponsesCompatible, false, true, true)
					if continuation && !nativeAvailable {
						require.Error(t, err)
						require.Nil(t, selection)
						require.Empty(t, acquired)
						return
					}
					require.NoError(t, err)
					require.NotNil(t, selection)
					if continuation {
						require.Equal(t, native.ID, selection.Account.ID)
						require.NotContains(t, acquired, bps.ID, "BPS must be vetoed before taking a slot")
					} else if !nativeAvailable {
						require.Equal(t, bps.ID, selection.Account.ID)
					} else {
						require.Contains(t, []int64{bps.ID, native.ID}, selection.Account.ID)
					}
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
				})
			}
		}
	}
	bps := excelBPSAccount(t)
	ctx := context.WithValue(t.Context(), openAIPreviousResponseAdmissionKey{}, true)
	require.Equal(t, "bps_requires_full_history", openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ctx, bps, PlatformOpenAI, "gpt-6-astra", false, OpenAIEndpointCapabilityResponsesCompatible))
	ok, reason := (&defaultOpenAIAccountScheduler{}).isAccountRequestCompatibleReason(t.Context(), bps, OpenAIAccountScheduleRequest{PreviousResponseID: "resp_bps"})
	require.False(t, ok)
	require.Equal(t, "bps_requires_full_history", reason)
}

func TestExcelBPSCompactionPreparationPreservesSupportedControls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, body := range map[string]string{
		"context management": `{"model":"gpt-6-astra","input":"hello","context_management":[{"type":"compaction","compact_threshold":1000}]}`,
		"compaction trigger": `{"model":"gpt-6-astra","input":[{"role":"user","content":"hello"},{"type":"compaction_trigger"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			a := excelBPSAccount(t)
			assertPrepared := func(req *http.Request, bridge any, err error) {
				t.Helper()
				require.NoError(t, err)
				require.NotNil(t, req)
				require.NotNil(t, bridge)
				wire, readErr := io.ReadAll(req.Body)
				require.NoError(t, readErr)
				_ = req.Body.Close()
				require.Contains(t, string(wire), "hello", "preparation must retain the original input")
				if name == "context management" {
					require.Equal(t, "compaction", gjson.GetBytes(wire, "context_management.0.type").String())
					require.EqualValues(t, 1000, gjson.GetBytes(wire, "context_management.0.compact_threshold").Int())
				} else {
					require.Contains(t, string(wire), "compaction_trigger", "the real upstream must receive the trigger")
				}
			}
			for _, cache := range []bool{false, true} {
				req, bridge, err := prepareExcelBPSRequest(t.Context(), []byte(body), a.GetOpenAIAccessToken(), a, "compaction-test", cache)
				assertPrepared(req, bridge, err)
			}
			testReq := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(body))
			testReq.Header.Set("Authorization", "Bearer "+a.GetOpenAIAccessToken())
			req, bridge, err := convertExcelBPSTestRequest(testReq, a)
			assertPrepared(req, bridge, err)
		})
	}
}

func TestExcelBPSEvalDirectOAuthEligibilityProjection(t *testing.T) {
	bps := excelBPSAccount(t)
	native := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{
		{AccountID: bps.ID, RequestedModel: "gpt-6-astra", DirectOAuthEligible: true},
		{AccountID: native.ID, RequestedModel: "gpt-6-astra", DirectOAuthEligible: false},
	}}}
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{bps.ID: bps, native.ID: native}}}
	svc := NewOpenAIEvalService(repo, accounts, &AccountTestService{})
	got, err := svc.GetConfig(t.Context())
	require.NoError(t, err)
	require.Len(t, got.Accounts, 2)
	require.False(t, got.Accounts[0].DirectOAuthEligible)
	require.True(t, got.Accounts[1].DirectOAuthEligible)
	require.True(t, repo.config.Accounts[0].DirectOAuthEligible, "read-time projection must not mutate persisted config")
	require.False(t, repo.config.Accounts[1].DirectOAuthEligible)
}

func TestExcelBPSExplicitModelSchedulerAdmission(t *testing.T) {
	for _, advanced := range []string{"true", "false"} {
		for _, tc := range []struct {
			name        string
			mapping     map[string]any
			allowlist   GroupModelAllowlist
			publicModel string
			clientModel string
			want        bool
		}{
			{name: "empty mapping and no allowlist"},
			{name: "disabled concrete allowlist", allowlist: GroupModelAllowlist{Models: []string{"gpt-6-astra"}}},
			{name: "group wildcard is not concrete", allowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-*"}}},
			{name: "different concrete selection", allowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-other"}}},
			{name: "concrete group-only selection", allowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-astra"}}, want: true},
			{name: "composite public selection", allowlist: GroupModelAllowlist{Enabled: true, Models: []string{"public-astra"}}, publicModel: "public-astra", want: true},
			{name: "client model before rewrite", allowlist: GroupModelAllowlist{Enabled: true, Models: []string{"public-astra"}}, clientModel: "public-astra", want: true},
			{name: "explicit account mapping", mapping: map[string]any{"gpt-6-astra": "upstream-model"}, want: true},
			{name: "account wildcard preserved", mapping: map[string]any{"gpt-*": "upstream-model"}, want: true},
		} {
			t.Run(advanced+"/"+tc.name, func(t *testing.T) {
				t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
				bps := *excelBPSAccount(t)
				bps.Status, bps.Schedulable = StatusActive, true
				if tc.mapping != nil {
					bps.Credentials["model_mapping"] = tc.mapping
				}
				groupID := int64(10132)
				ctx := context.WithValue(t.Context(), ctxkey.Group, &Group{ID: groupID, Platform: PlatformOpenAI, ModelAllowlist: tc.allowlist})
				if tc.publicModel != "" {
					ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, tc.publicModel)
				}
				if tc.clientModel != "" {
					ctx = WithOpenAIClientRequestedModel(ctx, tc.clientModel)
				}
				var acquired []int64
				svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{bps}},
					cache: &schedulerTestGatewayCache{}, cfg: &config.Config{}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced),
					concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired})}
				selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", "gpt-6-astra", nil,
					OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponsesCompatible, false, false, true)
				if tc.want {
					require.NoError(t, err)
					require.NotNil(t, selection)
					require.Equal(t, bps.ID, selection.Account.ID)
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
				} else {
					require.Error(t, err)
					require.Nil(t, selection)
					require.Empty(t, acquired)
					require.Equal(t, "bps_models_not_configured", openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ctx, &bps, PlatformOpenAI, "gpt-6-astra", false, OpenAIEndpointCapabilityResponsesCompatible))
					ok, reason := (&defaultOpenAIAccountScheduler{}).isAccountRequestCompatibleReason(ctx, &bps, OpenAIAccountScheduleRequest{RequestedModel: "gpt-6-astra"})
					require.False(t, ok)
					require.Equal(t, "bps_models_not_configured", reason)
				}
			})
		}
	}
}
