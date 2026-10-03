//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type evalTransportStub struct {
	calls   atomic.Int32
	respond func(*http.Request, int) (*http.Response, error)
}

func (*evalTransportStub) SupportsSingleSend() bool { return true }

func (s *evalTransportStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return s.respond(req, int(s.calls.Add(1)))
}
func (s *evalTransportStub) DoWithTLS(req *http.Request, p string, id int64, c int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, p, id, c)
}

type evalCloseBody struct {
	io.Reader
	close  func() error
	closed atomic.Bool
}

func (b *evalCloseBody) Close() error {
	if b.closed.Swap(true) || b.close == nil {
		return nil
	}
	return b.close()
}

func evalCompletedJSON(answer string) string {
	text, _ := json.Marshal(answer)
	return `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":` + string(text) + `}]}],"usage":{"input_tokens":7,"output_tokens":2}}`
}

func evalOAuthHarness(upstream HTTPUpstream) (*AccountTestService, *OpenAIEvalTarget) {
	a := &Account{ID: 991, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "eval-mock-secret"}}
	return &AccountTestService{httpUpstream: upstream, openaiGatewayService: &OpenAIGatewayService{}, tlsFPProfileService: &TLSFingerprintProfileService{}},
		&OpenAIEvalTarget{Account: a, Credential: a, RequestedModel: "gpt-6-astra", UpstreamModel: "gpt-6-astra"}
}

func TestEvalOAuthResponseTerminals(t *testing.T) {
	for _, tc := range []struct {
		name, body, code, answer, message string
		retry                             bool
	}{
		{name: "completed output", body: `data: {"type":"response.completed","response":` + evalCompletedJSON("21") + "}\n\n", answer: "21"},
		{name: "delta and multiline terminal", body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\r\n\r\nevent: response.completed\r\ndata: {\"response\":\r\ndata: {\"status\":\"completed\",\"usage\":{\"input_tokens\":7,\"output_tokens\":2}}}\r\n\r\n", answer: "21"},
		{name: "done item alone", body: "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"21\"}]}}\n\n", code: "missing_terminal", retry: true},
		{name: "DONE is not completed", body: "data: [DONE]\n\n", code: "missing_terminal", retry: true},
		{name: "failed code", body: "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"capacity busy\"}}}\n\n", code: "server_is_overloaded", message: "capacity busy", retry: true},
		{name: "failed no code", body: "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n", code: "response_failed"},
		{name: "incomplete", body: "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n", code: "max_output_tokens", message: "max_output_tokens"},
		{name: "top level error", body: "data: {\"type\":\"error\",\"code\":\"invalid_api_key\",\"message\":\"authentication denied\"}\n\n", code: "invalid_api_key", message: "authentication denied"},
		{name: "nested error", body: "data: {\"type\":\"error\",\"error\":{\"code\":\"model_not_found\",\"message\":\"unknown model\"}}\n\n", code: "model_not_found", message: "unknown model"},
		{name: "refusal", body: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"refusal\",\"refusal\":\"cannot answer\"}]}]}}\n\n", code: "refusal", message: "cannot answer"},
		{name: "empty", body: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n", code: "empty_output"},
		{name: "mismatched status", body: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"failed\"}}\n\n", code: "response_failed"},
		{name: "missing response", body: "data: {\"type\":\"response.completed\"}\n\n", code: "invalid_response"},
		{name: "malformed", body: "data: {not json}\n\n", code: "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &evalCloseBody{Reader: strings.NewReader(tc.body)}
			upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
			}}
			svc, target := evalOAuthHarness(upstream)
			result, err := svc.RunOpenAIEvalSample(t.Context(), target, "probe", "high")
			require.True(t, body.closed.Load())
			if tc.code == "" {
				require.NoError(t, err)
				require.Equal(t, tc.answer, result.Text)
				require.EqualValues(t, 7, result.InputTokens)
				require.EqualValues(t, 2, result.OutputTokens)
			} else {
				var failure *OpenAIEvalRequestError
				require.ErrorAs(t, err, &failure)
				require.Equal(t, tc.code, failure.Code)
				require.Equal(t, tc.retry, failure.Retryable)
				require.Equal(t, 200, failure.HTTPStatus)
				if tc.message != "" {
					require.Contains(t, failure.Message, tc.message)
				}
			}
		})
	}
}

