//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestScoreOpenAIEvalCandyRequiresCorrectLeadingAnswer(t *testing.T) {
	for _, answer := range []string{"29", "答案：29 颗。", "二十九颗"} {
		outcome := ScoreOpenAIEvalCandy(answer)
		require.Equal(t, "pass", outcome.Status, answer)
		require.Equal(t, "neutral", outcome.Scheduling)
		require.Equal(t, "low", outcome.Confidence)
	}

	for _, answer := range []string{"21", "至少 30 颗，29 颗不够。", "答案是 20，29 才是最大反例。", "二十颗", "29.5"} {
		outcome := ScoreOpenAIEvalCandy(answer)
		require.Equal(t, "warning", outcome.Status, answer)
		require.Equal(t, "alert_only", outcome.Scheduling)
	}
}

func TestNormalizeOpenAIEvalFingerprintAnswer(t *testing.T) {
	probe := OpenAIEvalProbe{Kind: "int", Low: 1, High: 100}
	for raw, expected := range map[string]string{" ４２。 ": "42", "四十二": "42", "42 because random": "42", "seventy": "70", "forty-two": "42"} {
		got, ok := NormalizeOpenAIEvalFingerprintAnswer(raw, probe)
		require.True(t, ok, raw)
		require.Equal(t, expected, got)
	}
	for _, raw := range []string{"", "抱歉，我无法回答", "I cannot answer", "0", "一百零一"} {
		_, ok := NormalizeOpenAIEvalFingerprintAnswer(raw, probe)
		require.False(t, ok, raw)
	}

	coin := OpenAIEvalProbe{Kind: "coin"}
	got, ok := NormalizeOpenAIEvalFingerprintAnswer("正面", coin)
	require.True(t, ok)
	require.Equal(t, "heads", got)
	letter := OpenAIEvalProbe{Kind: "letter"}
	got, ok = NormalizeOpenAIEvalFingerprintAnswer("Z", letter)
	require.True(t, ok)
	require.Equal(t, "z", got)
}

func TestOpenAIEvalFingerprintJSD(t *testing.T) {
	require.True(t, math.IsNaN(OpenAIEvalFingerprintJSD(nil, []string{"a"})))
	require.InDelta(t, 0, OpenAIEvalFingerprintJSD([]string{"a", "b"}, []string{"a", "b"}), 1e-12)
	require.InDelta(t, 1, OpenAIEvalFingerprintJSD([]string{"a"}, []string{"b"}), 1e-12)
}

func TestOpenAIEvalPinnedFingerprintDataAndPlans(t *testing.T) {
	require.Len(t, OpenAIEvalFingerprintProbes, 16)
	require.Len(t, OpenAIEvalFingerprintBaselines, 7)
	for _, probe := range OpenAIEvalFingerprintProbes {
		for _, baseline := range OpenAIEvalFingerprintBaselines {
			require.GreaterOrEqual(t, len(baseline.Cells[probe.ID]), 10, "%s/%s", baseline.Model, probe.ID)
		}
	}
	for _, tc := range []struct {
		mode string
		want int
	}{{"quick", 60}, {"standard", 200}, {"strict", 400}} {
		got, err := OpenAIEvalFingerprintSampleCount(tc.mode)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
		cells, repeats, err := OpenAIEvalFingerprintPlan(tc.mode)
		require.NoError(t, err)
		require.Equal(t, tc.want, cells*repeats)
	}
	for _, probe := range OpenAIEvalFingerprintProbes {
		if probe.Kind == "int" {
			require.GreaterOrEqual(t, probe.Low, 0)
			require.GreaterOrEqual(t, probe.High, probe.Low)
		}
	}
}

