package service

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

type latestVerdictBlockingRepo struct {
	*rankingTestRepo
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (r *latestVerdictBlockingRepo) LatestCompletedRuns(ctx context.Context, keys []OpenAIEvalEvidenceKey) ([]OpenAIEvalRun, error) {
	runs, err := r.rankingTestRepo.LatestCompletedRuns(ctx, keys)
	r.once.Do(func() { close(r.entered); <-r.release })
	return runs, err
}

func TestOpenAIEvalLatestVerdictForcedEvaluationAfterInFlightBuild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, repo, now := latestVerdictHarness(t)
		blocking := &latestVerdictBlockingRepo{rankingTestRepo: repo, entered: make(chan struct{}), release: make(chan struct{})}
		s.repo = blocking
		type evaluationResult struct {
			summary *OpenAIEvalRankingSummary
			err     error
		}
		periodic, manual := make(chan evaluationResult, 1), make(chan evaluationResult, 1)
		go func() {
			summary, err := s.ranking.evaluate(context.Background(), "interval", false)
			periodic <- evaluationResult{summary, err}
		}()
		<-blocking.entered
		newer := repo.runs[0]
		newer.ID, newer.TriggerSource, newer.Status = 700, "manual", "warning"
		newer.FinishedAt, newer.Samples = now, []OpenAIEvalSampleRecord{{Valid: true, Answer: "29"}}
		repo.mu.Lock()
		repo.runs = append(repo.runs, newer)
		repo.mu.Unlock()
		go func() {
			summary, err := s.EvaluateScheduling(context.Background(), 1)
			manual <- evaluationResult{summary, err}
		}()
		// Both callers are blocked: the older build has already read its evidence.
		synctest.Wait()
		close(blocking.release)
		first, latest := <-periodic, <-manual
		require.NoError(t, first.err)
		require.NoError(t, latest.err)
		require.NotEqual(t, first.summary.EvaluationID, latest.summary.EvaluationID)
		require.Equal(t, "manual", latest.summary.Trigger)
		assertLatestVerdict(t, s, .5)
	})
}

func TestOpenAIEvalLatestVerdictPeriodicCoalescesAndManualCancels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, repo, _ := latestVerdictHarness(t)
		blocking := &latestVerdictBlockingRepo{rankingTestRepo: repo, entered: make(chan struct{}), release: make(chan struct{})}
		s.repo = blocking
		first, joined := make(chan *OpenAIEvalRankingSummary, 1), make(chan *OpenAIEvalRankingSummary, 1)
		go func() {
			summary, err := s.ranking.evaluate(context.Background(), "interval", false)
			require.NoError(t, err)
			first <- summary
		}()
		<-blocking.entered
		go func() {
			summary, err := s.ranking.evaluate(context.Background(), "interval", false)
			require.NoError(t, err)
			joined <- summary
		}()
		ctx, cancel := context.WithCancel(context.Background())
		cancelled := make(chan error, 1)
		go func() { _, err := s.EvaluateScheduling(ctx, 1); cancelled <- err }()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-cancelled, context.Canceled)
		close(blocking.release)
		require.Equal(t, (<-first).EvaluationID, (<-joined).EvaluationID)
		require.Equal(t, uint64(1), s.ranking.seq)
	})
}

func latestVerdictHarness(t *testing.T) (*OpenAIEvalService, *rankingTestRepo, time.Time) {
	t.Helper()
	s, repo, _, _ := rankingHarness(t)
	now := time.Now().UTC()
	s.ranking.now = func() time.Time { return now }
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	repo.config.QualityRefreshIntervalSeconds = 300
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "medium", CandySchedule: schedule, ModelTraceSchedule: schedule}}
	repo.config.Revision++
	repo.runs = []OpenAIEvalRun{
		{ID: 636, AccountID: 1, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "medium",
			DataVersion: OpenAIEvalQualityDataVersion, TriggerSource: "scheduled", Status: "pass", FinishedAt: now.Add(-time.Minute),
			Samples: []OpenAIEvalSampleRecord{{Valid: true, Answer: "21"}}},
		{ID: 635, AccountID: 1, TestType: OpenAIEvalTypeModelTrace, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "medium",
			DataVersion: OpenAIEvalQualityDataVersion, TriggerSource: "scheduled", Status: "attributed", Error: "upstream_error", FinishedAt: now.Add(-50 * time.Second),
			Outcome: OpenAIEvalOutcome{ModelTrace: &OpenAIEvalModelTraceResult{Prediction: "gpt-6.1-sol", UsedOutputs: 1, BankRevision: OpenAIEvalQualityModelTraceBankRevision,
				Samples: []OpenAIEvalModelTraceSample{{Error: "upstream_error"}, {Valid: true}, {Error: "upstream_error"}}}}},
	}
	return s, repo, now
}

