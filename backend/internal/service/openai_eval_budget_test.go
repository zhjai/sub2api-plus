//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"net/http"
	"net/http/httptrace"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type evalBudgetSpy struct {
	GatewayCache
	sends      int
	operations []string
}

func TestOpenAIEvalBudgetInterruptionsRetainExistingQuality(t *testing.T) {
	for _, scenario := range []string{"pause", "cancel", "window"} {
		t.Run(scenario, func(t *testing.T) {
			var repo *openAIEvalRepoFake
			var cancel context.CancelFunc
			svc, r, _ := evalRunHarness(t, func(req *http.Request, attempt int) (*http.Response, error) {
				if attempt == 2 {
					switch scenario {
					case "pause":
						until := time.Now().Add(time.Hour)
						repo.config.BackgroundControls[0].PausedUntil = &until
					case "cancel":
						cancel()
					case "window":
						c := req.Context().Value(openAIEvalControllerKey{}).(*openAIEvalController)
						c.deadline = time.Now().Add(-time.Second)
					}
				}
				return newJSONResponse(200, evalCompletedJSON("21")), nil
			})
			repo = r
			accounts := &qualityRunAccountRepository{openAIAccountTestRepo: svc.accounts.(*openAIAccountTestRepo)}
			svc.accounts = accounts
			repo.config = &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{AccountID: 995, RequestedModel: "gpt-5.4", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, SampleCount: 3}}}, BackgroundControls: []OpenAIEvalBackgroundControl{{AccountID: 995}}}
			svc.accountTest.openaiGatewayService.cache = &evalBudgetSpy{}
			request := OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeCandy, SampleCount: 1}
			first, err := svc.Run(t.Context(), request, 1, "manual")
			require.NoError(t, err)
			require.Equal(t, "pass", first.Status)
			key := OpenAIEvalQualityExtraKeyFor("gpt-5.4", "", OpenAIEvalTypeCandy)
			before, err := json.Marshal(accounts.accountsByID[995].Extra[key])
			require.NoError(t, err)
			require.NotEqual(t, "null", string(before))
			request.SampleCount = 3
			ctx, stop := context.WithCancel(t.Context())
			cancel = stop
			defer stop()
			partial, _ := svc.Run(ctx, request, 1, "scheduled")
			require.NotNil(t, partial)
			require.True(t, partial.DiagnosticOnly)
			require.Equal(t, "inconclusive", partial.Status)
			after, err := json.Marshal(accounts.accountsByID[995].Extra[key])
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

type evalPreDialFailureUpstream struct{ HTTPUpstream }

func (*evalPreDialFailureUpstream) SupportsSingleSend() bool { return true }
func (*evalPreDialFailureUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	httptrace.ContextClientTrace(req.Context()).GetConn("synthetic.test:443")
	return nil, errors.New("synthetic dial failure")
}

func TestOpenAIEvalBudgetRunPreDialFailureDoesNotCharge(t *testing.T) {
	svc, repo, _ := evalRunHarness(t, nil)
	spy := &evalBudgetSpy{}
	svc.accountTest.openaiGatewayService.cache = spy
	svc.accountTest.httpUpstream = &evalPreDialFailureUpstream{}
	repo.config = &OpenAIEvalConfig{BackgroundControls: []OpenAIEvalBackgroundControl{{AccountID: 995, BudgetEnabled: true}}}
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeCandy, SampleCount: 1}, 1, "manual")
	require.NoError(t, err)
	require.NotNil(t, run)
	require.Zero(t, spy.sends)
	require.Contains(t, spy.operations, "prepare")
	require.Contains(t, spy.operations, "refund")
}

type evalCompletionSpy struct {
	*openAIEvalRepoFake
	mu        sync.Mutex
	completed []OpenAIEvalScheduledRun
}