func TestEvalOAuthUsesTokenProviderAndOverrides(t *testing.T) {
	upstream := &evalTransportStub{respond: func(req *http.Request, _ int) (*http.Response, error) {
		require.Equal(t, "Bearer cached-eval-secret", req.Header.Get("Authorization"))
		require.Equal(t, "en-US,en;q=0.9", req.Header.Get("Accept-Language"))
		require.Equal(t, "chat-account", req.Header.Get("chatgpt-account-id"))
		return newJSONResponse(200, "data: {\"type\":\"response.completed\",\"response\":"+evalCompletedJSON("21")+"}\n\n"), nil
	}}
	svc, target := evalOAuthHarness(upstream)
	target.Credential.Credentials["chatgpt_account_id"] = "chat-account"
	target.Credential.Credentials[credKeyHeaderOverrideEnabled] = true
	target.Credential.Credentials[credKeyHeaderOverrides] = map[string]any{"accept-language": "zh-CN"}
	cache := newOpenAITokenCacheStub()
	cache.tokens[OpenAITokenCacheKey(target.Credential)] = "cached-eval-secret"
	svc.openaiGatewayService.openAITokenProvider = NewOpenAITokenProvider(nil, cache, nil)
	_, err := svc.RunOpenAIEvalSample(t.Context(), target, "probe", "")
	require.NoError(t, err)
	require.EqualValues(t, 1, atomic.LoadInt32(&cache.getCalled))
}

func TestEvalAPIKeyAcceptLanguageEnforcedAfterOverrides(t *testing.T) {
	svc, _, upstream := evalRunHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
		require.Equal(t, "en-US,en;q=0.9", req.Header.Get("Accept-Language"))
		require.Equal(t, "yes", getHeaderRaw(req.Header, "x-eval-custom"))
		return newJSONResponse(200, evalCompletedJSON("21")), nil
	})
	account, err := svc.accounts.GetByID(t.Context(), 995)
	require.NoError(t, err)
	account.Credentials[credKeyHeaderOverrideEnabled] = true
	account.Credentials[credKeyHeaderOverrides] = map[string]any{"accept-language": "zh-CN", "x-eval-custom": "yes"}
	_, err = svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4"}, 1, "manual")
	require.NoError(t, err)
	require.EqualValues(t, 1, upstream.calls.Load())
}

func TestEvalResponseErrorsAreBoundedAndSanitized(t *testing.T) {
	message := "denied eval-mock-secret https://user:pass@upstream.invalid/path?secret=value Bearer arbitrary-token\nCookie: opaque-cookie\n" + strings.Repeat("x", 3000)
	raw, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "invalid_api_key", "message": message}})
	upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) { return newJSONResponse(401, string(raw)), nil }}
	svc, target := evalOAuthHarness(upstream)
	_, record, err := svc.runOpenAIEvalSampleAttempts(t.Context(), target, "probe", "", 3)
	require.Error(t, err)
	require.EqualValues(t, 1, upstream.calls.Load())
	require.Equal(t, 1, record.Attempts)
	require.Equal(t, 401, record.HTTPStatus)
	require.Contains(t, record.ErrorMessage, "denied")
	require.LessOrEqual(t, len(record.ErrorMessage), openAIEvalErrorLimit)
	stored, _ := json.Marshal(record)
	for _, secret := range []string{"eval-mock-secret", "arbitrary-token", "opaque-cookie", "user:pass", "secret=value"} {
		require.NotContains(t, string(stored), secret)
	}
	for _, tc := range []struct {
		status int
		code   string
	}{{400, "unsupported_parameter"}, {401, "server_error"}, {403, "server_error"}, {429, "insufficient_quota"}, {500, "invalid_request_error"}} {
		require.False(t, newOpenAIEvalRequestError(tc.code, "failure", tc.status).Retryable)
	}
}

func TestEvalCancellationClosesBodyAndStopsRetries(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	body := &evalCloseBody{Reader: r, close: r.Close}
	started := make(chan struct{})
	upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) {
		close(started)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
	}}
	svc, target := evalOAuthHarness(upstream)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan OpenAIEvalSampleRecord, 1)
	go func() { _, record, _ := svc.runOpenAIEvalSampleAttempts(ctx, target, "probe", "", 3); done <- record }()
	<-started
	cancel()
	select {
	case record := <-done:
		require.Equal(t, 1, record.Attempts)
		require.Equal(t, "cancelled", record.ErrorCode)
		require.True(t, body.closed.Load())
		require.EqualValues(t, 1, upstream.calls.Load())
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock response reading")
	}
	_, record, err := svc.runOpenAIEvalSampleAttempts(ctx, target, "probe", "", 3)
	require.Error(t, err)
	require.Zero(t, record.Attempts)
	require.EqualValues(t, 1, upstream.calls.Load())
}