func assertLatestVerdict(t *testing.T, s *OpenAIEvalService, ratio float64) {
	t.Helper()
	dim := rankingDimension(t, s, 7, "gpt-6.1-sol", "medium")
	var quality OpenAIEvalRankingQuality
	for _, row := range dim.Accounts {
		if row.AccountID == 1 {
			quality = row.Factors.Quality
		}
	}
	require.True(t, quality.Known)
	require.NotNil(t, quality.Ratio)
	require.InDelta(t, ratio, *quality.Ratio, 1e-12)
	require.Equal(t, 2, quality.Selected)
	require.Equal(t, 2, quality.Evaluated)
	overview, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	for _, row := range overview.Accounts {
		if row.AccountID == 1 {
			require.True(t, row.Priority.QualityKnown)
			require.NotNil(t, row.Priority.QualityRatio)
			require.InDelta(t, ratio, *row.Priority.QualityRatio, 1e-12)
		}
	}
	require.Equal(t, 1, OpenAIEvalQualityRefreshStatus().RouteCount)
}

func TestOpenAIEvalLatestVerdictPrismPassesBothTypes(t *testing.T) {
	s, repo, _ := latestVerdictHarness(t)
	summary, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, OpenAIEvalRankingAlgorithmVersion, summary.AlgorithmVersion)
	assertLatestVerdict(t, s, 1)
	require.Equal(t, "upstream_error", repo.runs[1].Error)
}

func TestOpenAIEvalLatestVerdictManualAndPeriodicRecompute(t *testing.T) {
	s, repo, now := latestVerdictHarness(t)
	first, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	assertLatestVerdict(t, s, 1)

	manual := repo.runs[0]
	manual.ID, manual.TriggerSource, manual.Status = 700, "manual", "warning"
	manual.FinishedAt, manual.Samples = now.Add(-10*time.Second), []OpenAIEvalSampleRecord{{Valid: true, Answer: "29"}}
	repo.runs = append(repo.runs, manual)
	second, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.NotEqual(t, first.EvaluationID, second.EvaluationID, "immediate evaluation must bypass a fresh snapshot")
	assertLatestVerdict(t, s, .5)

	// A later automatic run replaces the manual result; no historical averaging.
	scheduled := manual
	scheduled.ID, scheduled.TriggerSource, scheduled.Status = 701, "scheduled", "pass"
	scheduled.FinishedAt, scheduled.Samples = now, []OpenAIEvalSampleRecord{{Valid: true, Answer: "21"}}
	repo.runs = append(repo.runs, scheduled)
	unchanged, err := s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, second.EvaluationID, unchanged.EvaluationID, "periodic refresh respects the configured interval")
	s.ranking.now = func() time.Time { return now.Add(301 * time.Second) }
	third, err := s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.NotEqual(t, second.EvaluationID, third.EvaluationID)
	assertLatestVerdict(t, s, 1)
}

func TestOpenAIEvalLatestVerdictDoesNotRestoreOlderPass(t *testing.T) {
	s, repo, now := latestVerdictHarness(t)
	newer := repo.runs[1]
	newer.ID, newer.TriggerSource, newer.Status = 800, "manual", "insufficient"
	newer.FinishedAt, newer.Outcome = now, OpenAIEvalOutcome{}
	repo.runs = append(repo.runs, newer)
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	dim := rankingDimension(t, s, 7, "gpt-6.1-sol", "medium")
	for _, row := range dim.Accounts {
		if row.AccountID == 1 {
			require.False(t, row.Factors.Quality.Known)
			require.Nil(t, row.Factors.Quality.Ratio)
			require.Equal(t, 1, row.Factors.Quality.Evaluated)
		}
	}
}

