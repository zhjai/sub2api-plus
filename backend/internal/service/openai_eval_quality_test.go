package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func enableQualityEffects(t *testing.T) {
	t.Helper()
	previous := OpenAIEvalEffectsEnabled()
	SetOpenAIEvalEffectsEnabled(true)
	t.Cleanup(func() { SetOpenAIEvalEffectsEnabled(previous) })
}

func qualityTestAggregate(now time.Time, accountID int64, evaluated, passed, suspected int) OpenAIEvalQualityAggregate {
	return OpenAIEvalQualityAggregate{
		OpenAIEvalQualityCounts: OpenAIEvalQualityCounts{evaluated, passed, suspected},
		Version:                 OpenAIEvalQualityVersion, DataVersion: OpenAIEvalQualityDataVersion,
		AccountID: accountID, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high",
		RunID: 1, TestType: OpenAIEvalTypeCandy, TriggerSource: "scheduled",
		EvaluatedAt: now, ExpiresAt: now.Add(OpenAIEvalQualityTTL),
	}
}

func qualityTestAccount(t *testing.T, quality OpenAIEvalQualityAggregate) *Account {
	t.Helper()
	raw, err := json.Marshal(quality)
	require.NoError(t, err)
	// Exercise the same map representation returned by JSONB decoding.
	var persisted map[string]any
	require.NoError(t, json.Unmarshal(raw, &persisted))
	return &Account{ID: quality.AccountID, Extra: map[string]any{
		OpenAIEvalQualityExtraKeyFor(quality.RequestedModel, quality.ReasoningEffort, quality.TestType): persisted,
	}}
}

func TestOpenAIEvalQualityLogicalCounts(t *testing.T) {
	samples := []OpenAIEvalQualitySample{
		{SampleID: "candy-21-v3-1", Evaluated: true, Status: "pass"},
		{SampleID: "candy-21-v3-2", Evaluated: true, Status: "warning"},
		{SampleID: "identity-1", Evaluated: true, Status: "suspected_normal"},
		{SampleID: "identity-2", Evaluated: true, Status: "suspected_warning"},
		{SampleID: "timeout", Status: "error"},
		{SampleID: "incomplete", Status: "insufficient"},
	}
	counts, err := CountOpenAIEvalQualitySamples(samples)
	require.NoError(t, err)
	require.Equal(t, OpenAIEvalQualityCounts{4, 1, 1}, counts)
	require.Equal(t, 0.5, counts.Ratio())
	_, err = CountOpenAIEvalQualitySamples(append(samples, samples[0]))
	require.ErrorContains(t, err, "unique logical sample")
	_, err = CountOpenAIEvalQualitySamples([]OpenAIEvalQualitySample{{SampleID: "retry", Evaluated: true, Status: "error"}})
	require.Error(t, err)
	for _, counts := range []OpenAIEvalQualityCounts{{}, {-1, 0, 0}, {1, 2, 0}, {1, 1, 1}, {1, -1, 0}, {414, 1, 0}} {
		require.False(t, counts.valid())
	}
}

func TestOpenAIEvalQualityIdentityClassification(t *testing.T) {
	for _, model := range []string{"gpt-5.6-luna", "GPT-6-LUNA", "gpt-6-luna-preview"} {
		require.Equal(t, "suspected_warning", OpenAIEvalIdentityQualityStatus(model))
	}
	for _, model := range []string{"gpt-6-sol", "gpt-6.1-sol", "gpt-6-astra", "claude-opus-5"} {
		require.Equal(t, "suspected_normal", OpenAIEvalIdentityQualityStatus(model))
	}
	require.Equal(t, "insufficient", OpenAIEvalIdentityQualityStatus(" "))
}