func (r *evalCompletionSpy) ClaimDueSchedulesForCompletion(context.Context, time.Time, int) ([]OpenAIEvalScheduledRun, error) {
	return r.due, nil
}
func (r *evalCompletionSpy) RenewScheduleClaim(context.Context, OpenAIEvalScheduledRun) (bool, error) {
	return true, nil
}
func (r *evalCompletionSpy) CompleteSchedule(_ context.Context, item OpenAIEvalScheduledRun, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed = append(r.completed, item)
	return nil
}
func TestOpenAIEvalCompletionRunnerLatestTargetsSkipsAndAdmissionOrder(t *testing.T) {
	svc, base, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, evalCompletedJSON("21")), nil
	})
	base.config = &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{AccountID: 995, RequestedModel: "gpt-5.4", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, SampleCount: 1}, FingerprintSchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, SampleMode: "quick"}}}, BackgroundControls: []OpenAIEvalBackgroundControl{{AccountID: 995, BudgetEnabled: true}}}
	svc.accountTest.openaiGatewayService.cache = &evalBudgetSpy{}
	base.due = []OpenAIEvalScheduledRun{{AccountID: 995, TestType: OpenAIEvalTypeFingerprint, RequestedModel: "gpt-5.4", SampleMode: "full"}, {AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4", SampleCount: 10}, {AccountID: 995, TestType: OpenAIEvalTypeModelTrace, RequestedModel: "gpt-5.4"}}
	repo := &evalCompletionSpy{openAIEvalRepoFake: base}
	svc.repo = repo
	r := NewOpenAIEvalRunner(repo, svc)
	latest, enabled, err := r.latestScheduledRequest(t.Context(), base.due[1])
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, 1, latest.SampleCount)
	r.runDue(t.Context())
	require.Len(t, repo.completed, 3)
	require.EqualValues(t, 1, upstream.calls.Load())
	require.Len(t, base.runs, 1)
	require.Equal(t, OpenAIEvalTypeCandy, base.runs[0].TestType)
}

func (s *evalBudgetSpy) AdmitAccountRPM(context.Context, int64, int, string) (AccountRPMDecision, error) {
	return AccountRPMDecision{Allowed: true}, nil
}
func (s *evalBudgetSpy) ReadAccountRPMBatch(context.Context, map[int64]int) (map[int64]AccountRPMDecision, error) {
	return map[int64]AccountRPMDecision{}, nil
}

func (s *evalBudgetSpy) OpenAIEvalBudget(_ context.Context, _ string, op OpenAIEvalBudgetOperation) (OpenAIEvalBudgetDecision, error) {
	s.operations = append(s.operations, op.Action)
	if op.Action == "confirm" {
		s.sends++
	}
	return OpenAIEvalBudgetDecision{Allowed: true}, nil
}

type evalDeletedBudgetAccounts struct{ *deletedEvalAccounts }

func (r *evalDeletedBudgetAccounts) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	return nil, nil
}
func TestOpenAIEvalBudgetGetConfigPrunesBeforeUnavailableProjection(t *testing.T) {
	account := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture"}}
	accounts := &evalDeletedBudgetAccounts{deletedEvalAccounts: &deletedEvalAccounts{items: map[int64]*Account{2: account}}}
	repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{BackgroundControls: []OpenAIEvalBackgroundControl{{AccountID: 1}, {AccountID: 2, BudgetEnabled: true}}}}
	svc := NewOpenAIEvalService(repo, accounts, nil)
	got, err := svc.GetConfig(t.Context())
	require.NoError(t, err)
	require.Len(t, got.BackgroundControls, 1)
	c := got.BackgroundControls[0]
	require.EqualValues(t, 2, c.AccountID)
	require.Nil(t, c.Runtime)
	require.Equal(t, "evaluation_budget_unavailable", c.RuntimeUnavailableReason)
	require.Len(t, repo.config.BackgroundControls, 2, "GET projection must not mutate saved controls")
	c.BudgetEnabled = false
	got.BackgroundControls = []OpenAIEvalBackgroundControl{c}
	require.NoError(t, svc.SaveConfig(t.Context(), got, 1))
	require.Empty(t, repo.config.BackgroundControls[0].RuntimeUnavailableReason)
}

