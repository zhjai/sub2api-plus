//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/cpaeval"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIEvalCPAFingerprintBoundedInvalidRetry(t *testing.T) {
	svc, _, transport := evalRunHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.False(t, gjson.GetBytes(body, "reasoning.effort").Exists())
		return newJSONResponse(200, evalCompletedJSON("not enough numbers")), nil
	})
	maximum := 2
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", ReasoningEffort: "high", TestType: OpenAIEvalTypeFingerprint, SampleMode: "ignored", MaxAttempts: &maximum}, 1, "manual")
	require.NoError(t, err)
	require.EqualValues(t, 6, transport.calls.Load())
	require.Equal(t, 6, run.RequestCount)
	require.Equal(t, "high", run.ReasoningEffort)
	require.Equal(t, "insufficient", run.Status)
	require.Equal(t, 3, run.CompletedSamples)
	require.Equal(t, 3, run.Outcome.LMFingerprint.Concurrency)
	require.EqualValues(t, 42, run.InputTokens)
	require.EqualValues(t, 12, run.OutputTokens)
	for _, sample := range run.Samples {
		require.Equal(t, 2, sample.Attempts)
		require.Len(t, sample.AttemptErrors, 2)
		require.Equal(t, "invalid_fingerprint_answer", sample.ErrorCode)
	}
}

func TestOpenAIEvalCPAAcceptRetryUsesSingleBudget(t *testing.T) {
	svc, _, transport := evalRunHarness(t, func(_ *http.Request, n int) (*http.Response, error) {
		if n == 1 {
			return newJSONResponse(503, `{"error":{"code":"server_error","message":"busy"}}`), nil
		}
		if n == 2 {
			return newJSONResponse(200, evalCompletedJSON("short")), nil
		}
		return newJSONResponse(200, evalCompletedJSON(strings.Repeat("42,", 350))), nil
	})
	target, err := svc.accountTest.ResolveOpenAIEvalTarget(t.Context(), 995, "gpt-5.4")
	require.NoError(t, err)
	ctx := withOpenAIEvalAccept(t.Context(), func(text string) error { _, _, err := cpaeval.FingerprintText(text, 300); return err })
	result, record, err := svc.accountTest.RunOpenAIEvalSampleAttempts(ctx, target, "challenge", "", 3)
	require.NoError(t, err)
	require.EqualValues(t, 3, transport.calls.Load())
	require.Equal(t, 3, record.Attempts)
	require.Len(t, record.AttemptErrors, 2)
	require.True(t, record.Valid)
	require.EqualValues(t, 14, result.InputTokens)
}

func TestOpenAIEvalCPAFingerprintVerdictsAndQuality(t *testing.T) {
	for _, tc := range []struct {
		model, prediction, decision, status string
		used                                int
	}{
		{"gpt-6.1-sol", "gpt-6-astra", "partial", "pass", 1},
		{"gpt-6-astra", "gpt-6-astra", "partial", "pass", 2},
		{"gpt-6-astra", "claude-sonnet-4.6", "partial", "warning", 2},
		{"gpt-6-astra", "", "not_model", "warning", 3},
		{"gpt-6-astra", "", "", "insufficient", 0},
		{"unknown-model", "gpt-6-astra", "partial", "insufficient", 2},
	} {
		t.Run(fmt.Sprintf("%s-%s-%d", tc.model, tc.status, tc.used), func(t *testing.T) {
			fp := &OpenAIEvalLMFingerprintResult{DetectorRevision: cpaeval.DetectorRevision(), ComparedAs: cpaeval.FingerprintModelID(tc.model), Enrolled: cpaeval.ModelEnrolled(tc.model), Attribution: cpaeval.FingerprintAttribution{Prediction: tc.prediction, Decision: tc.decision, Used: tc.used}}
			outcome := openAIEvalLMFingerprintOutcome(tc.model, fp)
			require.Equal(t, tc.status, outcome.Status)
			run := &OpenAIEvalRun{TestType: OpenAIEvalTypeFingerprint, RequestedModel: tc.model, Status: tc.status, Outcome: outcome, DataVersion: OpenAIEvalQualityDataVersion, BaselineVersion: OpenAIEvalQualityBaselineVersion}
			counts := openAIEvalCurrentQualityCountsFromRun(run)
			if tc.status == "insufficient" {
				require.Zero(t, counts.EvaluatedCount)
			} else {
				require.Equal(t, tc.used, counts.EvaluatedCount)
			}
			if tc.status == "pass" {
				require.Equal(t, tc.used, counts.PassCount)
			} else {
				require.Zero(t, counts.PassCount)
			}
			run.DataVersion = "sub2api-candy-21-v4-number-presence-cpa-97623969-modeltrace-97623969"
			require.Zero(t, openAIEvalCurrentQualityCountsFromRun(run).EvaluatedCount)
		})
	}
}

