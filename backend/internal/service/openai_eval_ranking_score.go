package service

import (
	"math"
	"sort"
	"time"
)

func rankingPtr[T any](value T) *T { return &value }

func rankingUnknown(reason string) OpenAIEvalFactorMeta {
	return OpenAIEvalFactorMeta{Score: .5, UnknownReason: rankingPtr(reason)}
}
func rankingDefault(reason string) OpenAIEvalFactorMeta {
	return OpenAIEvalFactorMeta{Score: .9, UnknownReason: rankingPtr(reason), DefaultApplied: true}
}
func rankingKnown(score float64, at time.Time) OpenAIEvalFactorMeta {
	return OpenAIEvalFactorMeta{Score: clamp01(score), Known: true, ObservedAt: rankingPtr(at.UTC())}
}

func openAIEvalRankingWeights(config *OpenAIEvalConfig, model, effort string) (string, OpenAIEvalRankingWeights) {
	policy := OpenAIEvalSchedulingPolicyFor(config, model, effort)
	switch policy {
	case OpenAIEvalSchedulingPolicyCostFirst:
		return policy, OpenAIEvalRankingWeights{.6, .2, .1, .1, 0}
	case OpenAIEvalSchedulingPolicyStabilityFirst:
		return policy, OpenAIEvalRankingWeights{.1, .5, .3, .1, 0}
	case OpenAIEvalSchedulingPolicyAvoidDegradation:
		return policy, OpenAIEvalRankingWeights{.4, .35, .15, .1, 0}
	case OpenAIEvalSchedulingPolicyCustomBalance:
		weights := config.CustomBalance
		for _, rule := range config.Policies {
			if openAIEvalQualityDimension(rule.RequestedModel) != openAIEvalQualityDimension(model) || rule.CustomBalance == nil {
				continue
			}
			if rule.ReasoningEffort == "" {
				weights = *rule.CustomBalance
			}
		}
		for _, rule := range config.Policies {
			if rule.ReasoningEffort != "" && openAIEvalQualityDimension(rule.RequestedModel) == openAIEvalQualityDimension(model) && openAIEvalQualityDimension(rule.ReasoningEffort) == effort && rule.CustomBalance != nil {
				weights = *rule.CustomBalance
				break
			}
		}
		weights, err := normalizeOpenAIEvalPolicyWeights(weights)
		if err != nil {
			return policy, OpenAIEvalRankingWeights{.2, .2, .2, .2, .2}
		}
		return policy, OpenAIEvalRankingWeights{weights.Cost, weights.ErrorRate + weights.Stability*.6, weights.TTFT + weights.Stability*.4, weights.Load, weights.Quality}
	default:
		return "", OpenAIEvalRankingWeights{}
	}
}

type openAIEvalRankingInput struct {
	account    *Account
	factors    OpenAIEvalRankingFactors
	exclusions []OpenAIEvalRankingExclusion
	compatible bool
	upstream   []string
}

func emptyOpenAIEvalRankingFactors() OpenAIEvalRankingFactors {
	return OpenAIEvalRankingFactors{
		Price:     OpenAIEvalRankingPrice{OpenAIEvalFactorMeta: rankingDefault("price_unavailable"), Source: rankingPtr("optimistic_default")},
		ErrorRate: OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingDefault("no_request_samples"), Source: rankingPtr("optimistic_default"), MonitorIDs: []int64{}},
		TTFT:      OpenAIEvalRankingTTFT{OpenAIEvalFactorMeta: rankingDefault("no_first_output_samples")},
		Load:      OpenAIEvalRankingLoad{OpenAIEvalFactorMeta: rankingDefault("load_unavailable")},
		Quality:   OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingUnknown("no_selected_tests"), State: "unknown"},
	}
}

