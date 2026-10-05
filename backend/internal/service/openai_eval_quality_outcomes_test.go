package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func addQualityEvidence(t *testing.T, account *Account, evidence OpenAIEvalQualityAggregate) {
	t.Helper()
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	for key, value := range qualityTestAccount(t, evidence).Extra {
		account.Extra[key] = value
	}
}

func qualityOutcomeEvidence(now time.Time, accountID int64, testType string, samples int, passed bool) OpenAIEvalQualityAggregate {
	evidence := qualityTestAggregate(now, accountID, samples, 0, 0)
	evidence.TestType = testType
	evidence.AttributionRuleVersion = openAIEvalQualityAttributionRuleVersion(testType)
	if passed {
		if testType == OpenAIEvalTypeCandy {
			evidence.PassCount = samples
		} else {
			evidence.SuspectedPassCount = samples
		}
	}
	evidence.OutcomeStatus, _ = evidence.diagnosticStatus()
	return evidence
}

func TestOpenAIEvalQualityEqualDiagnosticOutcomesAndCompletionOrder(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		candySamples int
		candyPass    bool
		fingerprint  bool
		tracePass    bool
		wantCount    int
		wantPass     int
		wantSuspect  int
		wantRatio    float64
	}{
		{"one_candy_and_trace_warning", 1, true, false, false, 2, 1, 0, .5},
		{"ten_candy_and_trace_warning", 10, true, false, false, 2, 1, 0, .5},
		{"ten_candy_warning_fingerprint_normal_trace_luna", 10, false, true, false, 3, 0, 1, 1.0 / 3},
		{"candy_and_trace_normal", 10, true, false, true, 2, 1, 1, 1},
	} {
		for _, order := range [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
			t.Run(fmt.Sprintf("%s/%v", scenario.name, order), func(t *testing.T) {
				s, repo, accounts := setupQualityRefreshTest(t)
				schedule := repo.config.Accounts[0].CandySchedule
				repo.config.Accounts[0].ModelTraceSchedule = schedule
				if scenario.fingerprint {
					repo.config.Accounts[0].FingerprintSchedule = schedule
				}
				repo.config.Accounts[0].StateProbeSchedule = schedule
				accounts.accounts[17].Extra = make(map[string]any)
				now := time.Now().Add(-time.Minute)
				evidence := []OpenAIEvalQualityAggregate{
					qualityOutcomeEvidence(now, 17, OpenAIEvalTypeCandy, scenario.candySamples, scenario.candyPass),
					qualityOutcomeEvidence(now, 17, OpenAIEvalTypeFingerprint, 400, true),
					qualityOutcomeEvidence(now, 17, OpenAIEvalTypeModelTrace, 3, scenario.tracePass),
				}
				completed := 0
				for _, i := range order {
					addQualityEvidence(t, accounts.accounts[17], evidence[i])
					if i != 1 || scenario.fingerprint {
						completed++
					}
					result, err := s.RefreshOpenAIEvalQuality(context.Background(), 42)
					require.NoError(t, err)
					got, known := openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
					if completed != scenario.wantCount {
						require.False(t, known, "incomplete selected set must be unknown")
						require.Zero(t, result.RouteCount)
						continue
					}
					require.True(t, known)
					require.Equal(t, scenario.wantCount, got.EvaluatedCount)
					require.Equal(t, scenario.wantPass, got.PassCount)
					require.Equal(t, scenario.wantSuspect, got.SuspectedPassCount)
					require.Equal(t, scenario.wantRatio, got.Ratio())
					require.Equal(t, 1, result.RouteCount)
				}
			})
		}
	}
}

