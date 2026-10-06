//go:build unit

package service

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalStateProbeBPSIsolation(t *testing.T) {
	newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"gpt-5.4"},{"slug":"gpt-6-astra"}]}`)
	for _, tc := range []struct {
		name          string
		source        string
		effort        string
		accountConfig bool
		wantModel     string
		wantUpdate    bool
	}{
		{name: "manual test target", source: "manual", accountConfig: true, wantModel: "gpt-5.4"},
		{name: "scheduled test target", source: "scheduled", accountConfig: true, wantModel: "gpt-5.4"},
		{name: "manual high effort target", source: "manual", effort: "high", accountConfig: true, wantModel: "gpt-5.4"},
		{name: "scheduled xhigh effort target", source: "scheduled", effort: "xhigh", accountConfig: true, wantModel: "gpt-5.4"},
		{name: "internal account BPS schedule", source: "scheduled", effort: OpenAIEvalBPSAccountEffort, accountConfig: true, wantModel: "gpt-6-astra", wantUpdate: true},
		{name: "legacy manual probe without account entry", source: "manual", wantModel: "gpt-5.4"},
		{name: "legacy scheduled probe without account entry", source: "scheduled", wantModel: "gpt-5.4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initialState := OpenAIBPSAccountState{DegradedStreak: 2, UpdatedAt: time.Now().UTC().Add(-time.Hour)}
			account := &Account{
				ID: 61, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1, Schedulable: true,
				Credentials: map[string]any{"access_token": "probe-token"},
				Extra:       map[string]any{OpenAIBPSAccountStateExtraKey(): initialState},
			}
			accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
			repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{
				BPSAutoEnabled: true,
				// Even a retained legacy route cannot bypass account-schedule ownership.
				Accounts: []OpenAIEvalAccountConfig{{AccountID: account.ID, RequestedModel: "gpt-5.4", BPSMode: OpenAIEvalBPSModeAuto}},
			}}
			if tc.accountConfig {
				repo.config.BPSAccounts = []OpenAIEvalBPSAccountConfig{{
					AccountID: account.ID, ProbeModel: "gpt-6-astra", Mode: OpenAIEvalBPSModeAuto,
					FailureThreshold: 3, RecoveryThreshold: 2, IntervalSeconds: 24 * 60 * 60,
				}}
			}
			completed := "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"" + tc.wantModel + "\"}}\n\n"
			upstream := &queuedHTTPUpstream{responses: []*http.Response{
				stateProbeResponse("ticket-a", completed, http.StatusOK),
				stateProbeResponse("ticket-b", completed, http.StatusOK),
			}}
			accountTest := &AccountTestService{
				accountRepo: accounts, httpUpstream: upstream,
				openaiGatewayService: &OpenAIGatewayService{}, tlsFPProfileService: &TLSFingerprintProfileService{},
			}
			svc := NewOpenAIEvalService(repo, accounts, accountTest)
			request := OpenAIEvalRunRequest{AccountID: account.ID, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeStateProbe, ReasoningEffort: tc.effort}
			if tc.source == "scheduled" {
				repo.due = []OpenAIEvalScheduledRun{{
					AccountID: request.AccountID, RequestedModel: request.RequestedModel,
					TestType: request.TestType, ReasoningEffort: request.ReasoningEffort,
				}}
				NewOpenAIEvalRunner(repo, svc).runDue(t.Context())
			} else {
				_, err := svc.Run(t.Context(), request, 9, tc.source)
				require.NoError(t, err)
			}

			require.Len(t, repo.runs, 1)
			run := repo.runs[0]
			require.Equal(t, "degraded", run.Status)
			require.Equal(t, tc.source, run.TriggerSource)
			require.Equal(t, request.RequestedModel, run.RequestedModel)
			require.Equal(t, tc.wantModel, run.UpstreamModel)
			require.Empty(t, run.ReasoningEffort)
			require.Len(t, upstream.requests, 2)
			require.Equal(t, "fresh_linked_ticket_chain", run.Outcome.StateProbe.RetryPolicy)
			require.Len(t, run.Samples, 2)
			for _, req := range upstream.requests {
				var body struct {
					Model string `json:"model"`
				}
				require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
				require.Equal(t, tc.wantModel, body.Model)
			}
			if tc.wantUpdate {
				state, ok := accounts.updatedExtra[OpenAIBPSAccountStateExtraKey()].(OpenAIBPSAccountState)
				require.True(t, ok)
				require.True(t, state.Active)
				require.Equal(t, 3, state.DegradedStreak)
				require.Zero(t, state.HealthyStreak)
				require.True(t, state.UpdatedAt.After(initialState.UpdatedAt))
			} else {
				require.Nil(t, accounts.updatedExtra)
				require.Equal(t, initialState, readOpenAIBPSAccountState(account))
			}
		})
	}
}

func TestOpenAIEvalManualProbeRejectsInternalBPSSentinel(t *testing.T) {
	repo := &openAIEvalRepoFake{}
	svc := NewOpenAIEvalService(repo, nil, &AccountTestService{})
	_, err := svc.Run(t.Context(), OpenAIEvalRunRequest{
		AccountID: 61, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeStateProbe,
		ReasoningEffort: OpenAIEvalBPSAccountEffort,
	}, 9, "manual")
	require.ErrorContains(t, err, "unsupported reasoning effort")
	require.Zero(t, repo.leaseAcquire)
	require.Empty(t, repo.runs)
}

func TestOpenAIBPSRoutingIgnoresLegacyEvaluationTargets(t *testing.T) {
	for _, mode := range []string{OpenAIEvalBPSModeAuto, OpenAIEvalBPSModeForceOn} {
		t.Run(mode, func(t *testing.T) {
			account := &Account{ID: 61, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Extra: map[string]any{OpenAIBPSModelStateExtraKeyFor("gpt-5.4"): OpenAIBPSModelState{Active: true}},
			}
			repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{
				BPSAutoEnabled: true,
				Accounts:       []OpenAIEvalAccountConfig{{AccountID: account.ID, RequestedModel: "gpt-5.4", BPSMode: mode}},
			}}
			svc := &OpenAIGatewayService{openAIEvalRepo: repo}
			for _, model := range []string{"gpt-5.4", "gpt-6-astra"} {
				require.False(t, svc.isOpenAIBPSForwardEligible(t.Context(), account, model), "legacy modes are migration data, not routing authority")
			}
			repo.config.BPSAccounts = []OpenAIEvalBPSAccountConfig{{AccountID: account.ID, Mode: mode}}
			for _, model := range []string{"gpt-5.4", "gpt-6-astra"} {
				require.Equal(t, mode == OpenAIEvalBPSModeForceOn, svc.isOpenAIBPSForwardEligible(t.Context(), account, model), "only explicit manual force_on can authorize legacy state")
			}
			account.Extra[OpenAIBPSAccountStateExtraKey()] = OpenAIBPSAccountState{Active: true, DegradedStreak: 3}
			repo.config.Accounts = nil
			require.True(t, svc.isOpenAIBPSForwardEligible(t.Context(), account, "gpt-6-astra"), "removing evaluation targets must not disable independent account BPS")
		})
	}
}

func TestOpenAIEvalDisabledAccountProbeDoesNotMutateBPSState(t *testing.T) {
	initial := OpenAIBPSAccountState{DegradedStreak: 2, UpdatedAt: time.Now().UTC().Add(-time.Hour)}
	account := &Account{ID: 62, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Schedulable: false,
		Extra: map[string]any{OpenAIBPSAccountStateExtraKey(): initial}}
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{
		BPSAutoEnabled: true,
		BPSAccounts:    []OpenAIEvalBPSAccountConfig{{AccountID: account.ID, Mode: OpenAIEvalBPSModeAuto, FailureThreshold: 3, RecoveryThreshold: 2}},
	}}
	svc := NewOpenAIEvalService(repo, accounts, &AccountTestService{})
	svc.applyOpenAIStateProbeBPS(t.Context(), &OpenAIEvalTarget{Account: account, RequestedModel: "gpt-6-astra"},
		&OpenAIStateProbeResult{Verdict: "degraded"}, true)
	require.Nil(t, accounts.updatedExtra)
	require.Equal(t, initial, readOpenAIBPSAccountState(account))
}

func TestOpenAIEvalAccountDisabledDuringAutomaticProbeRemainsDiagnostic(t *testing.T) {
	newCodexModelsOAuthCacheServer(t, `{"models":[{"slug":"gpt-6-astra"}]}`)
	initial := OpenAIBPSAccountState{DegradedStreak: 2, UpdatedAt: time.Now().UTC().Add(-time.Hour)}
	account := &Account{ID: 63, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "probe-token"},
		Extra:       map[string]any{OpenAIBPSAccountStateExtraKey(): initial}}
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{BPSAutoEnabled: true,
		BPSAccounts: []OpenAIEvalBPSAccountConfig{{AccountID: account.ID, ProbeModel: "gpt-6-astra", Mode: OpenAIEvalBPSModeAuto, FailureThreshold: 3, RecoveryThreshold: 2}}}}
	upstream := &evalTransportStub{respond: func(_ *http.Request, call int) (*http.Response, error) {
		ticket := "ticket-a"
		if call == 2 {
			account.Schedulable = false
			ticket = "ticket-b"
		}
		return stateProbeResponse(ticket, "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-astra\"}}\n\n", http.StatusOK), nil
	}}
	accountTest := &AccountTestService{accountRepo: accounts, httpUpstream: upstream,
		openaiGatewayService: &OpenAIGatewayService{}, tlsFPProfileService: &TLSFingerprintProfileService{}}
	svc := NewOpenAIEvalService(repo, accounts, accountTest)
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: account.ID, RequestedModel: "gpt-6-astra",
		TestType: OpenAIEvalTypeStateProbe, ReasoningEffort: OpenAIEvalBPSAccountEffort}, 0, "scheduled")
	require.NoError(t, err)
	require.True(t, run.DiagnosticOnly)
	require.Nil(t, accounts.updatedExtra)
	require.Equal(t, initial, readOpenAIBPSAccountState(account))
}