func TestOpenAIEvalLatestVerdictReordersAccountsAndDispatch(t *testing.T) {
	s, repo, now := latestVerdictHarness(t)
	repo.config.Accounts = append(repo.config.Accounts, OpenAIEvalAccountConfig{AccountID: 2, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "medium",
		CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}})
	other := repo.runs[0]
	other.ID, other.AccountID = 637, 2
	repo.runs = append(repo.runs, other)
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), rankingDimension(t, s, 7, "gpt-6.1-sol", "medium").Accounts[0].AccountID)

	latest := repo.runs[0]
	latest.ID, latest.TriggerSource, latest.Status = 700, "manual", "warning"
	latest.FinishedAt, latest.Samples = now, []OpenAIEvalSampleRecord{{Valid: true, Answer: "29"}}
	repo.runs = append(repo.runs, latest)
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), rankingDimension(t, s, 7, "gpt-6.1-sol", "medium").Accounts[0].AccountID)
	group := int64(7)
	ctx := WithRequestedReasoningEffort(context.Background(), "medium")
	selection, _, err := s.ranking.gateway.SelectAccountWithScheduler(ctx, &group, "", "", "gpt-6.1-sol", nil,
		OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID)
	selection.ReleaseFunc()

	latest.ID, latest.TriggerSource, latest.Status = 701, "scheduled", "pass"
	latest.FinishedAt, latest.Samples = now.Add(time.Second), []OpenAIEvalSampleRecord{{Valid: true, Answer: "21"}}
	repo.runs = append(repo.runs, latest)
	s.ranking.now = func() time.Time { return now.Add(301 * time.Second) }
	_, err = s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, int64(1), rankingDimension(t, s, 7, "gpt-6.1-sol", "medium").Accounts[0].AccountID)
}

func TestOpenAIEvalLatestVerdictReloadsOperationalFactors(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	repo.config.QualityRefreshIntervalSeconds = 300
	repo.config.Revision++
	now := time.Now().UTC()
	s.ranking.now = func() time.Time { return now }
	first, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), rankingDimension(t, s, 7, "gpt-6.1-sol", "medium").Accounts[0].AccountID)

	accounts.items[0].RateMultiplier = rankingPtr(6.)
	accounts.items[1].RateMultiplier = rankingPtr(.7)
	gateway.openaiAccountStats.reportForRequest(1, "gpt-6.1-sol", "medium", false, rankingPtr(900))
	gateway.openaiAccountStats.reportForRequest(2, "gpt-6.1-sol", "medium", true, rankingPtr(100))
	s.ranking.now = time.Now
	second, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.NotEqual(t, first.EvaluationID, second.EvaluationID)
	dim := rankingDimension(t, s, 7, "gpt-6.1-sol", "medium")
	require.Equal(t, int64(2), dim.Accounts[0].AccountID)
	require.Equal(t, .7, *dim.Accounts[0].Factors.Price.RateMultiplier)
	require.True(t, dim.Accounts[0].Factors.ErrorRate.Known)
	require.True(t, dim.Accounts[0].Factors.TTFT.Known)
	require.Equal(t, 100., *dim.Accounts[0].Factors.TTFT.MS)

	accounts.items[0].RateMultiplier = rankingPtr(.01)
	s.ranking.now = func() time.Time { return now.Add(301 * time.Second) }
	third, err := s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.NotEqual(t, second.EvaluationID, third.EvaluationID)
	dim = rankingDimension(t, s, 7, "gpt-6.1-sol", "medium")
	require.Equal(t, int64(1), dim.Accounts[0].AccountID)
	require.Equal(t, .01, *dim.Accounts[0].Factors.Price.RateMultiplier)
}