// Both evaluation and request fallback use this pure scorer over one input pool.
func scoreOpenAIEvalRanking(policy string, weights OpenAIEvalRankingWeights, inputs []openAIEvalRankingInput, now time.Time, oauthRate *float64) []OpenAIEvalRankedAccount {
	pool := make([]*Account, 0, len(inputs))
	minTTFT, maxTTFT := math.Inf(1), math.Inf(-1)
	for _, input := range inputs {
		if !input.compatible {
			continue
		}
		pool = append(pool, input.account)
		if input.factors.TTFT.Known && input.factors.TTFT.MS != nil {
			minTTFT = math.Min(minTTFT, *input.factors.TTFT.MS)
			maxTTFT = math.Max(maxTTFT, *input.factors.TTFT.MS)
		}
	}
	prices := openAIUpstreamCostFactors(pool, now, oauthRate)
	rows := make([]OpenAIEvalRankedAccount, 0, len(inputs))
	for _, input := range inputs {
		row := OpenAIEvalRankedAccount{AccountID: input.account.ID, AccountName: input.account.Name,
			Eligible: input.compatible && len(input.exclusions) == 0, ExclusionReasons: input.exclusions, UpstreamModels: input.upstream, Factors: input.factors}
		if row.ExclusionReasons == nil {
			row.ExclusionReasons = []OpenAIEvalRankingExclusion{}
		}
		if row.UpstreamModels == nil {
			row.UpstreamModels = []string{}
		}
		if len(row.ExclusionReasons) > 0 {
			row.ExclusionReason = rankingPtr(row.ExclusionReasons[0].Code)
		}
		f := &row.Factors
		if rate, ok := openAISchedulingRate(input.account, now, oauthRate); ok {
			factor := .5
			if value, exists := prices[input.account.ID]; exists {
				factor = value
			}
			f.Price = OpenAIEvalRankingPrice{OpenAIEvalFactorMeta: rankingKnown(factor, now), RateMultiplier: rankingPtr(rate), Source: rankingPtr("account_rate")}
			if input.account.IsOpenAIOAuthLike() && oauthRate != nil {
				f.Price.Source = rankingPtr("oauth_scheduling_rate")
			} else if _, fresh := openAIFreshUpstreamBillingRate(input.account, now); fresh {
				f.Price.Source = rankingPtr("upstream_reported_rate")
			}
		}
		if f.TTFT.Known && f.TTFT.MS != nil {
			f.TTFT.Score = .5
			if maxTTFT > minTTFT {
				f.TTFT.Score = 1 - clamp01((*f.TTFT.MS-minTTFT)/(maxTTFT-minTTFT))
			}
		}
		if input.compatible && policy != "" {
			row.Contributions = OpenAIEvalRankingWeights{100 * weights.Price * f.Price.Score, 100 * weights.ErrorRate * f.ErrorRate.Score,
				100 * weights.TTFT * f.TTFT.Score, 100 * weights.Load * f.Load.Score, 100 * weights.Quality * f.Quality.Score}
			c := row.Contributions
			row.PriorityScore = rankingPtr(c.Price + c.ErrorRate + c.TTFT + c.Load + c.Quality)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.PriorityScore == nil) != (b.PriorityScore == nil) {
			return a.PriorityScore != nil
		}
		if a.PriorityScore != nil && policy == OpenAIEvalSchedulingPolicyAvoidDegradation {
			if compared := compareRankingQuality(a.Factors.Quality, b.Factors.Quality); compared != 0 {
				return compared > 0
			}
		}
		if a.PriorityScore != nil && *a.PriorityScore != *b.PriorityScore {
			return *a.PriorityScore > *b.PriorityScore
		}
		return a.AccountID < b.AccountID
	})
	tier := 0
	for i := range rows {
		if rows[i].PriorityScore == nil {
			continue
		}
		rows[i].Rank = rankingPtr(i + 1)
		if policy == OpenAIEvalSchedulingPolicyAvoidDegradation {
			if i == 0 || compareRankingQuality(rows[i-1].Factors.Quality, rows[i].Factors.Quality) != 0 {
				tier++
			}
			rows[i].QualityTier = rankingPtr(tier)
		}
	}
	return rows
}

func compareRankingQuality(a, b OpenAIEvalRankingQuality) int {
	if a.macroRatio != nil || b.macroRatio != nil {
		left, right := rankingQualityFraction(a), rankingQualityFraction(b)
		if left == nil && right == nil {
			return 0
		}
		if left == nil {
			return -1
		}
		if right == nil {
			return 1
		}
		return left.Cmp(right)
	}
	assessment := func(q OpenAIEvalRankingQuality) *OpenAIEvalQualityAssessment {
		if !q.Known || q.Ratio == nil {
			return nil
		}
		return &OpenAIEvalQualityAssessment{EvaluatedCount: q.Selected, PassCount: q.Pass, SuspectedPassCount: q.SuspectedPass}
	}
	return compareOpenAIQuality(assessment(a), assessment(b))
}

func rankingFactorExpiry(f OpenAIEvalRankingFactors, weights OpenAIEvalRankingWeights, policy string) *time.Time {
	var until *time.Time
	include := func(at *time.Time, duration time.Duration) {
		if at != nil {
			expiry := at.Add(duration)
			if until == nil || expiry.Before(*until) {
				until = &expiry
			}
		}
	}
	if weights.ErrorRate > 0 && f.ErrorRate.Known {
		include(f.ErrorRate.ObservedAt, time.Duration(f.ErrorRate.WindowSeconds)*time.Second)
	}
	if weights.TTFT > 0 && f.TTFT.Known {
		include(f.TTFT.ObservedAt, openAIRankingMetricTTL)
	}
	if (weights.Quality > 0 || policy == OpenAIEvalSchedulingPolicyAvoidDegradation) && f.Quality.Known && f.Quality.ExpiresAt != nil {
		if until == nil || f.Quality.ExpiresAt.Before(*until) {
			until = f.Quality.ExpiresAt
		}
	}
	// Each matched probe may age out before the latest probe timestamp does.
	if weights.ErrorRate > 0 && f.ErrorRate.Source != nil && *f.ErrorRate.Source == "v1_matched_probe" {
		if f.monitorExpiresAt != nil && (until == nil || f.monitorExpiresAt.Before(*until)) {
			until = f.monitorExpiresAt
		}
	}
	return until
}