func TestOpenAIEvalCPANormalizeLegacyConfiguration(t *testing.T) {
	cfg := &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{
		{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", ModelTraceSchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 600, SampleMode: "strict"}, CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 900}},
		{AccountID: 2, RequestedModel: "unknown-model", FingerprintSchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, SampleMode: "invalid"}},
	}}
	NormalizeOpenAIEvalDetectorConfig(cfg)
	require.True(t, cfg.Accounts[0].FingerprintSchedule.Enabled)
	require.Equal(t, 600, cfg.Accounts[0].FingerprintSchedule.IntervalSeconds)
	require.Equal(t, 3, cfg.Accounts[0].FingerprintSchedule.Concurrency)
	require.Equal(t, "high", cfg.Accounts[0].ReasoningEffort)
	require.Equal(t, 900, cfg.Accounts[0].CandySchedule.IntervalSeconds)
	require.False(t, cfg.Accounts[0].ModelTraceSchedule.Enabled)
	require.False(t, cfg.Accounts[1].FingerprintSchedule.Enabled)
	require.Len(t, cfg.Warnings, 1)
	require.NoError(t, validateOpenAIEvalSchedule(&cfg.Accounts[1].FingerprintSchedule, OpenAIEvalTypeFingerprint))
	_, selected := openAIEvalQualityTestInterval(cfg, 2, "unknown-model", "", OpenAIEvalTypeFingerprint)
	require.False(t, selected)
	before, _ := json.Marshal(cfg)
	NormalizeOpenAIEvalDetectorConfig(cfg)
	after, _ := json.Marshal(cfg)
	require.JSONEq(t, string(before), string(after))
}

func TestOpenAIEvalCPAPelicanRunAndIsolation(t *testing.T) {
	markup := `<!doctype html><svg xmlns="http://www.w3.org/2000/svg"><style>.p { fill: red }</style><text>pelican</text></svg>`
	svc, _, transport := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, evalCompletedJSON("```html\n"+markup+"\n```")), nil
	})
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypePelican, Variant: "original"}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "completed", run.Status)
	require.Equal(t, markup, run.PelicanHTML)
	require.Equal(t, len(markup), run.Outcome.Pelican.HTMLBytes)
	require.Len(t, run.Outcome.Pelican.HTMLSHA256, 64)
	require.Empty(t, run.Samples[0].Answer)
	wire, err := json.Marshal(run)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "<!doctype")
	require.True(t, run.DiagnosticOnly)
	require.Zero(t, openAIEvalCurrentQualityCountsFromRun(run).EvaluatedCount)
	require.False(t, shouldRecordOpenAIEvalRouteHealth(OpenAIEvalTypePelican))
	_, err = svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypePelican}, 1, "scheduled")
	require.ErrorContains(t, err, "manual only")
	require.EqualValues(t, 1, transport.calls.Load())
}

func TestOpenAIEvalCPAPelicanLimitsAndTimeouts(t *testing.T) {
	ctx := withOpenAIEvalKind(t.Context(), OpenAIEvalTypePelican)
	require.Equal(t, 10*time.Minute, openAIEvalAttemptTimeoutFor(ctx))
	require.Equal(t, 10*time.Minute, openAIEvalAttemptTimeoutFor(withOpenAIEvalKind(t.Context(), OpenAIEvalTypeCandy)))
	require.Equal(t, 3*time.Minute, openAIEvalAttemptTimeoutFor(t.Context()))
	require.Equal(t, "<svg xmlns=\"http://www.w3.org/2000/svg\"/>", openAIEvalSampleTextFor(ctx, "<svg xmlns=\"http://www.w3.org/2000/svg\"/>"))
	body := "data: " + `{"type":"response.output_text.delta","delta":"` + strings.Repeat("a", 3<<20) + `"}` + "\n\ndata: " + `{"type":"response.completed","response":{"status":"completed"}}` + "\n\n"
	response, err := readOpenAIEvalResponse(ctx, newJSONResponse(200, body), true, true)
	require.NoError(t, err)
	require.Len(t, response.Text, 3<<20)
	_, err = readOpenAIEvalResponse(t.Context(), newJSONResponse(200, body), true, true)
	require.Error(t, err)
}

func TestOpenAIEvalCPAFingerprintGlobalSlotsCancellation(t *testing.T) {
	var inFlight, peak atomic.Int32
	started := make(chan struct{}, 12)
	release := make(chan struct{})
	svc, _, _ := evalRunHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
			return newJSONResponse(200, evalCompletedJSON(strings.Repeat("42,", 350))), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})
	target, err := svc.accountTest.ResolveOpenAIEvalTarget(t.Context(), 995, "gpt-5.4")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run := &OpenAIEvalRun{}
			svc.collectOpenAIEvalFingerprint(withOpenAIEvalKind(ctx, OpenAIEvalTypeFingerprint), target, "gpt-5.4", 3, 1, run, func() {})
		}()
	}
	for range 6 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			cancel()
			wg.Wait()
			t.Fatal("six global slots did not start")
		}
	}
	require.EqualValues(t, 6, peak.Load())
	cancel()
	close(release)
	wg.Wait()
	require.LessOrEqual(t, peak.Load(), int32(6))
	require.Empty(t, openAIEvalFingerprintSlots)
}