func TestEvalResponseBoundAndAnswerRedaction(t *testing.T) {
	upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, "data: "+strings.Repeat("x", openAIEvalResponseLimit+10)), nil
	}}
	svc, target := evalOAuthHarness(upstream)
	_, err := svc.RunOpenAIEvalSample(t.Context(), target, "probe", "")
	require.Equal(t, "response_too_large", safeOpenAIEvalErrorCode(err))
	upstream.respond = func(*http.Request, int) (*http.Response, error) {
		resp := newJSONResponse(200, "data: {\"type\":\"response.completed\",\"response\":"+evalCompletedJSON("21 eval-mock-secret response-cookie "+strings.Repeat("z", 20000))+"}\n\n")
		resp.Header.Set("Set-Cookie", "routing=response-cookie")
		return resp, nil
	}
	answer, record, err := svc.runOpenAIEvalSampleAttempts(t.Context(), target, "probe", "", 3)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(answer.Text, "21"))
	require.LessOrEqual(t, len(record.Answer), openAIEvalAnswerLimit)
	require.NotContains(t, record.Answer, "eval-mock-secret")
	require.NotContains(t, record.Answer, "response-cookie")
}

func TestEvalCandyPreservesFullAnswerForAnnotation(t *testing.T) {
	for _, extracted := range []bool{false, true} {
		t.Run(fmt.Sprint(extracted), func(t *testing.T) {
			answer := strings.Repeat("推导内容。", 4000) + "\n未确认结论 eval-key"
			if extracted {
				answer += "\n最终答案：21颗。"
			}
			svc, repo, _ := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
				return newJSONResponse(200, evalCompletedJSON(answer)), nil
			})
			run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4"}, 1, "manual")
			require.NoError(t, err)
			require.Len(t, run.Samples, 1)
			stored := run.Samples[0]
			require.Equal(t, strings.ReplaceAll(answer, "eval-key", "[redacted]"), stored.Answer)
			require.Greater(t, len(stored.Answer), openAIEvalAnswerLimit)
			require.Equal(t, stored.Answer, repo.runs[0].Samples[0].Answer)
			if extracted {
				require.Equal(t, "21", stored.NormalizedAnswer)
				require.Equal(t, "pass", run.Status)
			} else {
				require.Empty(t, stored.NormalizedAnswer)
				require.Equal(t, "warning", run.Status)
			}
		})
	}
}

func evalRunHarness(t *testing.T, respond func(*http.Request, int) (*http.Response, error)) (*OpenAIEvalService, *openAIEvalRepoFake, *evalTransportStub) {
	t.Helper()
	a := newCodexModelsAPIKeyTestAccount("https://eval.example/v1")
	a.ID = 995
	a.Credentials["api_key"] = "eval-key"
	a.Extra = map[string]any{"openai_responses_supported": true}
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{a.ID: a}}}
	models := &codexModelsHTTPUpstreamStub{do: func(*http.Request, string, int64, int) (*http.Response, error) {
		return ordinaryModelsUpstreamResponse(`{"data":[{"id":"gpt-5.4"}]}`), nil
	}}
	upstream := &evalTransportStub{respond: respond}
	at := &AccountTestService{accountRepo: accounts, httpUpstream: upstream, cfg: testConfig(), openaiGatewayService: newCodexModelsAPIKeyTestService(models), tlsFPProfileService: &TLSFingerprintProfileService{}}
	repo := &openAIEvalRepoFake{}
	return NewOpenAIEvalService(repo, accounts, at), repo, upstream
}

func TestEvalModelTracePhysicalAttemptCapAndEvidence(t *testing.T) {
	svc, repo, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(503, `{"error":{"code":"server_error","message":"capacity unavailable"}}`), nil
	})
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeModelTrace, RequestedModel: "gpt-5.4"}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "insufficient", run.Status)
	require.EqualValues(t, 9, upstream.calls.Load())
	require.Equal(t, 9, run.RequestCount)
	require.Equal(t, 3, run.CompletedSamples)
	require.Equal(t, 3, run.Outcome.ExpectedCount)
	require.NotNil(t, run.Outcome.ModelTrace)
	require.Len(t, run.Outcome.ModelTrace.Samples, 3)
	require.Len(t, repo.runs[0].Samples, 3)
	require.Equal(t, "server_error", run.Error)
	for _, sample := range run.Samples {
		require.Equal(t, 3, sample.Attempts)
		require.Len(t, sample.AttemptErrors, 3)
		require.Equal(t, 503, sample.HTTPStatus)
		require.Contains(t, sample.ErrorMessage, "capacity unavailable")
	}
}

