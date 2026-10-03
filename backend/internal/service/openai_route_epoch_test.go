//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type openAIOpaqueRouteEpochTestCache struct {
	*stubGatewayCache
	remote        OpenAIOpaqueRouteEpochState
	getCalls      int
	convergeCalls int
}

type openAIOpaqueRouteEpochLookupErrorCache struct {
	*stubGatewayCache
	err error
}

func (c *openAIOpaqueRouteEpochLookupErrorCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, c.err
}

func (c *openAIOpaqueRouteEpochTestCache) GetOpenAIOpaqueRouteEpoch(context.Context, int64, string, string, string, int64, time.Duration) (OpenAIOpaqueRouteEpochState, error) {
	c.getCalls++
	return c.remote, nil
}

func (c *openAIOpaqueRouteEpochTestCache) BumpOpenAIOpaqueRouteEpoch(_ context.Context, _ int64, _, _, _ string, _ int64, expectedEpoch int64, _ int, _, _ time.Duration) (OpenAIOpaqueRouteEpochState, bool, error) {
	if c.remote.Epoch != expectedEpoch {
		return c.remote, c.remote.Epoch > expectedEpoch, nil
	}
	c.remote.Epoch++
	c.remote.Bumps++
	return c.remote, true, nil
}

func (c *openAIOpaqueRouteEpochTestCache) ConvergeOpenAIOpaqueRouteEpoch(_ context.Context, _ int64, _, _, _ string, _ int64, floor OpenAIOpaqueRouteEpochState, _ time.Duration) (OpenAIOpaqueRouteEpochState, error) {
	c.convergeCalls++
	if c.remote.Epoch < floor.Epoch {
		c.remote = floor
	}
	return c.remote, nil
}

func newOpenAIOpaqueRouteEpochTestAccount() *Account {
	return &Account{
		ID:          7101,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Extra:       map[string]any{"openai_opaque_upstream": true},
	}
}

func newOpenAIOpaqueRouteEpochTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(nil)
	c.Request, _ = http.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func TestOpenAIOpaqueRouteEpochZeroIsNoOp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	account := newOpenAIOpaqueRouteEpochTestAccount()
	c := newOpenAIOpaqueRouteEpochTestContext()

	state, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(context.Background(), c, 1, "session-a", "gpt-6-astra", "high", account, "")
	require.NoError(t, err)
	require.Zero(t, state.Epoch)

	body := []byte(`{"model":"gpt-6-astra","prompt_cache_key":"cache-a"}`)
	got, changed, err := applyOpenAIOpaqueRouteEpochBody(c, account, body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)

	headers := make(http.Header)
	headers.Set("session_id", "session-a")
	require.False(t, applyOpenAIOpaqueRouteEpochHeaders(c, account, headers, true))
	require.Equal(t, "session-a", headers.Get("session_id"))
}

func TestOpenAIOpaqueRouteEpochZeroResponseBindingSurvivesRotation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	account := newOpenAIOpaqueRouteEpochTestAccount()
	ctx := context.Background()
	groupID := int64(1)
	created := newOpenAIOpaqueRouteEpochTestContext()
	created.Set("api_key", &APIKey{GroupID: &groupID})
	_, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, created, groupID, "session-a", "gpt-6-astra", "high", account, "")
	require.NoError(t, err)
	svc.bindOpenAIResponseRouteEpoch(ctx, created, account, "resp_epoch_0")
	epoch, found, err := svc.getOpenAIWSStateStore().GetResponseRouteEpoch(ctx, groupID, "resp_epoch_0", account.ID)
	require.NoError(t, err)
	require.True(t, found, "the original route identity must also be bound")
	require.Zero(t, epoch)
	_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(ctx, groupID, "session-a", "gpt-6-astra", "high", account.ID, 0)
	require.NoError(t, err)
	require.True(t, advanced)
	continued := newOpenAIOpaqueRouteEpochTestContext()
	state, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, continued, groupID, "session-a", "gpt-6-astra", "high", account, "resp_epoch_0")
	require.NoError(t, err)
	require.EqualValues(t, 1, state.Epoch)
	body := []byte(`{"prompt_cache_key":"cache-a","previous_response_id":"resp_epoch_0"}`)
	got, changed, err := applyOpenAIOpaqueRouteEpochBody(continued, account, body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
}

func TestOpenAIOpaqueRouteEpochRekeysBodyAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	account := newOpenAIOpaqueRouteEpochTestAccount()
	c := newOpenAIOpaqueRouteEpochTestContext()

	state, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(context.Background(), 1, "session-a", "gpt-6-astra", "high", account.ID, 0)
	require.NoError(t, err)
	require.True(t, advanced)
	require.EqualValues(t, 1, state.Epoch)

	_, err = svc.PrepareOpenAIOpaqueRouteEpochAttempt(context.Background(), c, 1, "session-a", "gpt-6-astra", "high", account, "")
	require.NoError(t, err)
	body := []byte(`{"model":"gpt-6-astra","prompt_cache_key":"cache-a"}`)
	got, changed, err := applyOpenAIOpaqueRouteEpochBody(c, account, body)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotEqual(t, "cache-a", gjson.GetBytes(got, "prompt_cache_key").String())

	headers := make(http.Header)
	headers.Set("session_id", "session-a")
	headers.Set("conversation_id", "conversation-a")
	require.True(t, applyOpenAIOpaqueRouteEpochHeaders(c, account, headers, true))
	require.NotEqual(t, "session-a", headers.Get("session_id"))
	require.NotEqual(t, "conversation-a", headers.Get("conversation_id"))
	require.Equal(t, 25, len(headers.Get("session_id")))
}