func TestOpenAIEvalBudgetPauseBetweenSamplesPreservesEvidence(t *testing.T) {
	var repo *openAIEvalRepoFake
	svc, r, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		until := time.Now().Add(time.Hour)
		repo.config.BackgroundControls[0].PausedUntil = &until
		return newJSONResponse(200, evalCompletedJSON("21")), nil
	})
	repo = r
	account, err := svc.accounts.GetByID(t.Context(), 995)
	require.NoError(t, err)
	account.Schedulable = true
	repo.config = &OpenAIEvalConfig{BackgroundControls: []OpenAIEvalBackgroundControl{{AccountID: 995}}}
	spy := &evalBudgetSpy{}
	svc.accountTest.openaiGatewayService.cache = spy
	run, _ := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeCandy, SampleCount: 3}, 1, "scheduled")
	require.NotNil(t, run)
	require.True(t, run.DiagnosticOnly)
	require.Equal(t, "inconclusive", run.Status)
	require.Equal(t, 1, run.RequestCount)
	require.EqualValues(t, 1, upstream.calls.Load())
	require.Equal(t, 1, spy.sends)
	require.Equal(t, "automatic_evaluation_paused", run.Error)
	require.Contains(t, spy.operations, "release")
}

func TestOpenAIEvalBudgetFingerprintDefersManualAndRPMOneWorks(t *testing.T) {
	svc, repo, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, evalCompletedJSON("21")), nil
	})
	account, err := svc.accounts.GetByID(t.Context(), 995)
	require.NoError(t, err)
	account.Extra["rpm_limit"] = 1
	c := OpenAIEvalBackgroundControl{AccountID: 995, BudgetEnabled: true}
	require.NoError(t, normalizeOpenAIEvalBackgroundControl(&c))
	repo.config = &OpenAIEvalConfig{BackgroundControls: []OpenAIEvalBackgroundControl{c}}
	spy := &evalBudgetSpy{}
	svc.accountTest.openaiGatewayService.cache = spy
	require.True(t, openAIEvalBudgetFeasible(c, 1, 1))
	require.False(t, openAIEvalBudgetFeasible(c, 60, 1))
	require.False(t, openAIEvalBudgetFeasible(c, 200, 0))
	require.False(t, openAIEvalBudgetFeasible(c, 400, 0))
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeFingerprint, SampleMode: "quick"}, 1, "manual")
	require.Error(t, err)
	require.ErrorContains(t, err, "budget_cannot_complete_run")
	require.Nil(t, run)
	require.Zero(t, upstream.calls.Load())
	require.Zero(t, spy.sends)
	run, err = svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeCandy, SampleCount: 1}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "pass", run.Status)
	require.Equal(t, 1, spy.sends)
	// Configured numeric controls may never silently succeed without Redis.
	svc.accountTest.openaiGatewayService.cache = nil
	_, err = svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeCandy, SampleCount: 1}, 1, "manual")
	require.ErrorContains(t, err, "evaluation_budget_unavailable")
}

func TestOpenAIEvalBudgetPrismAlwaysFailsClosed(t *testing.T) {
	svc, repo, _ := evalRunHarness(t, nil)
	account, err := svc.accounts.GetByID(t.Context(), 995)
	require.NoError(t, err)
	account.Platform = "prism"
	repo.config = &OpenAIEvalConfig{}
	ctx := context.WithValue(t.Context(), openAIEvalAutomaticKey{}, true)
	target := &OpenAIEvalTarget{Account: account, Credential: account, UpstreamModel: "gpt-5.4", RequestedModel: "gpt-5.4"}
	_, release, err := svc.beginBackgroundRun(ctx, target, OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy}, "scheduled", "prism-test", 1)
	_ = release
	require.ErrorContains(t, err, "evaluation_budget_transport_unsupported")
}