func TestEvalManualAndScheduledAttemptSettingsAndLogicalCounts(t *testing.T) {
	for _, source := range []string{"manual", "scheduled"} {
		t.Run(source, func(t *testing.T) {
			svc, repo, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
				return newJSONResponse(503, `{"error":{"code":"server_error","message":"busy"}}`), nil
			})
			repo.config = &OpenAIEvalConfig{MaxRequestAttempts: 2}
			request := OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4", SampleCount: 2}
			run, err := svc.Run(t.Context(), request, 1, source)
			require.NoError(t, err)
			require.Equal(t, 4, run.RequestCount)
			require.Equal(t, 2, run.CompletedSamples)
			require.Equal(t, 2, run.Outcome.ExpectedCount)
			require.Equal(t, "server_error", run.Error)
			one := 1
			request.MaxAttempts = &one
			run, err = svc.Run(t.Context(), request, 1, source)
			require.NoError(t, err)
			require.Equal(t, 2, run.RequestCount)
			require.EqualValues(t, 6, upstream.calls.Load())
		})
	}
	svc, _, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, evalCompletedJSON("29")), nil
	})
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4", SampleCount: 2}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "warning", run.Status)
	require.Equal(t, 2, run.Outcome.SampleCount)
	require.EqualValues(t, 2, upstream.calls.Load())
	require.True(t, run.Samples[0].Valid)
	require.Equal(t, "29", run.Samples[0].Answer)
}

func TestEvalFingerprintAndModelTraceDoNotRetryInvalidAnswers(t *testing.T) {
	for _, kind := range []string{OpenAIEvalTypeFingerprint, OpenAIEvalTypeModelTrace} {
		t.Run(kind, func(t *testing.T) {
			svc, _, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
				return newJSONResponse(200, evalCompletedJSON("invalid answer")), nil
			})
			run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: kind, RequestedModel: "gpt-5.4", SampleMode: "quick"}, 1, "manual")
			require.NoError(t, err)
			require.Equal(t, "insufficient", run.Status)
			want := 3
			if kind == OpenAIEvalTypeFingerprint {
				want = 60
			}
			require.EqualValues(t, want, upstream.calls.Load())
			require.Equal(t, want, run.CompletedSamples)
			for _, sample := range run.Samples {
				require.Equal(t, 1, sample.Attempts)
				if !sample.Valid {
					require.NotEmpty(t, sample.ErrorMessage)
				}
				require.Empty(t, sample.AttemptErrors)
			}
		})
	}
}

func TestEvalUsageIncludesFailedAttemptsAndJSONOrSSE(t *testing.T) {
	svc, _, _ := evalRunHarness(t, func(_ *http.Request, call int) (*http.Response, error) {
		if call == 1 {
			return newJSONResponse(200, `{"status":"failed","error":{"code":"server_error","message":"try again"},"usage":{"input_tokens":3,"output_tokens":1}}`), nil
		}
		return newJSONResponse(200, "data: {\"type\":\"response.completed\",\"response\":"+evalCompletedJSON("21")+"}\n\n"), nil
	})
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-5.4"}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "pass", run.Status)
	require.Equal(t, 2, run.RequestCount)
	require.EqualValues(t, 10, run.InputTokens)
	require.EqualValues(t, 3, run.OutputTokens)
	require.Equal(t, 1, run.Outcome.SampleCount)
	require.Len(t, run.Samples[0].AttemptErrors, 1)
}

func TestEvalRetryDelayCancellationAndDeterministicErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) { cancel(); return nil, errors.New("connection reset") }}
	svc, target := evalOAuthHarness(upstream)
	_, record, err := svc.runOpenAIEvalSampleAttempts(ctx, target, "probe", "", 3)
	require.Error(t, err)
	require.Equal(t, 1, record.Attempts)
	require.EqualValues(t, 1, upstream.calls.Load())
	for _, body := range []string{
		"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"invalid_request_error\",\"message\":\"bad request\"}}}\n\n",
		"data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n",
	} {
		upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) { return newJSONResponse(200, body), nil }}
		svc, target := evalOAuthHarness(upstream)
		_, record, err := svc.runOpenAIEvalSampleAttempts(t.Context(), target, "probe", "", 3)
		require.Error(t, err)
		require.Equal(t, 1, record.Attempts)
		require.EqualValues(t, 1, upstream.calls.Load())
	}
}

