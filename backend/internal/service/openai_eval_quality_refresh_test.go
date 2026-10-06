package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type qualityRefreshRepository struct {
	OpenAIEvalRepository
	mu       sync.Mutex
	config   OpenAIEvalConfig
	err      error
	auditErr error
	audits   []string
}

func (r *qualityRefreshRepository) GetConfig(context.Context) (*OpenAIEvalConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := r.config
	copy.Accounts = append([]OpenAIEvalAccountConfig(nil), r.config.Accounts...)
	return &copy, r.err
}
func (r *qualityRefreshRepository) AcquireLease(context.Context, string, string, time.Duration) (bool, error) {
	return true, r.err
}
func (r *qualityRefreshRepository) ReleaseLease(context.Context, string, string) error {
	return nil
}
func (r *qualityRefreshRepository) RecordAuditEvent(_ context.Context, _ int64, action string, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audits = append(r.audits, action)
	return r.auditErr
}

type qualityRefreshAccounts struct {
	AccountRepository
	accounts map[int64]*Account
	batches  [][]int64
	err      error
	get      func(context.Context, []int64) ([]*Account, error)
}

func (r *qualityRefreshAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	return r.accounts[id], r.err
}

func (r *qualityRefreshAccounts) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	account := r.accounts[id]
	if account == nil {
		return ErrAccountNotFound
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	for key, value := range updates {
		account.Extra[key] = value
	}
	return r.err
}

func (r *qualityRefreshAccounts) GetByIDs(ctx context.Context, ids []int64) ([]*Account, error) {
	if r.get != nil {
		return r.get(ctx, ids)
	}
	r.batches = append(r.batches, append([]int64(nil), ids...))
	var out []*Account
	for _, id := range ids {
		if a := r.accounts[id]; a != nil {
			out = append(out, a)
		}
	}
	return out, r.err
}

func setupQualityRefreshTest(t *testing.T) (*OpenAIEvalService, *qualityRefreshRepository, *qualityRefreshAccounts) {
	t.Helper()
	previousPolicy := openAIEvalSchedulingPolicy.Load()
	t.Cleanup(func() { openAIEvalSchedulingPolicy.Store(previousPolicy) })
	enableQualityEffects(t)
	previous := openAIEvalQualitySnapshots
	openAIEvalQualitySnapshots = &openAIEvalQualitySnapshotStore{}
	t.Cleanup(func() { openAIEvalQualitySnapshots = previous })
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}
	repo := &qualityRefreshRepository{OpenAIEvalRepository: &qualityLeaseRepository{}, config: OpenAIEvalConfig{EffectsEnabled: true, Revision: 1, Accounts: []OpenAIEvalAccountConfig{{AccountID: 17, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule}}}}
	accounts := &qualityRefreshAccounts{accounts: map[int64]*Account{17: qualityTestAccount(t, qualityTestAggregate(time.Now().Add(-time.Minute), 17, 10, 8, 0))}}
	return &OpenAIEvalService{repo: repo, accounts: accounts}, repo, accounts
}