func TestOpenAIEvalQualityReadIsolationFreshnessAndMalformed(t *testing.T) {
	enableQualityEffects(t)
	now := time.Now().UTC()
	quality := qualityTestAggregate(now, 17, 10, 7, 0)
	account := qualityTestAccount(t, quality)
	got, ok := ReadOpenAIEvalQualityFromAccount(account, " GPT-6.1-SOL ", "HIGH", now)
	require.True(t, ok)
	require.Equal(t, 0.7, got.Ratio())
	for _, dimension := range [][2]string{{"gpt-6-sol", "high"}, {"gpt-6.1-sol", "low"}, {"gpt-6.1-sol", ""}} {
		_, ok := ReadOpenAIEvalQualityFromAccount(account, dimension[0], dimension[1], now)
		require.False(t, ok)
	}
	_, ok = ReadOpenAIEvalQualityFromAccount(account, "gpt-6.1-sol", "high", now.Add(OpenAIEvalQualityTTL))
	require.False(t, ok)
	for name, change := range map[string]func(*OpenAIEvalQualityAggregate){
		"old_question":   func(q *OpenAIEvalQualityAggregate) { q.DataVersion = "candy-29-v2" },
		"old_schema":     func(q *OpenAIEvalQualityAggregate) { q.Version = "attempts-v0" },
		"wrong_account":  func(q *OpenAIEvalQualityAggregate) { q.AccountID++ },
		"wrong_model":    func(q *OpenAIEvalQualityAggregate) { q.RequestedModel = "gpt-6-sol" },
		"wrong_effort":   func(q *OpenAIEvalQualityAggregate) { q.ReasoningEffort = "low" },
		"unknown_source": func(q *OpenAIEvalQualityAggregate) { q.TriggerSource = "imported" },
		"zero":           func(q *OpenAIEvalQualityAggregate) { q.EvaluatedCount = 0 },
		"overflow":       func(q *OpenAIEvalQualityAggregate) { q.PassCount = 11 },
		"future": func(q *OpenAIEvalQualityAggregate) {
			q.EvaluatedAt = now.Add(time.Minute)
			q.ExpiresAt = q.EvaluatedAt.Add(OpenAIEvalQualityTTL)
		},
		"extended_ttl": func(q *OpenAIEvalQualityAggregate) {
			q.ExpiresAt = q.EvaluatedAt.Add(OpenAIEvalQualityMaxTTL + time.Second)
		},
		"state_probe": func(q *OpenAIEvalQualityAggregate) { q.TestType = OpenAIEvalTypeStateProbe },
	} {
		t.Run(name, func(t *testing.T) {
			bad := quality
			change(&bad)
			account.Extra[OpenAIEvalQualityExtraKeyFor("gpt-6.1-sol", "high")] = bad
			_, ok := ReadOpenAIEvalQualityFromAccount(account, "gpt-6.1-sol", "high", now)
			require.False(t, ok)
		})
	}
	for _, bad := range []any{"bad json", json.RawMessage(`{"evaluated_count":1.5}`), map[string]any{"pass_count": "1"}, nil} {
		account.Extra[OpenAIEvalQualityExtraKeyFor("gpt-6.1-sol", "high")] = bad
		_, ok := ReadOpenAIEvalQualityFromAccount(account, "gpt-6.1-sol", "high", now)
		require.False(t, ok)
	}
	SetOpenAIEvalEffectsEnabled(false)
	_, ok = ReadOpenAIEvalQualityFromAccount(qualityTestAccount(t, quality), "gpt-6.1-sol", "high", now)
	require.False(t, ok)
	require.NotEqual(t, OpenAIEvalQualityExtraKeyFor("a:b", "c"), OpenAIEvalQualityExtraKeyFor("a", "b:c"))
}

type qualityAccountRepository struct {
	AccountRepository
	account *Account
	writes  int
	err     error
}

func (r *qualityAccountRepository) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}
func (r *qualityAccountRepository) UpdateExtra(_ context.Context, _ int64, extra map[string]any) error {
	if r.err != nil {
		return r.err
	}
	for k, v := range extra {
		r.account.Extra[k] = v
	}
	r.writes++
	return nil
}