func TestOpenAIEvalQualitySelectedTypesRequireFreshValidEvidence(t *testing.T) {
	for _, invalid := range []string{"missing", "expired", "old_contract", "unknown_source", "insufficient", "error", "contradictory_status", "partial_identity", "wrong_model", "wrong_effort"} {
		t.Run(invalid, func(t *testing.T) {
			s, repo, accounts := setupQualityRefreshTest(t)
			repo.config.Accounts[0].ModelTraceSchedule = repo.config.Accounts[0].CandySchedule
			accounts.accounts[17].Extra = make(map[string]any)
			now := time.Now().Add(-time.Minute)
			addQualityEvidence(t, accounts.accounts[17], qualityOutcomeEvidence(now, 17, OpenAIEvalTypeCandy, 10, true))
			trace := qualityOutcomeEvidence(now, 17, OpenAIEvalTypeModelTrace, 3, true)
			switch invalid {
			case "expired":
				trace.ExpiresAt = time.Now().Add(-time.Second)
			case "old_contract":
				trace.DataVersion = "candy-29-v2"
			case "unknown_source":
				trace.TriggerSource = "imported"
			case "insufficient", "error":
				trace.OutcomeStatus = invalid
			case "contradictory_status":
				trace.OutcomeStatus = "suspected_warning"
			case "partial_identity":
				trace.SuspectedPassCount = 2
			case "wrong_model":
				trace.RequestedModel = "gpt-6-sol"
			case "wrong_effort":
				trace.ReasoningEffort = "low"
			}
			if invalid != "missing" {
				addQualityEvidence(t, accounts.accounts[17], trace)
			}
			result, err := s.RefreshOpenAIEvalQuality(context.Background(), 42)
			require.NoError(t, err)
			require.Zero(t, result.RouteCount)
			_, known := openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
			require.False(t, known)
			// Disabling the unavailable diagnostic changes the selected set.
			repo.config.Accounts[0].ModelTraceSchedule.Enabled = false
			repo.config.Revision++
			_, err = s.RefreshOpenAIEvalQuality(context.Background(), 42)
			require.NoError(t, err)
			got, known := openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
			require.True(t, known)
			require.Equal(t, 1, got.EvaluatedCount)
			require.Equal(t, 1.0, got.Ratio())
		})
	}
}

func TestOpenAIEvalQualityExpiryCannotShrinkSelectedDenominator(t *testing.T) {
	s, repo, accounts := setupQualityRefreshTest(t)
	repo.config.Accounts[0].ModelTraceSchedule = repo.config.Accounts[0].CandySchedule
	now := time.Now().Add(-time.Minute)
	addQualityEvidence(t, accounts.accounts[17], qualityOutcomeEvidence(now, 17, OpenAIEvalTypeCandy, 10, true))
	trace := qualityOutcomeEvidence(now, 17, OpenAIEvalTypeModelTrace, 3, false)
	trace.ExpiresAt = time.Now().Add(time.Minute)
	addQualityEvidence(t, accounts.accounts[17], trace)
	_, err := s.RefreshOpenAIEvalQuality(context.Background(), 42)
	require.NoError(t, err)
	got, known := openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
	require.True(t, known)
	require.Equal(t, .5, got.Ratio())
	_, known = openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", trace.ExpiresAt)
	require.False(t, known, "expiry cannot change 1/2 into 1/1")
	require.Empty(t, openAIEvalQualitySnapshots.entries)
}

