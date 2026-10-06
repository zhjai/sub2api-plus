package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func (r *qualityRefreshRepository) SaveConfig(_ context.Context, config *OpenAIEvalConfig, _ int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config = *config
	return r.err
}

func TestOpenAIEvalQualityConfigLegacyWeightsAndRoundtrip(t *testing.T) {
	s, repo, _ := setupQualityRefreshTest(t)
	legacy := OpenAIEvalPolicyWeights{Cost: 2, Stability: 3, ErrorRate: 1, TTFT: 4, Load: 1}
	weights, err := normalizeOpenAIEvalPolicyWeights(legacy)
	require.NoError(t, err)
	require.Zero(t, weights.Stability)
	require.Zero(t, weights.Quality)
	require.InDelta(t, (1+.6*3)/11, weights.ErrorRate, 1e-12)
	require.InDelta(t, (4+.4*3)/11, weights.TTFT, 1e-12)
	for i := 0; i < 10; i++ {
		again, err := normalizeOpenAIEvalPolicyWeights(weights)
		require.NoError(t, err)
		require.Equal(t, weights, again)
	}
	config := &OpenAIEvalConfig{SchedulingPolicy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: legacy}
	require.NoError(t, s.SaveConfig(context.Background(), config, 1))
	require.Equal(t, 3600, config.QualityRefreshIntervalSeconds)
	got, err := s.GetConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, weights, got.CustomBalance)
	require.NoError(t, s.SaveConfig(context.Background(), got, 1))
	require.Equal(t, weights, repo.config.CustomBalance)
	var decoded OpenAIEvalConfig
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, weights, decoded.CustomBalance)
	config.CustomBalance = OpenAIEvalPolicyWeights{Quality: 1, Cost: 1}
	config.Policies = []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", Policy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: &OpenAIEvalPolicyWeights{Quality: 3, Load: 1}}}
	config.EffectsEnabled = true
	require.NoError(t, s.SaveConfig(context.Background(), config, 1))
	global, ok := OpenAIEvalCustomBalanceForRequest("gpt-6.1-sol", "low")
	require.True(t, ok)
	require.Equal(t, .5, global.Quality)
	route, ok := OpenAIEvalCustomBalanceForRequest("gpt-6.1-sol", "high")
	require.True(t, ok)
	require.Equal(t, .75, route.Quality)
}

func TestOpenAIEvalQualityConfigValidationAndIntervals(t *testing.T) {
	setters := []func(*OpenAIEvalPolicyWeights, float64){func(w *OpenAIEvalPolicyWeights, v float64) { w.Cost = v }, func(w *OpenAIEvalPolicyWeights, v float64) { w.Stability = v }, func(w *OpenAIEvalPolicyWeights, v float64) { w.ErrorRate = v }, func(w *OpenAIEvalPolicyWeights, v float64) { w.TTFT = v }, func(w *OpenAIEvalPolicyWeights, v float64) { w.Load = v }, func(w *OpenAIEvalPolicyWeights, v float64) { w.Quality = v }}
	for _, set := range setters {
		for _, value := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
			w := OpenAIEvalPolicyWeights{Cost: 1}
			set(&w, value)
			_, err := normalizeOpenAIEvalPolicyWeights(w)
			require.Error(t, err)
		}
	}
	for _, seconds := range []int{0, 300, 600, 1800, 3600, 21600, 43200, 86400, 73 * 60, int(OpenAIEvalMaxIntervalSeconds)} {
		config := &OpenAIEvalConfig{QualityRefreshIntervalSeconds: seconds}
		require.NoError(t, normalizeOpenAIEvalQualityConfig(config))
		require.GreaterOrEqual(t, config.QualityRefreshIntervalSeconds, 300)
	}
	for _, seconds := range []int{-1, 1, 299, int(OpenAIEvalMaxIntervalSeconds) + 1} {
		require.Error(t, normalizeOpenAIEvalQualityConfig(&OpenAIEvalConfig{QualityRefreshIntervalSeconds: seconds}))
	}
}

func TestOpenAIEvalQualityRefreshIntervalChangeInvalidatesOldWork(t *testing.T) {
	s, repo, _ := setupQualityRefreshTest(t)
	first, err := s.refreshOpenAIEvalQuality(context.Background(), true)
	require.NoError(t, err)
	repo.config.QualityRefreshIntervalSeconds = 6 * 3600
	repo.config.Revision++
	second, err := s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.False(t, second.RefreshedAt.Before(first.RefreshedAt))
	require.Equal(t, 6*time.Hour, second.NextRefreshAt.Sub(second.RefreshedAt))
	old := repo.config
	old.Revision--
	require.ErrorIs(t, openAIEvalQualitySnapshots.configure(&old), ErrOpenAIEvalQualityRefreshSuperseded)
}