func TestOpenAIEvalBudgetDuplicateCredentialControls(t *testing.T) {
	svc, repo, _ := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, evalCompletedJSON("21")), nil
	})
	accounts := svc.accounts.(*openAIAccountTestRepo)
	a := accounts.accountsByID[995]
	b := *a
	b.ID = 996
	accounts.accountsByID[996] = &b
	c := OpenAIEvalBackgroundControl{AccountID: 995, BudgetEnabled: true}
	require.NoError(t, normalizeOpenAIEvalBackgroundControl(&c))
	repo.config = &OpenAIEvalConfig{BackgroundControls: []OpenAIEvalBackgroundControl{c}}
	ns, err := svc.backgroundNamespace(t.Context(), 996)
	require.NoError(t, err)
	resolved, found, err := svc.backgroundControl(t.Context(), ns)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, c, resolved)
	other := c
	other.AccountID = 996
	other.MaxRequestsPerHour++
	repo.config.BackgroundControls = append(repo.config.BackgroundControls, other)
	require.ErrorContains(t, svc.validateBackgroundControls(t.Context(), repo.config), "conflicting")
	_, _, err = svc.backgroundControl(t.Context(), ns)
	require.ErrorContains(t, err, "conflicting")
	// A shadow shares the actual OAuth namespace, not the local row ID.
	a.Type = AccountTypeOAuth
	a.Credentials = map[string]any{"chatgpt_account_id": "synthetic-account", "access_token": "synthetic-token"}
	parent := a.ID
	b.ParentAccountID = &parent
	ns, err = svc.backgroundNamespace(t.Context(), 996)
	require.NoError(t, err)
	require.Equal(t, openAIEvalCredentialNamespace(a), ns)
	require.NotContains(t, ns, "synthetic")
	a.Credentials["chatgpt_user_id"] = "fixture-user"
	duplicate := *a
	duplicate.ID = 997
	duplicate.Credentials = map[string]any{"chatgpt_account_id": "synthetic-account", "access_token": "another-fixture-token"}
	require.Equal(t, openAIEvalCredentialNamespace(a), openAIEvalCredentialNamespace(&duplicate), "missing user metadata cannot bypass account-wide controls")
}

type evalPeerAccounts struct {
	*openAIAccountTestRepo
	scans int
}

func (r *evalPeerAccounts) ListByPlatform(_ context.Context, platform string) ([]Account, error) {
	var rows []Account
	for _, a := range r.accountsByID {
		if a.Platform == platform {
			rows = append(rows, *a)
		}
	}
	return rows, nil
}
func (r *evalPeerAccounts) ListAllWithFilters(ctx context.Context, platform, _, _, _ string, _ int64, _ string) ([]Account, error) {
	r.scans++
	return r.ListByPlatform(ctx, platform)
}

func TestOpenAIEvalBudgetPollingSharesDuplicateControlsWithoutFullScans(t *testing.T) {
	svc, repo, _ := evalRunHarness(t, nil)
	base := svc.accounts.(*openAIAccountTestRepo)
	a := base.accountsByID[995]
	a.Type = AccountTypeOAuth
	a.Credentials = map[string]any{"chatgpt_account_id": "fixture-principal", "access_token": "fixture-bearer"}
	b := *a
	b.ID = 996
	b.Credentials = map[string]any{"access_token": "fixture-bearer"}
	base.accountsByID[b.ID] = &b
	accounts := &evalPeerAccounts{openAIAccountTestRepo: base}
	svc.accounts = accounts
	repo.config = &OpenAIEvalConfig{BackgroundControls: []OpenAIEvalBackgroundControl{{AccountID: 995}}}
	credential, err := svc.backgroundCredential(t.Context(), 996)
	require.NoError(t, err)
	require.Equal(t, 1, accounts.scans)
	c := &openAIEvalController{service: svc, namespace: openAIEvalCredentialNamespace(credential), credential: credential, accountID: 996, automatic: true, store: &evalBudgetSpy{}}
	for range 5 {
		control, err := c.current(t.Context())
		require.NoError(t, err)
		require.EqualValues(t, 995, control.AccountID)
	}
	require.Equal(t, 1, accounts.scans, "interval polling must not enumerate all accounts")
	until := time.Now().Add(time.Hour)
	repo.config.BackgroundControls[0].PausedUntil = &until
	_, err = c.current(t.Context())
	require.ErrorContains(t, err, "automatic_evaluation_paused")
	require.Equal(t, 1, accounts.scans)
}