func TestOpenAIEvalQualityRefreshCadenceManualAndMultiType(t *testing.T) {
	s, repo, accounts := setupQualityRefreshTest(t)
	repo.config.Accounts[0].FingerprintSchedule = repo.config.Accounts[0].CandySchedule
	repo.config.Accounts[0].ModelTraceSchedule = repo.config.Accounts[0].CandySchedule
	now := time.Now().Add(-time.Minute)
	fingerprint := qualityTestAggregate(now, 17, 60, 0, 60)
	fingerprint.TestType = OpenAIEvalTypeFingerprint
	trace := qualityTestAggregate(now, 17, 3, 0, 0)
	trace.TestType = OpenAIEvalTypeModelTrace
	trace.AttributionRuleVersion = OpenAIEvalModelTraceRuleVersion
	for _, q := range []OpenAIEvalQualityAggregate{fingerprint, trace} {
		for k, v := range qualityTestAccount(t, q).Extra {
			accounts.accounts[17].Extra[k] = v
		}
	}
	result, err := s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, result.RouteCount)
	require.InDelta(t, 3600, result.NextRefreshAt.Sub(result.RefreshedAt).Seconds(), 0.001)
	quality, ok := openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
	require.True(t, ok)
	require.Equal(t, 3, quality.EvaluatedCount)
	require.Equal(t, 0, quality.PassCount)
	require.Equal(t, 1, quality.SuspectedPassCount)
	require.InDelta(t, 1.0/3, quality.Ratio(), 1e-12)
	accounts.accounts[17] = qualityTestAccount(t, qualityTestAggregate(now, 17, 1, 0, 0))
	unchanged, err := s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, result.RefreshedAt, unchanged.RefreshedAt)
	require.Len(t, accounts.batches, 1)
	quality, _ = openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
	require.Equal(t, 3, quality.EvaluatedCount)
	refreshed, err := s.RefreshOpenAIEvalQuality(context.Background(), 42)
	require.NoError(t, err)
	require.False(t, refreshed.RefreshedAt.Before(result.RefreshedAt))
	_, known := openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
	require.False(t, known, "missing selected diagnostics cannot become an assessed route")
	require.Zero(t, refreshed.RouteCount)
	require.Equal(t, []string{"quality_refresh_requested"}, repo.audits)
}

func TestOpenAIEvalQualityRefreshIsolationAutomaticOnlyAndExpiry(t *testing.T) {
	s, repo, accounts := setupQualityRefreshTest(t)
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}
	for i, dimension := range [][2]string{{"gpt-6.1-sol", "low"}, {"gpt-6-sol", "high"}} {
		q := qualityTestAggregate(time.Now().Add(-time.Hour), 17, 10, i+1, 0)
		q.RequestedModel, q.ReasoningEffort = dimension[0], dimension[1]
		for k, v := range qualityTestAccount(t, q).Extra {
			accounts.accounts[17].Extra[k] = v
		}
		repo.config.Accounts = append(repo.config.Accounts, OpenAIEvalAccountConfig{AccountID: 17, RequestedModel: dimension[0], ReasoningEffort: dimension[1], CandySchedule: schedule})
	}
	result, err := s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 3, result.RouteCount)
	require.Len(t, accounts.batches[0], 1)
	for _, dimension := range [][2]string{{"gpt-6.1-sol", "low"}, {"gpt-6-sol", "high"}} {
		q, ok := openAIEvalQualitySnapshots.lookup(17, dimension[0], dimension[1], time.Now())
		require.True(t, ok)
		require.Less(t, q.Ratio(), 0.3)
	}
	_, ok := openAIEvalQualitySnapshots.lookup(18, "gpt-6.1-sol", "high", time.Now())
	require.False(t, ok)
	_, ok = openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "", time.Now())
	require.False(t, ok)
	_, ok = openAIEvalQualitySnapshots.lookup(17, "gpt-6-sol", "high", time.Now().Add(2*time.Hour))
	require.False(t, ok)
	repo.config.Accounts[0].CandySchedule.Enabled = false
	repo.config.Accounts[0].FingerprintSchedule.Enabled = false
	repo.config.Accounts[0].ModelTraceSchedule.Enabled = false
	repo.config.Revision++
	_, err = s.refreshOpenAIEvalQuality(context.Background(), true)
	require.NoError(t, err)
	_, ok = openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
	require.False(t, ok)
}

func TestOpenAIEvalQualityRefreshCapacityAndBatching(t *testing.T) {
	s, repo, accounts := setupQualityRefreshTest(t)
	repo.config.Accounts = nil
	for i := 1; i <= OpenAIEvalQualityRouteLimit; i++ {
		repo.config.Accounts = append(repo.config.Accounts, OpenAIEvalAccountConfig{AccountID: int64(i), RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}})
		quality := qualityTestAggregate(time.Now().Add(-time.Minute), int64(i), 1, 1, 0)
		quality.ReasoningEffort = ""
		accounts.accounts[int64(i)] = qualityTestAccount(t, quality)
	}
	_, err := s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, accounts.batches, 25)
	require.Len(t, openAIEvalQualitySnapshots.entries, 5000)
	for _, batch := range accounts.batches {
		require.LessOrEqual(t, len(batch), 200)
	}
	repo.config.Accounts = append(repo.config.Accounts, repo.config.Accounts[0])
	_, err = s.refreshOpenAIEvalQuality(context.Background(), true)
	require.ErrorContains(t, err, "5000")
	require.LessOrEqual(t, len(openAIEvalQualitySnapshots.entries), 5000)
}