func qualityFromLatestRuns(config *OpenAIEvalConfig, accountID int64, model, effort string, latest map[OpenAIEvalEvidenceKey]OpenAIEvalRun, now time.Time) OpenAIEvalRankingQuality {
	q := emptyOpenAIEvalRankingFactors().Quality
	for _, testType := range openAIEvalQualityTestTypes {
		interval, selected := openAIEvalQualityTestInterval(config, accountID, model, effort, testType)
		if !selected {
			continue
		}
		q.Selected++
		run, found := latest[OpenAIEvalEvidenceKey{accountID, model, effort, testType}]
		if !found {
			continue
		}
		expires := run.FinishedAt.Add(openAIEvalQualityFreshness(interval, openAIEvalQualityRefreshSeconds(config)))
		if !expires.After(now) {
			q.State = "stale"
			continue
		}
		if run.DataVersion != OpenAIEvalQualityDataVersion || run.FinishedAt.After(now) || run.FinishedAt.IsZero() || !openAIEvalQualityRunSourceSupported(run.TriggerSource) || run.ID <= 0 {
			continue
		}
		normalizeOpenAIEvalAttributionRun(&run)
		counts := openAIEvalQualityCountsFromRun(&run)
		if !counts.valid() || counts.EvaluatedCount > openAIEvalQualityMaxSamples(testType) {
			continue
		}
		if testType == OpenAIEvalTypeFingerprint && run.BaselineVersion != OpenAIEvalQualityBaselineVersion {
			continue
		}
		if testType == OpenAIEvalTypeModelTrace && (run.Outcome.ModelTrace == nil || run.Outcome.ModelTrace.BankRevision != OpenAIEvalQualityModelTraceBankRevision) {
			continue
		}
		status := run.Status
		if testType == OpenAIEvalTypeFingerprint {
			status = OpenAIEvalIdentityQualityStatus(run.Outcome.Fingerprint.NearestModel)
		}
		if testType == OpenAIEvalTypeModelTrace {
			status = OpenAIEvalIdentityQualityStatus(run.Outcome.ModelTrace.Prediction)
		}
		aggregate := OpenAIEvalQualityAggregate{OpenAIEvalQualityCounts: counts, TestType: testType, OutcomeStatus: status}
		if _, ok := aggregate.diagnosticStatus(); !ok {
			continue
		}
		q.Evaluated++
		if status == "pass" {
			q.Pass++
		}
		if status == "suspected_normal" {
			q.SuspectedPass++
		}
		if q.ExpiresAt == nil || expires.Before(*q.ExpiresAt) {
			q.ExpiresAt = rankingPtr(expires.UTC())
		}
		if q.ObservedAt == nil || run.FinishedAt.Before(*q.ObservedAt) {
			q.ObservedAt = rankingPtr(run.FinishedAt.UTC())
		}
	}
	if q.Selected > 0 && q.Selected == q.Evaluated {
		q.State, q.Known, q.UnknownReason = "assessed", true, nil
		q.Score = float64(q.Pass+q.SuspectedPass) / float64(q.Selected)
		q.Ratio = rankingPtr(q.Score)
	} else if q.Selected > 0 {
		if q.State != "stale" {
			q.State = "insufficient"
		}
		q.UnknownReason = rankingPtr("selected_test_evidence_unavailable")
	}
	return q
}

func rankingQualityEvidenceChanged(cfg *OpenAIEvalConfig, rows []OpenAIEvalRankedAccount, model, effort string, latest map[OpenAIEvalEvidenceKey]OpenAIEvalRun, now time.Time) bool {
	for _, row := range rows {
		before := row.Factors.Quality
		after := qualityFromLatestRuns(cfg, row.AccountID, model, effort, latest, now)
		if before.Known != after.Known || before.Pass != after.Pass || before.SuspectedPass != after.SuspectedPass || before.Selected != after.Selected || before.Evaluated != after.Evaluated {
			return true
		}
		if (before.ObservedAt == nil) != (after.ObservedAt == nil) || (before.ObservedAt != nil && !before.ObservedAt.Equal(*after.ObservedAt)) {
			return true
		}
	}
	return false
}