func TestOpenAIEvalInsufficientAndFingerprintIdentityAreAlertOnly(t *testing.T) {
	probes := OpenAIEvalFingerprintProbes[:4]
	samples := make([]OpenAIEvalSample, 0, 40)
	baselines := []OpenAIEvalFingerprintBaseline{{Model: "model-a", Cells: map[string][]string{}}}
	for _, probe := range probes {
		for i := 0; i < 10; i++ {
			answer := "41"
			if probe.Kind == "coin" {
				answer = "heads"
			} else if probe.Kind != "int" {
				answer = "blue"
			}
			samples = append(samples, OpenAIEvalSample{ProbeID: probe.ID, Answer: answer})
			baselines[0].Cells[probe.ID] = append(baselines[0].Cells[probe.ID], answer)
		}
	}

	insufficient := ScoreOpenAIEvalFingerprint("model-a", samples[:3], baselines, 60)
	require.Equal(t, "insufficient", insufficient.Status)
	require.Nil(t, insufficient.MeanJSD)

	identified := ScoreOpenAIEvalFingerprint("model-a", samples, baselines, 40)
	require.Equal(t, "suspected_normal", identified.Status)
	require.Equal(t, "alert_only", OpenAIEvalSchedulingDisposition(OpenAIEvalTypeFingerprint, OpenAIEvalOutcome{Status: "fail", Confidence: "high"}, true))
	require.Equal(t, "alert_only", OpenAIEvalSchedulingDisposition(OpenAIEvalTypeCandy, OpenAIEvalOutcome{Status: "fail", Confidence: "high"}, true))
	require.Equal(t, "disabled", OpenAIEvalSchedulingDisposition(OpenAIEvalTypeCandy, OpenAIEvalOutcome{Status: "fail", Confidence: "high"}, false))
	require.NotEmpty(t, identified.NearestModel)
}

type openAIEvalUpstreamStub struct {
	response *http.Response
	err      error
	request  *http.Request
	profile  *tlsfingerprint.Profile
}

func (s *openAIEvalUpstreamStub) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, errors.New("unexpected non-TLS transport")
}

func (s *openAIEvalUpstreamStub) DoWithTLS(req *http.Request, _ string, _ int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	s.request = req
	s.profile = profile
	return s.response, s.err
}

type openAIEvalModelGatewayStub struct {
	body []byte
}

func (s *openAIEvalModelGatewayStub) FetchOpenAIModelsList(context.Context, *Account) (*OpenAIModelsResponse, error) {
	return &OpenAIModelsResponse{Body: s.body}, nil
}

func TestRunOpenAIEvalSampleDoesNotMutateAccountHealth(t *testing.T) {
	responseBody := `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"42"}]}],"usage":{"input_tokens":12,"output_tokens":3}}`
	upstream := &openAIEvalUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}}
	account := newCodexModelsAPIKeyTestAccount("https://upstream.example/v1")
	account.ID = 44
	account.Platform = PlatformOpenAI
	account.Type = AccountTypeAPIKey
	if account.Credentials == nil {
		account.Credentials = make(map[string]any)
	}
	account.Extra = make(map[string]any)
	account.Credentials["api_key"] = "sk-eval-test"
	account.Extra["openai_responses_supported"] = true
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{
		accountsByID: map[int64]*Account{account.ID: account},
	}}
	svc := &AccountTestService{
		accountRepo:         repo,
		httpUpstream:        upstream,
		cfg:                 testConfig(),
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	modelUpstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		require.Equal(t, "/v1/models", req.URL.Path)
		return ordinaryModelsUpstreamResponse(`{"data":[{"id":"gpt-5.4"}]}`), nil
	}}
	modelGateway := newCodexModelsAPIKeyTestService(modelUpstream)
	svc.openaiGatewayService = modelGateway

	target, err := svc.ResolveOpenAIEvalTarget(context.Background(), account.ID, "gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", target.UpstreamModel)
	result, err := svc.RunOpenAIEvalSample(context.Background(), target, "Pick a random number.", "high")
	require.NoError(t, err)
	require.Equal(t, "42", result.Text)
	require.EqualValues(t, 12, result.InputTokens)
	require.EqualValues(t, 3, result.OutputTokens)
	require.Equal(t, "Bearer sk-eval-test", upstream.request.Header.Get("Authorization"))
	var requestBody map[string]any
	body, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &requestBody))
	require.Equal(t, "gpt-5.4", requestBody["model"])
	require.Equal(t, false, requestBody["store"])
	require.Equal(t, false, requestBody["stream"])
	require.Equal(t, "high", requestBody["reasoning"].(map[string]any)["effort"])
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.request.Context()))
	require.Nil(t, upstream.profile)
	require.Zero(t, repo.setErrorID)
	require.Zero(t, repo.rateLimitedID)
	require.Nil(t, repo.updatedExtra)
	require.Zero(t, repo.clearedErrorID)
}