func TestOpenAIEvalCPAPelicanInvalidPageRetriesWithinBudget(t *testing.T) {
	svc, _, transport := evalRunHarness(t, func(_ *http.Request, n int) (*http.Response, error) {
		if n == 1 {
			return newJSONResponse(200, evalCompletedJSON("I did not produce a page")), nil
		}
		return newJSONResponse(200, evalCompletedJSON("<html>ok</html>")), nil
	})
	maximum := 2
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypePelican, MaxAttempts: &maximum}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "completed", run.Status)
	require.EqualValues(t, 2, transport.calls.Load())
	require.Equal(t, 2, run.Outcome.Pelican.Attempts)
	require.Len(t, run.Samples[0].AttemptErrors, 1)
	require.EqualValues(t, 14, run.InputTokens)
}

func TestOpenAIEvalCPAPelicanFailedNeverGrades(t *testing.T) {
	svc, _, transport := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, evalCompletedJSON("no markup")), nil
	})
	maximum := 2
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypePelican, MaxAttempts: &maximum}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "failed", run.Status)
	require.Equal(t, "failed", run.Outcome.Status)
	require.EqualValues(t, 2, transport.calls.Load())
	require.Empty(t, run.PelicanHTML)
	require.Equal(t, "invalid_pelican_page", run.Error)
	require.Zero(t, openAIEvalCurrentQualityCountsFromRun(run).EvaluatedCount)
}

func TestOpenAIEvalCPAUnknownAutomaticFingerprintSaveDoesNotReject(t *testing.T) {
	svc, repo, _ := evalRunHarness(t, nil)
	require.False(t, cpaeval.ModelEnrolled("gpt-unenrolled-test"))
	cfg := &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{AccountID: 995, RequestedModel: "gpt-unenrolled-test", FingerprintSchedule: OpenAIEvalSchedule{Enabled: true, SampleMode: "garbage"}}}}
	require.NoError(t, svc.SaveConfig(t.Context(), cfg, 1))
	require.False(t, repo.config.Accounts[0].FingerprintSchedule.Enabled)
	require.NotEmpty(t, cfg.Warnings)
}

func TestOpenAIEvalCPAFingerprintPerRunConcurrency(t *testing.T) {
	var current, peak atomic.Int32
	svc, _, _ := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		n := current.Add(1)
		defer current.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		return newJSONResponse(200, evalCompletedJSON(strings.Repeat("42,", 350))), nil
	})
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeFingerprint, Concurrency: 1}, 1, "manual")
	require.NoError(t, err)
	require.EqualValues(t, 1, peak.Load())
	require.Equal(t, 3, run.RequestCount)
	require.Equal(t, 1, run.Outcome.LMFingerprint.Concurrency)
	for _, n := range []int{-1, 4} {
		_, err := normalizeOpenAIEvalFingerprintConcurrency(n)
		require.Error(t, err)
	}
}

func TestOpenAIEvalCPAFingerprintPartialCollectionAndNoToolEvidence(t *testing.T) {
	svc, _, transport := evalRunHarness(t, func(_ *http.Request, n int) (*http.Response, error) {
		if n == 1 {
			return newJSONResponse(200, evalCompletedJSON(strings.Repeat("42,", 350))), nil
		}
		if n == 2 {
			return newJSONResponse(401, `{"error":{"code":"invalid_api_key","message":"rejected"}}`), nil
		}
		return newJSONResponse(200, `{"status":"completed","output":[{"type":"function_call"},{"type":"message","content":[{"type":"output_text","text":"`+strings.Repeat("42,", 350)+`"}]}]}`), nil
	})
	target, err := svc.accountTest.ResolveOpenAIEvalTarget(t.Context(), 995, "gpt-5.4")
	require.NoError(t, err)
	run := &OpenAIEvalRun{}
	fp := svc.collectOpenAIEvalFingerprint(withOpenAIEvalKind(t.Context(), OpenAIEvalTypeFingerprint), target, "gpt-6-astra", 1, 1, run, func() {})
	require.EqualValues(t, 3, transport.calls.Load())
	require.Equal(t, "partial", fp.CollectionStatus)
	require.Equal(t, 1, fp.Attribution.Used)
	require.True(t, fp.Samples[0].Accepted)
	require.False(t, fp.Samples[1].Accepted)
	require.False(t, fp.Samples[2].Accepted)
	require.Equal(t, "unexpected_tool_call", run.Samples[2].ErrorCode)
	require.Equal(t, "warning", openAIEvalLMFingerprintOutcome("gpt-6-astra", fp).Status)
}