func TestOpenAIEvalQualityOperationalRunsRetainOnlyFreshPriorEvidence(t *testing.T) {
	enableQualityEffects(t)
	now := time.Now().Add(-time.Minute)
	prior := qualityOutcomeEvidence(now, 17, OpenAIEvalTypeCandy, 10, true)
	account := qualityTestAccount(t, prior)
	accounts := &qualityAccountRepository{account: account}
	s := &OpenAIEvalService{accounts: accounts, repo: &qualityLeaseRepository{}}
	for _, status := range []string{"error", "insufficient", "cancelled"} {
		run := &OpenAIEvalRun{AccountID: 17, TestType: OpenAIEvalTypeCandy, RequestedModel: prior.RequestedModel, ReasoningEffort: prior.ReasoningEffort,
			DataVersion: prior.DataVersion, TriggerSource: "scheduled", Status: status, FinishedAt: time.Now()}
		require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 2, run, OpenAIEvalQualityCounts{1, 0, 0}))
	}
	require.Zero(t, accounts.writes)
	got, ok := readOpenAIEvalQualityEvidence(account, prior.RequestedModel, prior.ReasoningEffort, prior.TestType, time.Now())
	require.True(t, ok)
	require.True(t, prior.ExpiresAt.Equal(got.ExpiresAt))
	_, ok = readOpenAIEvalQualityEvidence(account, prior.RequestedModel, prior.ReasoningEffort, prior.TestType, prior.ExpiresAt)
	require.False(t, ok)
}

func TestOpenAIEvalQualityLatestFailedResultInvalidatesPriorPassAndOrdersMarkers(t *testing.T) {
	for _, passed := range []bool{true, false} {
		for _, failureStatus := range []string{"insufficient", "error", "cancelled"} {
			t.Run(fmt.Sprintf("passed_%v_to_%s", passed, failureStatus), func(t *testing.T) {
				testQualityLatestFailedResultInvalidates(t, passed, failureStatus)
			})
		}
	}
}

func testQualityLatestFailedResultInvalidates(t *testing.T, passed bool, failureStatus string) {
	t.Helper()
	s, _, accounts := setupQualityRefreshTest(t)
	SetOpenAIEvalEffectsEnabled(true)
	t.Cleanup(func() { SetOpenAIEvalEffectsEnabled(false) })
	now := time.Now().Add(-time.Minute)
	prior := qualityOutcomeEvidence(now, 17, OpenAIEvalTypeCandy, 10, passed)
	addQualityEvidence(t, accounts.accounts[17], prior)
	_, err := s.RefreshOpenAIEvalQuality(t.Context(), 42)
	require.NoError(t, err)
	assessment, known := openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
	require.True(t, known)
	wantRatio := 0.0
	if passed {
		wantRatio = 1
	}
	require.Equal(t, wantRatio, assessment.Ratio())
	failed := &OpenAIEvalRun{AccountID: 17, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high",
		DataVersion: OpenAIEvalQualityDataVersion, TriggerSource: "scheduled", Status: failureStatus, Error: "server_error", FinishedAt: now.Add(time.Second)}
	require.NoError(t, s.recordOpenAIEvalQualityResult(t.Context(), prior.RunID+1, failed))
	result, err := s.RefreshOpenAIEvalQuality(t.Context(), 42)
	require.NoError(t, err)
	require.Zero(t, result.RouteCount)
	_, known = openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
	require.False(t, known)
	marker, found := readOpenAIEvalQualityRecord(accounts.accounts[17], failed.RequestedModel, failed.ReasoningEffort, failed.TestType)
	require.True(t, found)
	require.Equal(t, "unknown", marker.OutcomeStatus)
	older := *failed
	older.Status, older.Error, older.FinishedAt = "pass", "", now
	require.NoError(t, s.recordOpenAIEvalQuality(t.Context(), prior.RunID, &older, OpenAIEvalQualityCounts{1, 1, 0}))
	marker, found = readOpenAIEvalQualityRecord(accounts.accounts[17], failed.RequestedModel, failed.ReasoningEffort, failed.TestType)
	require.True(t, found)
	require.Equal(t, "unknown", marker.OutcomeStatus, "older success must not restore stale evidence")
	older.FinishedAt = now.Add(2 * time.Second)
	require.NoError(t, s.recordOpenAIEvalQuality(t.Context(), prior.RunID+2, &older, OpenAIEvalQualityCounts{1, 1, 0}))
	result, err = s.RefreshOpenAIEvalQuality(t.Context(), 42)
	require.NoError(t, err)
	require.Equal(t, 1, result.RouteCount)
}