func TestOpenAIOpaqueRouteEpochContinuationDefersRekey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	account := newOpenAIOpaqueRouteEpochTestAccount()
	c := newOpenAIOpaqueRouteEpochTestContext()

	_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(context.Background(), 1, "session-a", "gpt-6-astra", "high", account.ID, 0)
	require.NoError(t, err)
	require.True(t, advanced)
	_, err = svc.PrepareOpenAIOpaqueRouteEpochAttempt(context.Background(), c, 1, "session-a", "gpt-6-astra", "high", account, "resp_a")
	require.ErrorIs(t, err, ErrOpenAIOpaqueRouteEpochBindingUnavailable)

	body := []byte(`{"model":"gpt-6-astra","prompt_cache_key":"cache-a","previous_response_id":"resp_a"}`)
	got, changed, err := applyOpenAIOpaqueRouteEpochBody(c, account, body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
	require.Empty(t, openAIOpaqueRouteEpochAffinity(c, account))
}

func TestOpenAIOpaqueRouteEpochContinuationPropagatesBindingLookupError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lookupErr := errors.New("state store unavailable")
	svc := &OpenAIGatewayService{cache: &openAIOpaqueRouteEpochLookupErrorCache{
		stubGatewayCache: &stubGatewayCache{},
		err:              lookupErr,
	}}
	account := newOpenAIOpaqueRouteEpochTestAccount()
	c := newOpenAIOpaqueRouteEpochTestContext()

	_, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(context.Background(), c, 1, "session-a", "gpt-6-astra", "high", account, "resp_a")
	require.ErrorIs(t, err, ErrOpenAIOpaqueRouteEpochBindingLookup)
	require.ErrorIs(t, err, lookupErr)
}

func TestOpenAIOpaqueRouteEpochContinuationKeepsCreatingEpochAfterRotation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	account := newOpenAIOpaqueRouteEpochTestAccount()
	ctx := context.Background()
	created := newOpenAIOpaqueRouteEpochTestContext()

	_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(ctx, 1, "session-a", "gpt-6-astra", "high", account.ID, 0)
	require.NoError(t, err)
	require.True(t, advanced)
	_, err = svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, created, 1, "session-a", "gpt-6-astra", "high", account, "")
	require.NoError(t, err)
	require.NoError(t, svc.getOpenAIWSStateStore().BindResponseRouteEpoch(ctx, 1, "resp_epoch_1", account.ID, 1, time.Hour))

	_, advanced, err = svc.BumpOpenAIOpaqueRouteEpoch(ctx, 1, "session-a", "gpt-6-astra", "high", account.ID, 1)
	require.NoError(t, err)
	require.True(t, advanced)
	continued := newOpenAIOpaqueRouteEpochTestContext()
	state, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, continued, 1, "session-a", "gpt-6-astra", "high", account, "resp_epoch_1")
	require.NoError(t, err)
	require.EqualValues(t, 2, state.Epoch, "route state should remain current for future new responses")

	body := []byte(`{"prompt_cache_key":"cache-a","previous_response_id":"resp_epoch_1"}`)
	got, changed, err := applyOpenAIOpaqueRouteEpochBody(continued, account, body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, openAIOpaqueRouteEpochValue(account.ID, 1, "prompt-cache", "cache-a"), gjson.GetBytes(got, "prompt_cache_key").String())
	require.NotEmpty(t, openAIOpaqueRouteEpochAffinity(continued, account))

	otherAccount := newOpenAIOpaqueRouteEpochTestAccount()
	otherAccount.ID++
	other := newOpenAIOpaqueRouteEpochTestContext()
	_, err = svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, other, 1, "session-a", "gpt-6-astra", "high", otherAccount, "resp_epoch_1")
	require.ErrorIs(t, err, ErrOpenAIOpaqueRouteEpochBindingUnavailable)
	got, changed, err = applyOpenAIOpaqueRouteEpochBody(other, otherAccount, body)
	require.NoError(t, err)
	require.False(t, changed, "a response epoch binding must not cross accounts")
	require.Equal(t, body, got)
}

func TestOpenAIOpaqueRouteEpochRedisMissDoesNotCacheZero(t *testing.T) {
	cache := &openAIOpaqueRouteEpochTestCache{stubGatewayCache: &stubGatewayCache{}}
	svc := &OpenAIGatewayService{cache: cache}

	require.Zero(t, svc.getOpenAIOpaqueRouteEpoch(context.Background(), 1, "session-a", "gpt-6-astra", "high", 7101).Epoch)
	require.Empty(t, svc.openaiOpaqueRouteEpochs)
	require.Zero(t, svc.getOpenAIOpaqueRouteEpoch(context.Background(), 1, "session-a", "gpt-6-astra", "high", 7101).Epoch)
	require.Equal(t, 2, cache.getCalls, "a Redis miss must not create an epoch-zero local cache hit")
}