func TestOpenAIEvalTargetRequiresModelCatalogAndResponsesSupport(t *testing.T) {
	account := newCodexModelsAPIKeyTestAccount("https://upstream.example/v1")
	account.ID = 45
	account.Extra = make(map[string]any)
	account.Extra["openai_responses_supported"] = true
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	modelUpstream := &codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		return ordinaryModelsUpstreamResponse(`{"data":[{"id":"custom-model"}]}`), nil
	}}
	svc := &AccountTestService{accountRepo: repo, openaiGatewayService: newCodexModelsAPIKeyTestService(modelUpstream)}
	_, err := svc.ResolveOpenAIEvalTarget(context.Background(), account.ID, "gpt-5.4")
	require.ErrorContains(t, err, "not available in the account catalog")

	account.Extra["openai_responses_supported"] = false
	_, err = svc.ResolveOpenAIEvalTarget(context.Background(), account.ID, "custom-model")
	require.ErrorContains(t, err, "not in the supported OpenAI text-model catalog")

	account.Extra["openai_responses_supported"] = true
	account.Credentials["model_mapping"] = map[string]any{"gpt-5.4": "custom-model"}
	target, err := svc.ResolveOpenAIEvalTarget(context.Background(), account.ID, "gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, "custom-model", target.UpstreamModel)
}