func TestOpenAIEvalBudgetAdmissionSharesOAuthControls(t *testing.T) {
	for _, scenario := range []string{"duplicate", "incomplete_duplicate", "shadow", "control_on_incomplete_duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, upstream := evalRunHarness(t, nil)
			base := svc.accounts.(*openAIAccountTestRepo)
			a := base.accountsByID[995]
			a.Type = AccountTypeOAuth
			a.Credentials = map[string]any{"chatgpt_account_id": "fixture-principal", "access_token": "fixture-bearer"}
			b := *a
			b.ID = 996
			if scenario == "incomplete_duplicate" || scenario == "control_on_incomplete_duplicate" {
				b.Credentials = map[string]any{"access_token": "fixture-bearer"}
			}
			if scenario == "shadow" {
				parent := a.ID
				b.ParentAccountID = &parent
			}
			base.accountsByID[b.ID] = &b
			svc.accounts = &evalPeerAccounts{openAIAccountTestRepo: base}
			controlID, runAccount := a.ID, &b
			if scenario == "control_on_incomplete_duplicate" {
				controlID, runAccount = b.ID, a
			}
			until := time.Now().Add(time.Hour)
			control := OpenAIEvalBackgroundControl{AccountID: controlID, PausedUntil: &until, BudgetEnabled: true, MaxRequestsPerHour: 1}
			require.NoError(t, normalizeOpenAIEvalBackgroundControl(&control))
			repo.config = &OpenAIEvalConfig{BackgroundControls: []OpenAIEvalBackgroundControl{control}}
			spy := &evalBudgetSpy{}
			svc.accountTest.openaiGatewayService.cache = spy
			target := &OpenAIEvalTarget{Account: runAccount, Credential: a}
			request := OpenAIEvalRunRequest{AccountID: runAccount.ID, TestType: OpenAIEvalTypeCandy}
			_, release, err := svc.beginBackgroundRun(t.Context(), target, request, "scheduled", "fixture-owner", 1)
			require.ErrorContains(t, err, "automatic_evaluation_paused")
			require.Nil(t, release)
			require.Empty(t, spy.operations)

			repo.config.BackgroundControls[0].PausedUntil = nil
			for _, source := range []string{"scheduled", "manual"} {
				_, release, err = svc.beginBackgroundRun(t.Context(), target, request, source, "fixture-owner", 2)
				require.ErrorContains(t, err, "budget_cannot_complete_run")
				require.Nil(t, release)
			}
			ctx, release, err := svc.beginBackgroundRun(t.Context(), target, request, "scheduled", "fixture-owner", 1)
			require.NoError(t, err)
			defer release()
			controller := ctx.Value(openAIEvalControllerKey{}).(*openAIEvalController)
			require.Equal(t, openAIEvalCredentialNamespace(a), controller.namespace)
			require.Equal(t, controlID, controller.initialControl.AccountID)
			require.True(t, controller.initialControl.BudgetEnabled)
			require.Equal(t, 1, controller.initialControl.MaxRequestsPerHour)
			require.Contains(t, spy.operations, "admit")

			repo.config.BackgroundControls[0].PausedUntil = &until
			require.ErrorContains(t, openAIEvalBeforeSend(ctx), "automatic_evaluation_paused")
			require.NotContains(t, spy.operations, "prepare")
			require.Zero(t, spy.sends)
			require.Zero(t, upstream.calls.Load())
		})
	}
}

