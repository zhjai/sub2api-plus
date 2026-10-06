package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
	"github.com/stretchr/testify/require"
)

func prismDispatchFixture(t *testing.T) Account {
	t.Helper()
	a := Account{ID: 9841, Platform: PlatformPrism, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 2, Credentials: map[string]any{"access_token": "synthetic-prism-dispatch", "prism_verified_identity": "synthetic-identity"}}
	key := prismAccountFingerprint(&a)
	prismCatalogCache.Lock()
	prismCatalogCache.entries[key] = prismCatalogEntry{models: []prism.UpstreamModel{{ID: "real-model", Label: "Real model", Efforts: []string{"medium", "high"}, DefaultEffort: "medium"}}, expires: time.Now().Add(time.Hour)}
	prismCatalogCache.Unlock()
	t.Cleanup(func() { InvalidatePrismAccountCatalog(a.ID) })
	return a
}

func TestPrismDispatchNativeHTTPContinuationOwner(t *testing.T) {
	ctx := context.Background()
	a := prismDispatchFixture(t)
	group := int64(23)
	cache := &stubGatewayCache{}
	store := NewOpenAIWSStateStore(cache)
	svc := &OpenAIGatewayService{accountRepo: stubOpenAIAccountRepo{accounts: []Account{a}}, cache: cache, cfg: newOpenAIWSV2TestConfig(), concurrencyService: NewConcurrencyService(stubConcurrencyCache{}), openaiWSStateStore: store}
	require.NoError(t, store.BindResponseAccount(ctx, group, "resp_prism_owner", a.ID, time.Hour))
	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &group, "resp_prism_owner", "real-model", nil, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, a.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	selection, err = svc.SelectAccountByPreviousResponseID(ctx, &group, "resp_prism_owner", "real-model", map[int64]struct{}{a.ID: {}}, false)
	require.NoError(t, err)
	require.Nil(t, selection)
}

func TestPrismDispatchModelAndEffortHardQualification(t *testing.T) {
	a := prismDispatchFixture(t)
	ctx := WithRequestedReasoningEffort(context.Background(), "high")
	require.Empty(t, openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ctx, &a, PlatformPrism, "real-model", false, ""))
	ctx = WithRequestedReasoningEffort(context.Background(), "low")
	require.Equal(t, "prism_model_or_effort_unavailable", openAICompatibleAccountEligibilityFailureReasonBeforeProfit(ctx, &a, PlatformPrism, "real-model", false, ""))
	require.False(t, a.IsModelSupported("invented"))
	require.False(t, a.AllowsOpenAICompact())
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings))
	require.True(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	a.Credentials["model_mapping"] = map[string]any{"alias": "real-model"}
	// A new credential/mapping generation must be revalidated, never reuse the old directory.
	require.False(t, a.IsModelSupported("alias"))
}

func TestPrismDispatchRPMBlocksNativeUpstreamBeforeWrite(t *testing.T) {
	a := prismDispatchFixture(t)
	a.Extra = map[string]any{"rpm_limit": 1}
	cache := &hardRPMCache{used: 1}
	svc := &OpenAIGatewayService{cache: cache}
	body := `{"model":"real-model","input":"hello","store":false}`
	c, writer := prismTestContext(body)
	result, err := svc.forwardPrismResponses(context.Background(), c, &a, []byte(body))
	require.Nil(t, result)
	require.True(t, IsAccountRPMError(err))
	require.False(t, c.Writer.Written())
	require.Empty(t, writer.Body.String())
	require.Equal(t, 1, len(cache.ids))
}