func TestRunOpenAIEvalSampleRejectsHTTPErrorAndIncompleteWithoutHealthMutation(t *testing.T) {
	account := newCodexModelsAPIKeyTestAccount("https://upstream.example/v1")
	account.ID = 46
	account.Extra = make(map[string]any)
	account.Credentials["api_key"] = "sk-eval-test"
	account.Extra["openai_responses_supported"] = true
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	target := &OpenAIEvalTarget{Account: account, Credential: account, RequestedModel: "gpt-5.4", UpstreamModel: "gpt-5.4"}

	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"error":{"code":"rate_limit"}}`, want: "HTTP 429"},
		{name: "incomplete terminal", status: http.StatusOK, body: `{"status":"incomplete","output":[],"incomplete_details":{"reason":"max_output_tokens"}}`, want: "did not complete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &openAIEvalUpstreamStub{response: &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: testConfig(), tlsFPProfileService: &TLSFingerprintProfileService{}}
			_, err := svc.RunOpenAIEvalSample(context.Background(), target, "probe", "")
			require.ErrorContains(t, err, tc.want)
			require.Zero(t, repo.setErrorID)
			require.Zero(t, repo.rateLimitedID)
			require.Nil(t, repo.updatedExtra)
		})
	}
}

type openAIEvalRepoFake struct {
	config       *OpenAIEvalConfig
	runs         []*OpenAIEvalRun
	audit        []OpenAIEvalAuditEvent
	due          []OpenAIEvalScheduledRun
	leaseHeld    bool
	leaseAcquire int
	leaseRelease int
}

func (r *openAIEvalRepoFake) GetConfig(context.Context) (*OpenAIEvalConfig, error) {
	if r.config == nil {
		return &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{}}, nil
	}
	return r.config, nil
}

func (r *openAIEvalRepoFake) SaveConfig(_ context.Context, config *OpenAIEvalConfig, _ int64) error {
	r.config = config
	return nil
}

func (r *openAIEvalRepoFake) CreateRun(_ context.Context, run *OpenAIEvalRun) (int64, error) {
	r.runs = append(r.runs, run)
	return int64(len(r.runs)), nil
}

func (r *openAIEvalRepoFake) FinishRun(_ context.Context, id int64, run *OpenAIEvalRun) error {
	if id <= 0 || int(id) > len(r.runs) {
		return errors.New("run not found")
	}
	copy := *run
	r.runs[id-1] = &copy
	return nil
}

func (r *openAIEvalRepoFake) ListRuns(context.Context, OpenAIEvalRunFilter) ([]OpenAIEvalRun, error) {
	result := make([]OpenAIEvalRun, len(r.runs))
	for i, run := range r.runs {
		result[i] = *run
	}
	return result, nil
}

func (r *openAIEvalRepoFake) ListAuditEvents(context.Context, int) ([]OpenAIEvalAuditEvent, error) {
	return append([]OpenAIEvalAuditEvent(nil), r.audit...), nil
}

func (r *openAIEvalRepoFake) RecordAuditEvent(_ context.Context, actorID int64, action string, payload map[string]any) error {
	r.audit = append(r.audit, OpenAIEvalAuditEvent{ActorID: actorID, Action: action, Payload: payload})
	return nil
}

func (r *openAIEvalRepoFake) ClaimDueSchedules(context.Context, time.Time, int) ([]OpenAIEvalScheduledRun, error) {
	return append([]OpenAIEvalScheduledRun(nil), r.due...), nil
}

func (r *openAIEvalRepoFake) AcquireLease(context.Context, string, string, time.Duration) (bool, error) {
	r.leaseAcquire++
	return !r.leaseHeld, nil
}

func (r *openAIEvalRepoFake) RenewLease(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}

func (r *openAIEvalRepoFake) ReleaseLease(context.Context, string, string) error {
	r.leaseRelease++
	return nil
}

func TestOpenAIEvalSchedulesHaveBoundedIntervalsAndFingerprintSampleModes(t *testing.T) {
	cases := []struct {
		name     string
		schedule OpenAIEvalSchedule
		testType string
		wantErr  bool
	}{
		{name: "candy minimum", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 299}, testType: OpenAIEvalTypeCandy, wantErr: true},
		{name: "candy accepted", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}, testType: OpenAIEvalTypeCandy},
		{name: "fingerprint below five minutes", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 299, SampleMode: "quick"}, testType: OpenAIEvalTypeFingerprint, wantErr: true},
		{name: "fingerprint missing mode", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 86400}, testType: OpenAIEvalTypeFingerprint, wantErr: true},
		{name: "fingerprint accepted", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 5 * 60, SampleMode: "strict"}, testType: OpenAIEvalTypeFingerprint},
		{name: "model trace minimum", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 5 * 60}, testType: OpenAIEvalTypeModelTrace},
		{name: "state probe minimum", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 5 * 60}, testType: OpenAIEvalTypeStateProbe},
		{name: "state probe too frequent", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 5*60 - 1}, testType: OpenAIEvalTypeStateProbe, wantErr: true},
		{name: "custom interval has no product cap", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 365 * 24 * 3600}, testType: OpenAIEvalTypeCandy},
		{name: "custom interval must fit database storage", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: int(OpenAIEvalMaxIntervalSeconds) + 1}, testType: OpenAIEvalTypeCandy, wantErr: true},
		{name: "jitter allowed at minimum", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, JitterSeconds: 150}, testType: OpenAIEvalTypeCandy},
		{name: "jitter exceeds half interval", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, JitterSeconds: 151}, testType: OpenAIEvalTypeCandy, wantErr: true},
		{name: "jitter capped at one hour", schedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 24 * 3600, JitterSeconds: 3601}, testType: OpenAIEvalTypeCandy, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOpenAIEvalSchedule(&tc.schedule, tc.testType)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOpenAIEvalSaveConfigDefaultsEffectsOffAndValidatesRoutes(t *testing.T) {
	repo := &openAIEvalRepoFake{}
	svc := NewOpenAIEvalService(repo, nil, nil)
	config := &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{
		AccountID: 1, RequestedModel: "gpt-5.4", ReasoningEffort: "high",
		CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 900},
	}}}
	require.NoError(t, svc.SaveConfig(context.Background(), config, 9))
	require.False(t, repo.config.EffectsEnabled)
	config.Accounts = append(config.Accounts, config.Accounts[0])
	require.ErrorContains(t, svc.SaveConfig(context.Background(), config, 9), "duplicate")
	config.Accounts = config.Accounts[:1]
	config.Accounts[0].RequestedModel = "claude-opus"
	require.ErrorContains(t, svc.SaveConfig(context.Background(), config, 9), "invalid evaluation route")
}

func TestOpenAIEvalBPSAccountIntervalHasNoProductCapButRejectsStorageOverflow(t *testing.T) {
	account := &Account{ID: 61, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	repo := &openAIEvalRepoFake{}
	svc := NewOpenAIEvalService(repo, accounts, nil)
	config := &OpenAIEvalConfig{BPSAccounts: []OpenAIEvalBPSAccountConfig{{
		AccountID: account.ID, Mode: OpenAIEvalBPSModeAuto, FailureThreshold: 3, RecoveryThreshold: 2,
		IntervalSeconds: 365 * 24 * 3600,
	}}}
	require.NoError(t, svc.SaveConfig(context.Background(), config, 9))

	config.BPSAccounts[0].IntervalSeconds = int(OpenAIEvalMaxIntervalSeconds) + 1
	require.ErrorContains(t, svc.SaveConfig(context.Background(), config, 9), "fit database integer storage")
}

func TestOpenAIEvalRunnerNormalizesBPSAccountSentinelBeforePublicEffortValidation(t *testing.T) {
	repo := &openAIEvalRepoFake{due: []OpenAIEvalScheduledRun{{
		AccountID:       61,
		TestType:        OpenAIEvalTypeStateProbe,
		RequestedModel:  "gpt-5.4",
		ReasoningEffort: OpenAIEvalBPSAccountEffort,
	}}}
	service := NewOpenAIEvalService(repo, nil, &AccountTestService{})
	runner := NewOpenAIEvalRunner(repo, service)

	runner.runDue(context.Background())

	// ResolveOpenAIEvalTarget intentionally fails later because this focused
	// test has no account dependencies. Reaching the lease proves the runner's
	// internal sentinel passed through Run's public effort validation.
	require.Equal(t, 1, repo.leaseAcquire)
	require.Equal(t, 1, repo.leaseRelease)
}

func TestOpenAIEvalInitializeRestoresEffectsSwitch(t *testing.T) {
	t.Cleanup(func() { SetOpenAIEvalEffectsEnabled(false) })
	repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{EffectsEnabled: true}}
	svc := NewOpenAIEvalService(repo, nil, nil)
	SetOpenAIEvalEffectsEnabled(false)
	require.NoError(t, svc.Initialize(context.Background()))
	require.True(t, OpenAIEvalEffectsEnabled())

	repo.config.EffectsEnabled = false
	require.NoError(t, svc.Initialize(context.Background()))
	require.False(t, OpenAIEvalEffectsEnabled())
}

func TestOpenAIEvalEffectsDisabledSuppressesSchedulingPolicy(t *testing.T) {
	t.Cleanup(func() {
		SetOpenAIEvalEffectsEnabled(false)
		SetOpenAIEvalSchedulingPolicySnapshot(nil)
	})
	SetOpenAIEvalSchedulingPolicySnapshot(&OpenAIEvalConfig{
		EffectsEnabled:   true,
		SchedulingPolicy: OpenAIEvalSchedulingPolicyStabilityFirst,
		Policies: []OpenAIEvalSchedulingPolicyRule{{
			RequestedModel:  "gpt-6-astra",
			ReasoningEffort: "high",
			Policy:          OpenAIEvalSchedulingPolicyCostFirst,
		}},
	})
	SetOpenAIEvalEffectsEnabled(false)
	require.Empty(t, OpenAIEvalSchedulingPolicyForRequest("gpt-6-astra", "high"))

	SetOpenAIEvalEffectsEnabled(true)
	require.Equal(t, OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyForRequest("gpt-6-astra", "high"))
	require.Equal(t, OpenAIEvalSchedulingPolicyStabilityFirst, OpenAIEvalSchedulingPolicyForRequest("gpt-6-astra", "low"))
}

func TestOpenAIBPSForceOnBypassesMasterSwitchButNotLocks(t *testing.T) {
	account := &Account{ID: 88, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{
		BPSAutoEnabled: false,
		BPSAccounts:    []OpenAIEvalBPSAccountConfig{{AccountID: account.ID, Mode: OpenAIEvalBPSModeForceOn}},
	}}
	svc := &OpenAIGatewayService{openAIEvalRepo: repo}
	require.True(t, svc.isOpenAIBPSForwardEligible(context.Background(), account, "gpt-6-astra"))

	account.Extra = map[string]any{openAIBPSModelStateKey("gpt-6-astra"): OpenAIBPSModelState{DisabledReason: "upstream_403"}}
	require.False(t, svc.isOpenAIBPSForwardEligible(context.Background(), account, "gpt-6-astra"))
}

func TestOpenAIEvalRouteHealthIsScopedAndExpires(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	health := OpenAIEvalRouteHealth{}
	health = updateOpenAIEvalRouteHealth(health, 7, "gpt-6-astra", "high", now, true, "response_incomplete")
	require.Equal(t, 1, health.HardFailureStreak)
	require.Zero(t, health.Penalty)
	_, active := ReadOpenAIEvalRouteHealthFromAccount(&Account{Extra: map[string]any{OpenAIEvalRouteHealthExtraKeyFor("gpt-6-astra", "high"): health}}, "gpt-6-astra", "high", now)
	require.False(t, active)
	raw, stored := readOpenAIEvalRouteHealthStateFromAccount(&Account{Extra: map[string]any{OpenAIEvalRouteHealthExtraKeyFor("gpt-6-astra", "high"): health}}, "gpt-6-astra", "high")
	require.True(t, stored)
	require.Equal(t, 1, raw.HardFailureStreak)

	health = updateOpenAIEvalRouteHealth(health, 7, "gpt-6-astra", "high", now.Add(time.Minute), true, "response_incomplete")
	require.Equal(t, 2, health.HardFailureStreak)
	require.Equal(t, float64(1), health.Penalty)
	account := &Account{Extra: map[string]any{OpenAIEvalRouteHealthExtraKeyFor("gpt-6-astra", "high"): health}}
	_, active = ReadOpenAIEvalRouteHealthFromAccount(account, "gpt-6-astra", "high", now.Add(2*time.Minute))
	require.True(t, active)
	_, active = ReadOpenAIEvalRouteHealthFromAccount(account, "gpt-6-astra", "low", now.Add(2*time.Minute))
	require.False(t, active)
	_, active = ReadOpenAIEvalRouteHealthFromAccount(account, "gpt-5.4", "high", now.Add(2*time.Minute))
	require.False(t, active)
	_, active = ReadOpenAIEvalRouteHealthFromAccount(account, "gpt-6-astra", "high", now.Add(OpenAIEvalRouteHealthTTL+2*time.Minute))
	require.False(t, active)
}

func TestOpenAIEvalRouteHealthAppearsInSchedulerCandidateExplanation(t *testing.T) {
	now := time.Now().UTC()
	health := updateOpenAIEvalRouteHealth(OpenAIEvalRouteHealth{}, 9, "gpt-6-astra", "high", now, true, "response_failed")
	health = updateOpenAIEvalRouteHealth(health, 9, "gpt-6-astra", "high", now.Add(time.Second), true, "response_failed")
	account := &Account{ID: 9, Extra: map[string]any{OpenAIEvalRouteHealthExtraKeyFor("gpt-6-astra", "high"): health}}
	candidates := buildOpenAIAccountScheduleCandidates(openAIAccountLoadPlan{candidates: []openAIAccountCandidateScore{{account: account}}}, nil, "gpt-6-astra", "high")
	require.Len(t, candidates, 1)
	require.Equal(t, float64(1), candidates[0].EvaluationPenalty)
	other := buildOpenAIAccountScheduleCandidates(openAIAccountLoadPlan{candidates: []openAIAccountCandidateScore{{account: account}}}, nil, "gpt-6-astra", "low")
	require.Len(t, other, 1)
	require.Zero(t, other[0].EvaluationPenalty)
}

func TestOpenAIEvalRunCandyAndFingerprintUseSafeRouteOutcomes(t *testing.T) {
	newHarness := func(answer string) (*OpenAIEvalService, *openAIEvalRepoFake, *openAIAccountTestRepo, *openAIEvalUpstreamStub) {
		account := newCodexModelsAPIKeyTestAccount("https://upstream.example/v1")
		account.ID = 51
		account.Extra = map[string]any{"openai_responses_supported": true}
		account.Credentials["api_key"] = "sk-eval-test"
		repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
		modelUpstream := &codexModelsHTTPUpstreamStub{do: func(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			return ordinaryModelsUpstreamResponse(`{"data":[{"id":"gpt-5.4"}]}`), nil
		}}
		response := `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"` + answer + `"}]}],"usage":{"input_tokens":5,"output_tokens":2}}`
		upstream := &openAIEvalUpstreamStub{response: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}}
		accountTest := &AccountTestService{
			accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{RunMode: config.RunModeStandard},
			tlsFPProfileService:  &TLSFingerprintProfileService{},
			openaiGatewayService: newCodexModelsAPIKeyTestService(modelUpstream),
		}
		evalRepo := &openAIEvalRepoFake{}
		return NewOpenAIEvalService(evalRepo, repo, accountTest), evalRepo, repo, upstream
	}

	t.Run("Candy failed answer is alert only", func(t *testing.T) {
		svc, evalRepo, healthRepo, _ := newHarness("20")
		run, err := svc.Run(context.Background(), OpenAIEvalRunRequest{AccountID: 51, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4", ReasoningEffort: "high"}, 8, "manual")
		require.NoError(t, err)
		require.Equal(t, "warning", run.Status)
		require.Equal(t, "alert_only", run.Outcome.Scheduling)
		require.Len(t, evalRepo.runs, 1)
		require.Equal(t, 1, evalRepo.leaseAcquire)
		require.Equal(t, 1, evalRepo.leaseRelease)
		require.Zero(t, healthRepo.setErrorID)
		require.Zero(t, healthRepo.rateLimitedID)
		require.Nil(t, healthRepo.updatedExtra, "Candy probes are alert-only and must not mutate route health")
	})

	t.Run("fingerprint uses pinned baseline but remains alert only", func(t *testing.T) {
		svc, evalRepo, healthRepo, _ := newHarness("42")
		run, err := svc.Run(context.Background(), OpenAIEvalRunRequest{AccountID: 51, TestType: OpenAIEvalTypeFingerprint, RequestedModel: "gpt-5.4", ReasoningEffort: "high", SampleMode: "quick"}, 8, "manual")
		require.NoError(t, err)
		require.NotEmpty(t, run.Status)
		require.Equal(t, "alert_only", run.Outcome.Scheduling)
		require.Equal(t, 60, run.RequestCount)
		require.Len(t, run.Samples, 60)
		require.Greater(t, run.Outcome.SampleCount, 0)
		require.Equal(t, run.Outcome.ExpectedCount, 60)
		require.NotNil(t, run.Outcome.Fingerprint)
		require.Zero(t, healthRepo.setErrorID)
		require.Zero(t, healthRepo.rateLimitedID)
		require.Nil(t, healthRepo.updatedExtra, "Fingerprint probes are alert-only and must not mutate route health")
		for _, stored := range evalRepo.runs[0].Samples {
			require.NotContains(t, stored.NormalizedAnswer, "Bearer")
			require.NotContains(t, stored.NormalizedAnswer, "sk-")
		}
	})

	t.Run("active lease blocks duplicate run", func(t *testing.T) {
		svc, evalRepo, _, _ := newHarness("21")
		evalRepo.leaseHeld = true
		_, err := svc.Run(context.Background(), OpenAIEvalRunRequest{AccountID: 51, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4"}, 8, "manual")
		require.ErrorContains(t, err, "already running")
		require.Empty(t, evalRepo.runs)
	})

	t.Run("Candy transport failures remain alert only when effects are enabled", func(t *testing.T) {
		t.Cleanup(func() { SetOpenAIEvalEffectsEnabled(false) })
		SetOpenAIEvalEffectsEnabled(true)
		svc, _, healthRepo, upstream := newHarness("21")
		upstream.response = nil
		upstream.err = errors.New("timeout while contacting evaluation upstream")
		run, err := svc.Run(context.Background(), OpenAIEvalRunRequest{AccountID: 51, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4", ReasoningEffort: "high"}, 8, "scheduled")
		require.NoError(t, err)
		require.Equal(t, "insufficient", run.Status)
		require.Equal(t, "alert_only", run.Outcome.Scheduling)
		require.Nil(t, healthRepo.updatedExtra, "diagnostic probe transport failures must not change production route eligibility")
	})
}

func TestResetOpenAIBPSStateIsExplicitAndAudited(t *testing.T) {
	account := &Account{
		ID:       77,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			openAIBPSModelStateKey("gpt-6-astra"): OpenAIBPSModelState{
				Active:         false,
				DegradedStreak: 3,
				DisabledReason: "upstream_403",
				UpdatedAt:      time.Now().UTC().Add(-time.Hour),
			},
		},
	}
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	repo := &openAIEvalRepoFake{}
	svc := NewOpenAIEvalService(repo, accounts, nil)

	state, err := svc.ResetOpenAIBPSState(context.Background(), account.ID, "gpt-6-astra", 9)
	require.NoError(t, err)
	require.False(t, state.Active)
	require.Empty(t, state.DisabledReason)
	require.Zero(t, state.DegradedStreak)
	require.NotZero(t, state.UpdatedAt)

	stored, ok := accounts.updatedExtra[OpenAIBPSAccountStateExtraKey()].(OpenAIBPSModelState)
	require.True(t, ok)
	require.Empty(t, stored.DisabledReason)
	require.Len(t, repo.audit, 1)
	require.Equal(t, "bps_state_reset", repo.audit[0].Action)
	require.Equal(t, int64(77), repo.audit[0].Payload["account_id"])
	require.Equal(t, "gpt-6-astra", repo.audit[0].Payload["requested_model"])
}