func TestOpenAIEvalBudgetActualCredentialPeersAndCooldown(t *testing.T) {
	svc, _, _ := evalRunHarness(t, nil)
	base := svc.accounts.(*openAIAccountTestRepo)
	a := base.accountsByID[995]
	a.Concurrency = 1
	b := *a
	b.ID = 996
	base.accountsByID[996] = &b
	other := *a
	other.ID = 997
	other.Credentials = map[string]any{"api_key": "different-fixture"}
	base.accountsByID[997] = &other
	peers := &evalPeerAccounts{openAIAccountTestRepo: base}
	svc.accounts = peers
	svc.accountTest.accountRepo = peers
	rows, err := svc.accountTest.openAIEvalCredentialPeers(t.Context(), a)
	require.NoError(t, err)
	require.Equal(t, []AccountWithConcurrency{{ID: 995, MaxConcurrency: 1}, {ID: 996, MaxConcurrency: 1}}, rows)
	ctx := context.WithValue(t.Context(), openAIEvalAutomaticKey{}, true)
	until := time.Now().Add(time.Hour)
	for _, apply := range []func(){func() { a.RateLimitResetAt = &until }, func() { a.OverloadUntil = &until }, func() { a.TempUnschedulableUntil = &until }} {
		a.RateLimitResetAt, a.OverloadUntil, a.TempUnschedulableUntil = nil, nil, nil
		apply()
		require.Error(t, svc.accountTest.checkOpenAIEvalAutomaticAccount(ctx, a))
	}
	a.RateLimitResetAt, a.OverloadUntil, a.TempUnschedulableUntil = nil, nil, nil
	require.NoError(t, svc.accountTest.checkOpenAIEvalAutomaticAccount(ctx, a))
}

func TestOpenAIEvalBudgetIncompleteDuplicateCannotEscapeControls(t *testing.T) {
	svc, repo, _ := evalRunHarness(t, nil)
	base := svc.accounts.(*openAIAccountTestRepo)
	a := base.accountsByID[995]
	a.Type = AccountTypeOAuth
	a.Credentials = map[string]any{"chatgpt_account_id": "fixture-principal", "access_token": "fixture-bearer"}
	b := *a
	b.ID = 996
	b.Credentials = map[string]any{"access_token": "fixture-bearer"}
	base.accountsByID[b.ID] = &b
	svc.accounts = &evalPeerAccounts{openAIAccountTestRepo: base}
	c := OpenAIEvalBackgroundControl{AccountID: 995, BudgetEnabled: true}
	require.NoError(t, normalizeOpenAIEvalBackgroundControl(&c))
	repo.config = &OpenAIEvalConfig{BackgroundControls: []OpenAIEvalBackgroundControl{c}}
	ns, err := svc.backgroundNamespace(t.Context(), 996)
	require.NoError(t, err)
	require.Equal(t, openAIEvalCredentialNamespace(a), ns)
	_, found, err := svc.backgroundControl(t.Context(), ns)
	require.NoError(t, err)
	require.True(t, found)
	c.AccountID = 996
	cfg := &OpenAIEvalConfig{BackgroundControls: []OpenAIEvalBackgroundControl{c}}
	require.NoError(t, svc.validateBackgroundControls(t.Context(), cfg))
	a.Credentials = map[string]any{"access_token": "fixture-bearer"}
	require.ErrorContains(t, svc.validateBackgroundControls(t.Context(), cfg), "stable ChatGPT")
}

func TestOpenAIEvalBudgetSeparateTransportFailsClosed(t *testing.T) {
	svc, repo, _ := evalRunHarness(t, nil)
	a, err := svc.accounts.GetByID(t.Context(), 995)
	require.NoError(t, err)
	a.Platform = "prism"
	repo.config = &OpenAIEvalConfig{BackgroundControls: []OpenAIEvalBackgroundControl{{AccountID: 995, BudgetEnabled: true}}}
	spy := &evalBudgetSpy{}
	svc.accountTest.openaiGatewayService.cache = spy
	_, release, err := svc.beginBackgroundRun(t.Context(), &OpenAIEvalTarget{Account: a, Credential: a}, OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy}, "manual", "fixture-owner", 1)
	require.ErrorContains(t, err, "evaluation_budget_transport_unsupported")
	require.Nil(t, release)
	require.Zero(t, spy.sends)
}