func TestOpenAIOpaqueRouteEpochLocalCacheCleanupIsBoundedAndExpires(t *testing.T) {
	now := time.Now()
	entries := map[string]openAIOpaqueRouteEpochLocalState{
		"expired": {OpenAIOpaqueRouteEpochState: OpenAIOpaqueRouteEpochState{Epoch: 1}, Until: now.Add(-time.Second)},
		"live-a":  {OpenAIOpaqueRouteEpochState: OpenAIOpaqueRouteEpochState{Epoch: 1}, Until: now.Add(time.Hour)},
		"live-b":  {OpenAIOpaqueRouteEpochState: OpenAIOpaqueRouteEpochState{Epoch: 2}, Until: now.Add(time.Hour)},
	}
	cleanupOpenAIOpaqueRouteEpochs(entries, now, 2, "incoming")
	require.NotContains(t, entries, "expired")
	require.Less(t, len(entries), 2, "cleanup leaves room for the incoming entry")
}

func TestOpenAIOpaqueRouteEpochRedisRecoveryConvergesWithoutRollback(t *testing.T) {
	cache := &openAIOpaqueRouteEpochTestCache{stubGatewayCache: &stubGatewayCache{}}
	svc := &OpenAIGatewayService{cache: cache}
	now := time.Now()
	key := openAIOpaqueRouteEpochKey(1, "session-a", "gpt-6-astra", "high", 7101)
	local := OpenAIOpaqueRouteEpochState{Epoch: 2, Bumps: 2, WindowStartedUnix: now.Unix()}
	svc.storeLocalOpenAIOpaqueRouteEpoch(key, local, now)

	got := svc.getOpenAIOpaqueRouteEpoch(context.Background(), 1, "session-a", "gpt-6-astra", "high", 7101)
	require.Equal(t, local, got)
	require.Equal(t, local, cache.remote)
	require.Equal(t, 1, cache.convergeCalls)

	cache.remote = OpenAIOpaqueRouteEpochState{Epoch: 3, Bumps: 3, WindowStartedUnix: now.Unix()}
	got = svc.getOpenAIOpaqueRouteEpoch(context.Background(), 1, "session-a", "gpt-6-astra", "high", 7101)
	require.EqualValues(t, 3, got.Epoch, "a newer shared epoch must win")
}

func TestOpenAIOpaqueRouteEpochAffinityChangesWithEpoch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	account := newOpenAIOpaqueRouteEpochTestAccount()
	c := newOpenAIOpaqueRouteEpochTestContext()

	_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(context.Background(), 1, "session-a", "gpt-6-astra", "high", account.ID, 0)
	require.NoError(t, err)
	require.True(t, advanced)
	_, err = svc.PrepareOpenAIOpaqueRouteEpochAttempt(context.Background(), c, 1, "session-a", "gpt-6-astra", "high", account, "")
	require.NoError(t, err)
	first := openAIOpaqueRouteEpochAffinity(c, account)
	require.NotEmpty(t, first)

	_, advanced, err = svc.BumpOpenAIOpaqueRouteEpoch(context.Background(), 1, "session-a", "gpt-6-astra", "high", account.ID, 1)
	require.NoError(t, err)
	require.True(t, advanced)
	_, err = svc.PrepareOpenAIOpaqueRouteEpochAttempt(context.Background(), c, 1, "session-a", "gpt-6-astra", "high", account, "")
	require.NoError(t, err)
	second := openAIOpaqueRouteEpochAffinity(c, account)
	require.NotEmpty(t, second)
	require.NotEqual(t, first, second)
}

func TestOpenAIOpaqueRouteEpochBumpCap(t *testing.T) {
	svc := &OpenAIGatewayService{}
	var epoch int64
	for i := 0; i < openAIOpaqueRouteEpochMaxBumps; i++ {
		state, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(context.Background(), 1, "session-a", "gpt-6-astra", "high", 7101, epoch)
		require.NoError(t, err)
		require.True(t, advanced)
		epoch = state.Epoch
	}
	state, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(context.Background(), 1, "session-a", "gpt-6-astra", "high", 7101, epoch)
	require.NoError(t, err)
	require.False(t, advanced)
	require.EqualValues(t, openAIOpaqueRouteEpochMaxBumps, state.Bumps)
	require.Equal(t, epoch, state.Epoch)
}

func TestOpenAIOpaqueRouteEpochIgnoresOrdinaryAndOAuthAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, account := range []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{}},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_opaque_upstream": true}},
	} {
		c := newOpenAIOpaqueRouteEpochTestContext()
		c.Set(openAIOpaqueRouteEpochContextKey, openAIOpaqueRouteEpochAttempt{AccountID: account.ID, Epoch: 2, Apply: true})
		body := []byte(`{"prompt_cache_key":"cache-a"}`)
		got, changed, err := applyOpenAIOpaqueRouteEpochBody(c, account, body)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, got)
	}
}