type qualityLeaseRepository struct {
	OpenAIEvalRepository
	busy     bool
	released int
	config   *OpenAIEvalConfig
}

func (r *qualityLeaseRepository) GetConfig(context.Context) (*OpenAIEvalConfig, error) {
	if r.config != nil {
		return r.config, nil
	}
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}
	return &OpenAIEvalConfig{EffectsEnabled: true, Accounts: []OpenAIEvalAccountConfig{{AccountID: 17, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule, FingerprintSchedule: schedule, ModelTraceSchedule: schedule}}}, nil
}

func (r *qualityLeaseRepository) AcquireLease(context.Context, string, string, time.Duration) (bool, error) {
	return !r.busy, nil
}
func (r *qualityLeaseRepository) ReleaseLease(context.Context, string, string) error {
	r.released++
	return nil
}

func TestOpenAIEvalQualityRecordLatestRunNotAttempts(t *testing.T) {
	enableQualityEffects(t)
	accounts := &qualityAccountRepository{account: &Account{ID: 17, Extra: map[string]any{"unrelated": "preserved"}}}
	leases := &qualityLeaseRepository{}
	s := &OpenAIEvalService{accounts: accounts, repo: leases}
	now := time.Now().UTC().Add(-time.Second)
	run := &OpenAIEvalRun{AccountID: 17, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high",
		DataVersion: OpenAIEvalQualityDataVersion, TriggerSource: "scheduled", Status: "warning", FinishedAt: now,
		RequestCount: 12, ExpectedSamples: 10}
	counts := OpenAIEvalQualityCounts{3, 2, 0}
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 40, run, counts))
	got, ok := ReadOpenAIEvalQualityFromAccount(accounts.account, run.RequestedModel, run.ReasoningEffort, now)
	require.True(t, ok)
	require.Equal(t, 3, got.EvaluatedCount)
	require.InDelta(t, 2.0/3, got.Ratio(), 1e-12)
	require.Equal(t, "preserved", accounts.account.Extra["unrelated"])
	require.Equal(t, 1, leases.released)
	// Duplicates and out-of-order finish hooks cannot inflate or replace evidence.
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 40, run, counts))
	run.FinishedAt = now.Add(-time.Minute)
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 39, run, OpenAIEvalQualityCounts{10, 0, 0}))
	require.Equal(t, 1, accounts.writes)
	// A new run replaces the rolling aggregate, rather than accumulating forever.
	run.FinishedAt = now.Add(time.Millisecond)
	run.Status = "pass"
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 41, run, OpenAIEvalQualityCounts{1, 1, 0}))
	got, ok = ReadOpenAIEvalQualityFromAccount(accounts.account, run.RequestedModel, run.ReasoningEffort, time.Now())
	require.True(t, ok)
	require.Equal(t, 1, got.EvaluatedCount)
	require.Len(t, accounts.account.Extra, 2)
	accounts.err = errors.New("write failed")
	run.Status = "warning"
	run.FinishedAt = now.Add(2 * time.Millisecond)
	require.ErrorContains(t, s.recordOpenAIEvalQuality(context.Background(), 42, run, counts), "write failed")
	leases.busy = true
	require.ErrorContains(t, s.recordOpenAIEvalQuality(context.Background(), 42, run, counts), "busy")
}