func TestEvalSettingsValidationAndStateProbeEligibility(t *testing.T) {
	repo := &openAIEvalRepoFake{}
	a := &Account{ID: 996, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{a.ID: a}}}
	svc := NewOpenAIEvalService(repo, accounts, &AccountTestService{})
	for _, maximum := range []int{0, 1, 10, -1, 11} {
		config := &OpenAIEvalConfig{MaxRequestAttempts: maximum, Accounts: []OpenAIEvalAccountConfig{{AccountID: a.ID, RequestedModel: "gpt-5.4", ReasoningEffort: "xhigh", StateProbeSchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}}}}
		err := svc.SaveConfig(t.Context(), config, 1)
		if maximum < 0 || maximum > 10 {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		if maximum == 0 {
			require.Equal(t, 3, config.MaxRequestAttempts)
		}
		got, err := svc.GetConfig(t.Context())
		require.NoError(t, err)
		require.True(t, got.Accounts[0].DirectOAuthEligible)
		require.Equal(t, "xhigh", got.Accounts[0].ReasoningEffort)
		require.True(t, got.Accounts[0].StateProbeSchedule.Enabled)
	}
	for _, maximum := range []int{0, -1, 11} {
		_, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: a.ID, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeCandy, MaxAttempts: &maximum}, 1, "manual")
		require.ErrorContains(t, err, "max_attempts")
	}
	three := 3
	_, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: a.ID, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeStateProbe, MaxAttempts: &three}, 1, "manual")
	require.ErrorContains(t, err, "retries are unsupported")
	a.Type = AccountTypeAPIKey
	config := &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{AccountID: a.ID, RequestedModel: "gpt-5.4", ReasoningEffort: "high", BPSMode: OpenAIEvalBPSModeAuto}}}
	require.NoError(t, svc.SaveConfig(t.Context(), config, 1), "retained legacy BPS flags must not gate an unrelated route")
	config.Accounts[0].StateProbeSchedule = OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	require.ErrorContains(t, svc.SaveConfig(t.Context(), config, 1), "direct OpenAI OAuth")
}

func TestEvalHistoryPreservesCandy29Evidence(t *testing.T) {
	old := &OpenAIEvalRun{ID: 1, TestType: OpenAIEvalTypeCandy, DataVersion: "sub2api-candy-29-v2-cpa-fingerprint-5654020c", Status: "pass", Outcome: OpenAIEvalOutcome{Status: "pass", Score: 1}, Samples: []OpenAIEvalSampleRecord{{ProbeID: "candy-29-v2-1", NormalizedAnswer: "29", Valid: true}}}
	repo := &openAIEvalRepoFake{runs: []*OpenAIEvalRun{old}}
	before, _ := json.Marshal(old)
	svc := NewOpenAIEvalService(repo, nil, nil)
	got, err := svc.ListRuns(t.Context(), OpenAIEvalRunFilter{})
	require.NoError(t, err)
	after, _ := json.Marshal(got[0])
	require.JSONEq(t, string(before), string(after))
	require.NotContains(t, OpenAIEvalCandyPrompt, "21")
	require.Contains(t, OpenAIEvalCandyPrompt, "现已知不同口味的糖和不同形状的数量统计如下表")
}

func TestEvalCancellationDuringRetryDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	read := make(chan struct{})
	body := &evalCloseBody{Reader: strings.NewReader(`{"error":{"code":"server_error","message":"busy"}}`), close: func() error { close(read); return nil }}
	upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: body}, nil
	}}
	svc, target := evalOAuthHarness(upstream)
	done := make(chan OpenAIEvalSampleRecord, 1)
	go func() { _, record, _ := svc.runOpenAIEvalSampleAttempts(ctx, target, "probe", "", 3); done <- record }()
	<-read
	cancel()
	select {
	case record := <-done:
		require.Equal(t, 1, record.Attempts)
		require.Contains(t, record.AttemptErrors[0].Message, "busy")
	case <-time.After(time.Second):
		t.Fatal("retry delay ignored cancellation")
	}
	require.EqualValues(t, 1, upstream.calls.Load())
}

func TestEvalStateProbeRejectionKeepsDetailsAndNeverRetries(t *testing.T) {
	upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(429, `{"error":{"code":"rate_limit_exceeded","message":"retry later eval-mock-secret"}}`), nil
	}}
	svc, target := evalOAuthHarness(upstream)
	result := svc.RunOpenAIStateProbe(t.Context(), target)
	require.EqualValues(t, 1, upstream.calls.Load())
	require.Equal(t, 1, result.RequestCount)
	require.Equal(t, "unsupported_linked_ticket_chain", result.RetryPolicy)
	require.Equal(t, "rate_limited", result.Failure)
	require.Len(t, result.Samples, 1)
	require.Equal(t, "rate_limit_exceeded", result.Samples[0].ErrorCode)
	require.Contains(t, result.Samples[0].ErrorMessage, "retry later")
	require.NotContains(t, result.Samples[0].ErrorMessage, "eval-mock-secret")
}
