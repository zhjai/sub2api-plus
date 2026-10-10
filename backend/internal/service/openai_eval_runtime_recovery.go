package service

import (
	"context"
	"sync"
	"time"
)

// Recovery is a retry opportunity, not a synthetic health measurement. The
// published leaderboard retains its normal demotion; actual admission may
// temporarily undo only that one-position exception to collect real evidence.
type OpenAIEvalRuntimeRecovery struct {
	State       string    `json:"state"` // waiting, ready, in_flight
	NextTrialAt time.Time `json:"next_trial_at"`
}

func rankingRuntimeRecovery(policy string, t OpenAIEvalSchedulingThresholds, f OpenAIEvalRankingFactors, now time.Time) *OpenAIEvalRuntimeRecovery {
	if (t.RecoveryEnabled != nil && !*t.RecoveryEnabled) || len(rankingThresholdReasons(policy, t, f)) == 0 {
		return nil
	}
	interval := t.RecoveryIntervalSeconds
	if interval == 0 {
		interval = 1800
	}
	anchor := f.recoveryLastAttemptAt
	for _, observed := range []*time.Time{f.ErrorRate.ObservedAt, f.TTFT.ObservedAt} {
		if observed != nil && observed.After(anchor) {
			anchor = *observed
		}
	}
	if anchor.IsZero() || anchor.UnixNano() <= 0 {
		return nil
	}
	r := &OpenAIEvalRuntimeRecovery{State: "waiting", NextTrialAt: anchor.Add(time.Duration(interval) * time.Second)}
	if f.recoveryInFlight {
		r.State = "in_flight"
	} else if !now.Before(r.NextTrialAt) {
		r.State = "ready"
	}
	return r
}

func (s *openAIAccountRuntimeStats) reserveRuntimeRecovery(ctx context.Context, accountID int64, model, effort, policy string, t OpenAIEvalSchedulingThresholds, now time.Time) func() {
	if s == nil {
		return nil
	}
	// Take the same lock as reports so simultaneous selectors cannot claim the
	// same account/model/effort trial. No extra unbounded recovery map is needed.
	f := s.rankingFactors(accountID, model, effort, now)
	r := rankingRuntimeRecovery(policy, t, f, now)
	if r == nil || r.State != "ready" {
		return nil
	}
	s.rankingMu.Lock()
	stat, ok := s.loadRoute(accountID, model, effort)
	if !ok || stat.recoveryInFlight.Load() || stat.metricVersion.Load() != f.recoveryMetricVersion {
		s.rankingMu.Unlock()
		return nil
	}
	// A different selector may have completed a trial between the factor read
	// and this lock. Recheck its timestamp as well as new request evidence.
	if stat.recoveryLastAttemptAt.Load() != f.recoveryLastAttemptAt.UnixNano() {
		s.rankingMu.Unlock()
		return nil
	}
	beforeSamples := stat.sampleCount.Load()
	previousAttempt := stat.recoveryLastAttemptAt.Load()
	stat.recoveryLastAttemptAt.Store(now.UnixNano())
	stat.recoveryInFlight.Store(true)
	stat.metricVersion.Store(s.metricSeq.Add(1))
	s.rankingMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.rankingMu.Lock()
			defer s.rankingMu.Unlock()
			// HTTP handlers release concurrency before reporting the outcome.
			// Keep the interval reserved across that gap. Only explicit client
			// cancellation without a new sample refunds it; a missing report must
			// not cause a hot retry loop. We never erase EWMA failure history.
			if ctx.Err() != nil && stat.sampleCount.Load() == beforeSamples {
				stat.recoveryLastAttemptAt.Store(previousAttempt)
			}
			stat.recoveryInFlight.Store(false)
			stat.metricVersion.Store(s.metricSeq.Add(1))
		})
	}
}

func (s *defaultOpenAIAccountScheduler) runtimeRecoveryOrder(req OpenAIAccountScheduleRequest, rows []OpenAIEvalRankedAccount, order []openAIAccountCandidateScore, now time.Time) []openAIAccountCandidateScore {
	policy := openAIEffectiveSchedulingPolicy(req)
	if policy != OpenAIEvalSchedulingPolicyCostFirst && policy != OpenAIEvalSchedulingPolicyStabilityFirst && policy != OpenAIEvalSchedulingPolicyAvoidDegradation {
		return order
	}
	t := openAIEvalSchedulingPolicy.Load().(*openAIEvalSchedulingPolicySnapshot).Thresholds
	byID := make(map[int64]OpenAIEvalRankedAccount, len(rows))
	for _, row := range rows {
		byID[row.AccountID] = row
	}
	for i := 1; i < len(order); i++ {
		a, b := byID[order[i].account.ID], byID[order[i-1].account.ID]
		if len(a.ThresholdReasons) == 0 || !a.Eligible || !b.Eligible || !rankingPolicyLess(policy, a, b) {
			continue
		}
		if policy == OpenAIEvalSchedulingPolicyAvoidDegradation && compareRankingEffectiveQuality(a, b) != 0 {
			continue
		}
		if openAIAccountPriorityLess(req, b.AccountID, a.AccountID) {
			continue
		}
		f := s.stats.rankingFactors(a.AccountID, openAIClientModelForSchedule(req), req.RequestedReasoningEffort, now)
		r := rankingRuntimeRecovery(policy, t, f, now)
		if r == nil || r.State != "ready" {
			continue
		}
		trial := order[i]
		trial.thresholdRecoveryTrial = true
		// Keep the ordinary occurrence in its demoted position: if a concurrent
		// selector takes the trial, normal fallback remains available, unchanged.
		retried := make([]openAIAccountCandidateScore, 0, len(order)+1)
		retried = append(retried, order[:i-1]...)
		retried = append(retried, trial)
		retried = append(retried, order[i-1:]...)
		return retried
	}
	return order
}
