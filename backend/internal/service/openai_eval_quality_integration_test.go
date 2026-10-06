//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type qualityRunAccountRepository struct {
	*openAIAccountTestRepo
}

func (r *qualityRunAccountRepository) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	a := r.accountsByID[id]
	if a == nil {
		return ErrAccountNotFound
	}
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}
	for key, value := range updates {
		a.Extra[key] = value
	}
	r.updatedExtra = updates
	return nil
}

func (r *qualityRunAccountRepository) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	var accounts []*Account
	for _, id := range ids {
		if account := r.accountsByID[id]; account != nil {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
}

type qualityRunRepository struct {
	*openAIEvalRepoFake
	finishErr error
}

func (r *qualityRunRepository) FinishRun(ctx context.Context, id int64, run *OpenAIEvalRun) error {
	if r.finishErr != nil {
		return r.finishErr
	}
	return r.openAIEvalRepoFake.FinishRun(ctx, id, run)
}

func TestOpenAIEvalQualityIntegrationPersistsCompletedManualAndAutomaticEvidence(t *testing.T) {
	for _, source := range []string{"scheduled", "scheduled_pass", "manual", "finish_failed", "upstream_failed"} {
		t.Run(source, func(t *testing.T) {
			enableQualityEffects(t)
			previousSnapshot := openAIEvalQualitySnapshots
			openAIEvalQualitySnapshots = &openAIEvalQualitySnapshotStore{}
			t.Cleanup(func() { openAIEvalQualitySnapshots = previousSnapshot })
			svc, baseRepo, _ := evalRunHarness(t, func(_ *http.Request, attempt int) (*http.Response, error) {
				if source == "upstream_failed" || attempt == 1 {
					return newJSONResponse(503, `{"error":{"code":"server_error","message":"busy"}}`), nil
				}
				answer := "21"
				if attempt == 3 && source != "scheduled_pass" {
					answer = "29"
				}
				return newJSONResponse(200, evalCompletedJSON(answer)), nil
			})
			accounts := &qualityRunAccountRepository{openAIAccountTestRepo: svc.accounts.(*openAIAccountTestRepo)}
			svc.accounts = accounts
			baseRepo.config = &OpenAIEvalConfig{EffectsEnabled: true, MaxRequestAttempts: 3,
				Accounts: []OpenAIEvalAccountConfig{{AccountID: 995, RequestedModel: "gpt-5.4", ReasoningEffort: "high",
					CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, SampleCount: 2}}}}
			repo := &qualityRunRepository{openAIEvalRepoFake: baseRepo}
			svc.repo = repo
			if source == "finish_failed" {
				repo.finishErr = errors.New("persist run failed")
			}
			trigger := "scheduled"
			if source == "manual" {
				trigger = source
			}
			run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", ReasoningEffort: "high", TestType: OpenAIEvalTypeCandy, SampleCount: 2}, 1, trigger)
			if source == "finish_failed" {
				require.ErrorIs(t, err, repo.finishErr)
			} else {
				require.NoError(t, err)
			}
			quality, found := ReadOpenAIEvalQualityFromAccount(accounts.accountsByID[995], "gpt-5.4", "high", time.Now())
			if source != "scheduled" && source != "scheduled_pass" && source != "manual" {
				require.False(t, found)
				if source == "upstream_failed" {
					marker, present := readOpenAIEvalQualityRecord(accounts.accountsByID[995], "gpt-5.4", "high", OpenAIEvalTypeCandy)
					require.True(t, present)
					require.Equal(t, "unknown", marker.OutcomeStatus)
					require.Zero(t, marker.EvaluatedCount)
				} else {
					require.Nil(t, accounts.updatedExtra)
				}
				return
			}
			require.True(t, found)
			require.Equal(t, 3, run.RequestCount)
			passed := 1
			if source == "scheduled_pass" {
				passed = 2
			}
			require.Equal(t, OpenAIEvalQualityCounts{2, passed, 0}, quality.OpenAIEvalQualityCounts)
			require.Equal(t, int64(1), quality.RunID)
			require.Equal(t, float64(passed)/2, quality.Ratio(), "raw sample detail remains separate from routing outcomes")
			_, known := openAIEvalQualitySnapshots.lookup(995, "gpt-5.4", "high", time.Now())
			require.False(t, known, "persisting evidence does not bypass the refresh cadence")
			refreshed, refreshErr := svc.RefreshOpenAIEvalQuality(t.Context(), 1)
			require.NoError(t, refreshErr)
			require.Equal(t, 1, refreshed.RouteCount)
			assessment, known := openAIEvalQualitySnapshots.lookup(995, "gpt-5.4", "high", time.Now())
			require.True(t, known)
			require.Equal(t, 1, assessment.EvaluatedCount, "Candy counts as one selected test despite two samples and three requests")
			if source == "scheduled_pass" {
				require.Equal(t, 1, assessment.PassCount)
				require.Equal(t, 1.0, assessment.Ratio())
			} else {
				require.Zero(t, assessment.PassCount)
				require.Zero(t, assessment.Ratio(), "a warning Candy final verdict is not a half-passing test")
			}
			for _, dimensions := range [][2]string{{"gpt-6-astra", "high"}, {"gpt-5.4", "low"}} {
				_, other := ReadOpenAIEvalQualityFromAccount(accounts.accountsByID[995], dimensions[0], dimensions[1], time.Now())
				require.False(t, other)
			}
		})
	}
}
