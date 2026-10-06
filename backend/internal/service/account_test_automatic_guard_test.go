//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAccountTestBackgroundDisabledSkipsSend(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Schedulable: false,
		Credentials: map[string]any{"api_key": "fixture", "base_url": "https://example.invalid"}}
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{42: account}}}
	upstream := &automaticGuardUpstream{queuedHTTPUpstream: queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, `{"output":[]}`)}}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}
	result, err := svc.RunTestBackground(context.Background(), 42, "gpt-6.1-sol")
	require.NoError(t, err)
	require.NotEqual(t, "success", result.Status)
	require.Contains(t, result.ErrorMessage, "scheduling is disabled")
	require.Empty(t, upstream.requests)
}

func TestAccountTestBackgroundDisableBeforeSend(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Schedulable: true,
		Credentials: map[string]any{"api_key": "fixture", "base_url": "https://example.invalid"}}
	repo := &automaticAccountTestRepo{account: account, disableAt: 3}
	upstream := &automaticGuardUpstream{queuedHTTPUpstream: queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, `{"output":[]}`)}}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}
	result, err := svc.RunTestBackground(context.Background(), 42, "gpt-6.1-sol")
	require.NoError(t, err)
	require.NotEqual(t, "success", result.Status)
	require.Contains(t, result.ErrorMessage, "scheduling is disabled")
	require.Empty(t, upstream.requests)
}

type automaticAccountTestRepo struct {
	AccountRepository
	account   *Account
	reads     int
	disableAt int
}

type automaticGuardUpstream struct{ queuedHTTPUpstream }

func (*automaticGuardUpstream) SupportsSingleSend() bool { return true }

func (r *automaticAccountTestRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	copy := *r.account
	if r.disableAt > 0 && r.reads >= r.disableAt {
		copy.Schedulable = false
	}
	return &copy, nil
}

func TestAccountTestManualDisabledStillAllowed(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Schedulable: false,
		Credentials: map[string]any{"api_key": "fixture", "base_url": "https://example.invalid"}}
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{42: account}}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, "data: {\"type\":\"response.completed\"}\n\n")}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}
	client, _ := newTestContext()
	require.NoError(t, svc.TestAccountConnection(client, 42, "gpt-6.1-sol", "", ""))
	require.Len(t, upstream.requests, 1)
}

func TestAccountTestAutomaticSendRefreshesParticipation(t *testing.T) {
	account := &Account{ID: 42, Schedulable: true, Concurrency: 1}
	for _, automatic := range []bool{true, false} {
		t.Run(map[bool]string{true: "automatic", false: "manual"}[automatic], func(t *testing.T) {
			repo := &automaticAccountTestRepo{account: account, disableAt: 2}
			upstream := &automaticGuardUpstream{queuedHTTPUpstream: queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(200, "ok")}}}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
			ctx := context.WithValue(context.Background(), openAIEvalAutomaticKey{}, automatic)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.invalid", nil)
			require.NoError(t, err)
			response, err := svc.doAccountTestUpstreamTLS(req, "", account, nil)
			if automatic {
				require.ErrorContains(t, err, "scheduling is disabled")
				require.Nil(t, response)
				require.Empty(t, upstream.requests)
			} else {
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Len(t, upstream.requests, 1)
			}
		})
	}
}

type automaticGuardPlanRepo struct {
	ScheduledTestPlanRepository
	updates int
	next    time.Time
}

func (r *automaticGuardPlanRepo) UpdateAfterRun(_ context.Context, _ int64, _ time.Time, next time.Time) error {
	r.updates++
	r.next = next
	return nil
}

func TestScheduledAccountTestDisabledAdvancesPlanWithoutRecovery(t *testing.T) {
	account := &Account{ID: 42, Schedulable: false, Status: StatusError}
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{42: account}}}
	plans := &automaticGuardPlanRepo{}
	runner := &ScheduledTestRunnerService{planRepo: plans, accountTestSvc: &AccountTestService{accountRepo: repo}, rateLimitSvc: &RateLimitService{accountRepo: repo}}
	// No result service/transport is supplied: a disabled task must not use it.
	runner.runOnePlan(context.Background(), &ScheduledTestPlan{ID: 1, AccountID: 42, CronExpression: "* * * * *", AutoRecover: true})
	require.Equal(t, 1, plans.updates)
	require.True(t, plans.next.After(time.Now()))
	require.Zero(t, repo.clearedErrorID)
	ctx := context.WithValue(context.Background(), openAIEvalAutomaticKey{}, true)
	result, err := runner.rateLimitSvc.RecoverAccountState(ctx, 42, AccountRecoveryOptions{})
	require.NoError(t, err)
	require.False(t, result.ClearedError)
	require.Zero(t, repo.clearedErrorID)
}