func TestOpenAIEvalQualityRefreshFailuresDoNotStampSuccess(t *testing.T) {
	s, repo, accounts := setupQualityRefreshTest(t)
	result, err := s.refreshOpenAIEvalQuality(context.Background(), true)
	require.NoError(t, err)
	accounts.err = errors.New("database unavailable")
	_, err = s.refreshOpenAIEvalQuality(context.Background(), true)
	require.Error(t, err)
	require.Equal(t, *result, openAIEvalQualitySnapshots.result)
	accounts.err = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.refreshOpenAIEvalQuality(ctx, true)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, *result, openAIEvalQualitySnapshots.result)
	repo.auditErr = errors.New("audit unavailable")
	_, err = s.RefreshOpenAIEvalQuality(context.Background(), 42)
	require.Error(t, err)
	require.Equal(t, *result, openAIEvalQualitySnapshots.result)
	repo.err = errors.New("config unavailable")
	_, err = s.refreshOpenAIEvalQuality(context.Background(), true)
	require.Error(t, err)
	require.Equal(t, *result, openAIEvalQualitySnapshots.result)
}

func TestOpenAIEvalQualityRefreshConcurrentAndConfigRevision(t *testing.T) {
	for _, change := range []string{"newer_refresh", "effects_off", "config_revision"} {
		t.Run(change, func(t *testing.T) {
			s, repo, accounts := setupQualityRefreshTest(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			accounts.get = func(ctx context.Context, ids []int64) ([]*Account, error) {
				if calls.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return []*Account{accounts.accounts[17]}, nil
			}
			done := make(chan error, 1)
			go func() { _, err := s.refreshOpenAIEvalQuality(context.Background(), true); done <- err }()
			<-entered
			switch change {
			case "newer_refresh":
				_, err := s.refreshOpenAIEvalQuality(context.Background(), true)
				require.NoError(t, err)
			case "effects_off":
				SetOpenAIEvalEffectsEnabled(false)
			case "config_revision":
				repo.config.Revision++
				require.NoError(t, openAIEvalQualitySnapshots.configure(&repo.config))
			}
			close(release)
			if change == "effects_off" {
				require.NoError(t, <-done, "routing activation alone cannot supersede evidence refresh")
				_, known := openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
				require.True(t, known)
			} else {
				require.ErrorIs(t, <-done, ErrOpenAIEvalQualityRefreshSuperseded)
			}
		})
	}
}

func TestOpenAIEvalQualityRefreshDisabledAndFreshness(t *testing.T) {
	s, repo, accounts := setupQualityRefreshTest(t)
	repo.config.EffectsEnabled = false
	SetOpenAIEvalEffectsEnabled(false)
	result, err := s.refreshOpenAIEvalQuality(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, 1, result.RouteCount)
	require.Len(t, accounts.batches, 1)
	for _, hours := range []int{1, 6, 12, 24, 72} {
		require.Equal(t, time.Duration(hours)*2*time.Hour, openAIEvalQualityFreshness(hours*3600, 3600))
	}
	require.Equal(t, 12*time.Hour, openAIEvalQualityFreshness(300, 6*3600))
	require.Equal(t, OpenAIEvalQualityMaxTTL, openAIEvalQualityFreshness(int(OpenAIEvalMaxIntervalSeconds), 3600))
}

func TestOpenAIEvalQualityRunnerInitialRefresh(t *testing.T) {
	s, repo, _ := setupQualityRefreshTest(t)
	runner := NewOpenAIEvalRunner(repo, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go runner.qualityRefreshLoop(ctx, done)
	require.Eventually(t, func() bool {
		_, ok := openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
		return ok
	}, time.Second, time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("quality refresh runner did not stop")
	}
}