type delayedQualityConfigRead struct {
	*qualityRefreshRepository
	entered chan struct{}
	release chan struct{}
	reads   atomic.Int32
}

func (r *delayedQualityConfigRead) GetConfig(ctx context.Context) (*OpenAIEvalConfig, error) {
	config, err := r.qualityRefreshRepository.GetConfig(ctx)
	if r.reads.Add(1) == 1 {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return config, err
}

func TestOpenAIEvalQualityStaleReadCannotRollbackSavedSettings(t *testing.T) {
	for _, readPath := range []string{"admin", "initialize", "periodic"} {
		for _, newerEnabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/enabled_%t", readPath, newerEnabled), func(t *testing.T) {
				s, repo, _ := setupQualityRefreshTest(t)
				repo.config.EffectsEnabled = !newerEnabled
				repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyCostFirst
				SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
				delayed := &delayedQualityConfigRead{qualityRefreshRepository: repo, entered: make(chan struct{}), release: make(chan struct{})}
				reader := &OpenAIEvalService{repo: delayed, accounts: s.accounts}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					var err error
					switch readPath {
					case "admin":
						_, err = reader.GetConfig(ctx)
					case "initialize":
						err = reader.Initialize(ctx)
					case "periodic":
						_, err = reader.refreshOpenAIEvalQuality(ctx, true)
					}
					done <- err
				}()
				select {
				case <-delayed.entered:
				case <-ctx.Done():
					t.Fatal("config read did not reach delay")
				}
				newer := repo.config
				newer.Revision++
				newer.EffectsEnabled = newerEnabled
				newer.SchedulingPolicy = OpenAIEvalSchedulingPolicyCustomBalance
				newer.CustomBalance = OpenAIEvalPolicyWeights{Quality: 3, Cost: 1}
				require.NoError(t, s.SaveConfig(ctx, &newer, 1))
				close(delayed.release)
				if readPath == "periodic" {
					require.ErrorIs(t, <-done, ErrOpenAIEvalQualityRefreshSuperseded)
				} else {
					require.NoError(t, <-done)
				}
				require.Equal(t, newerEnabled, OpenAIEvalEffectsEnabled())
				snapshot := openAIEvalSchedulingPolicy.Load().(*openAIEvalSchedulingPolicySnapshot)
				require.Equal(t, OpenAIEvalSchedulingPolicyCustomBalance, snapshot.Default)
				require.Equal(t, .75, snapshot.CustomBalance.Quality)
				openAIEvalQualitySnapshots.mu.Lock()
				revision := openAIEvalQualitySnapshots.configRevision
				openAIEvalQualitySnapshots.mu.Unlock()
				require.Equal(t, newer.Revision, revision)
			})
		}
	}
}

func TestOpenAIEvalQualityPeriodicRefreshAdoptsRemoteSettings(t *testing.T) {
	s, repo, _ := setupQualityRefreshTest(t)
	repo.config.EffectsEnabled = false
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyCostFirst
	require.NoError(t, s.Initialize(context.Background()))
	require.False(t, OpenAIEvalEffectsEnabled())
	update := func(enabled bool, policy string) {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		repo.config.Revision++
		repo.config.EffectsEnabled = enabled
		repo.config.SchedulingPolicy = policy
		repo.config.CustomBalance = OpenAIEvalPolicyWeights{Quality: 3, Cost: 1}
		repo.config.Policies = []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", Policy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: &OpenAIEvalPolicyWeights{Quality: 1, Load: 1}}}
	}
	update(true, OpenAIEvalSchedulingPolicyCustomBalance)
	result, err := s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, result.RouteCount)
	require.True(t, OpenAIEvalEffectsEnabled())
	weights, ok := OpenAIEvalCustomBalanceForRequest("gpt-6.1-sol", "low")
	require.True(t, ok)
	require.Equal(t, .75, weights.Quality)
	weights, ok = OpenAIEvalCustomBalanceForRequest("gpt-6.1-sol", "high")
	require.True(t, ok)
	require.Equal(t, .5, weights.Quality)
	update(false, OpenAIEvalSchedulingPolicyStabilityFirst)
	result, err = s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, result.RouteCount, "remote effects setting must not suppress quality refresh")
	require.False(t, OpenAIEvalEffectsEnabled())
	require.Equal(t, OpenAIEvalSchedulingPolicyLegacy, OpenAIEvalSchedulingPolicyForRequest("gpt-6.1-sol", "low"))
	update(true, OpenAIEvalSchedulingPolicyAvoidDegradation)
	_, err = s.refreshOpenAIEvalQuality(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, OpenAIEvalSchedulingPolicyAvoidDegradation, OpenAIEvalSchedulingPolicyForRequest("gpt-6.1-sol", "low"))
}