func TestOpenAIEvalQualityRecordRejectsOperationalAndOldEvidence(t *testing.T) {
	enableQualityEffects(t)
	accounts := &qualityAccountRepository{account: &Account{ID: 17, Extra: map[string]any{}}}
	s := &OpenAIEvalService{accounts: accounts, repo: &qualityLeaseRepository{}}
	run := &OpenAIEvalRun{AccountID: 17, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high",
		DataVersion: OpenAIEvalQualityDataVersion, TriggerSource: "scheduled", Status: "pass", FinishedAt: time.Now().UTC()}
	counts := OpenAIEvalQualityCounts{1, 1, 0}
	for _, status := range []string{"error", "insufficient", "cancelled", "running"} {
		copy := *run
		copy.Status = status
		require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 1, &copy, counts))
	}
	unsupported := *run
	unsupported.TriggerSource = "imported"
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 1, &unsupported, counts))
	old := *run
	old.DataVersion = "candy-29-v2"
	require.Error(t, s.recordOpenAIEvalQuality(context.Background(), 1, &old, counts))
	require.Error(t, s.recordOpenAIEvalQuality(context.Background(), 1, run, OpenAIEvalQualityCounts{}))
	for _, code := range []string{"http_401", "timeout", "response_incomplete", "context_canceled"} {
		copy := *run
		copy.Error = code
		require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 1, &copy, counts))
	}
	SetOpenAIEvalEffectsEnabled(false)
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 1, run, counts))
	require.Zero(t, accounts.writes)
}

func TestOpenAIEvalQualityRecordIdentityBankAndPartialErrors(t *testing.T) {
	enableQualityEffects(t)
	accounts := &qualityAccountRepository{account: &Account{ID: 17, Extra: map[string]any{}}}
	s := &OpenAIEvalService{accounts: accounts, repo: &qualityLeaseRepository{}}
	now := time.Now().UTC().Add(-time.Second)
	run := &OpenAIEvalRun{AccountID: 17, TestType: OpenAIEvalTypeModelTrace, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high",
		DataVersion: OpenAIEvalQualityDataVersion, TriggerSource: "scheduled", Status: "suspected_normal", FinishedAt: now,
		Outcome: OpenAIEvalOutcome{ModelTrace: &OpenAIEvalModelTraceResult{BankRevision: OpenAIEvalQualityModelTraceBankRevision, Prediction: "gpt-6.1-sol", UsedOutputs: 2}}}
	counts := OpenAIEvalQualityCounts{2, 0, 2}
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 1, run, counts))
	require.Equal(t, 1, accounts.writes)
	run.FinishedAt = now.Add(time.Millisecond)
	run.Outcome.ModelTrace.Samples = []OpenAIEvalModelTraceSample{{Valid: true}, {Error: "response_incomplete"}}
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 2, run, counts))
	require.Equal(t, 1, accounts.writes, "used output count must match valid outputs")
	run.Outcome.ModelTrace.Samples = nil
	run.Outcome.ModelTrace.BankRevision = "old-16-model-bank"
	require.Error(t, s.recordOpenAIEvalQuality(context.Background(), 2, run, counts))
	run.Outcome.ModelTrace.BankRevision = OpenAIEvalQualityModelTraceBankRevision
	run.Outcome.ModelTrace.Prediction = "gpt-6-luna"
	require.Error(t, s.recordOpenAIEvalQuality(context.Background(), 2, run, counts))
	run.Status = "warning"
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 2, run, OpenAIEvalQualityCounts{2, 0, 0}))
	require.Equal(t, 2, accounts.writes)
	run.FinishedAt = now.Add(2 * time.Millisecond)
	run.TestType = OpenAIEvalTypeFingerprint
	run.Status = "suspected_normal"
	run.Outcome = OpenAIEvalOutcome{Fingerprint: &OpenAIEvalFingerprintResult{NearestModel: "gpt-6.1-sol", ValidSamples: 60}}
	counts = OpenAIEvalQualityCounts{60, 0, 60}
	run.BaselineVersion = "old-baseline"
	require.Error(t, s.recordOpenAIEvalQuality(context.Background(), 3, run, counts))
	run.BaselineVersion = OpenAIEvalQualityBaselineVersion
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 3, run, counts))
	require.Equal(t, 3, accounts.writes)
	require.Error(t, s.recordOpenAIEvalQuality(context.Background(), 4, run, OpenAIEvalQualityCounts{60, 60, 0}))
}
