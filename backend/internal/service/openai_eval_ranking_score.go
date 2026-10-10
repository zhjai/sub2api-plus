package service

import (
	"math"
	"math/big"
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
		return policy, OpenAIEvalRankingWeights{Price: 1}
	case OpenAIEvalSchedulingPolicyStabilityFirst:
		return policy, OpenAIEvalRankingWeights{Price: 1}
	case OpenAIEvalSchedulingPolicyAvoidDegradation:
		return policy, OpenAIEvalRankingWeights{Price: 1}
	case OpenAIEvalSchedulingPolicyCustomBalance:
		weights := config.CustomBalance
		for _, rule := range config.Policies {
			if rule.Enabled != nil && !*rule.Enabled {
				continue
			}
			if openAIEvalQualityDimension(rule.RequestedModel) != openAIEvalQualityDimension(model) || rule.CustomBalance == nil {
				continue
			}
			if rule.ReasoningEffort == "" {
				weights = *rule.CustomBalance
			}
		}
		for _, rule := range config.Policies {
			if rule.Enabled != nil && !*rule.Enabled {
				continue
			}
			if rule.ReasoningEffort != "" && openAIEvalQualityDimension(rule.RequestedModel) == openAIEvalQualityDimension(model) && openAIEvalQualityDimension(rule.ReasoningEffort) == effort && rule.CustomBalance != nil {
				weights = *rule.CustomBalance
				break
			}
		}
		weights, err := normalizeOpenAIEvalPolicyWeights(weights)
		if err != nil {
			return policy, OpenAIEvalRankingWeights{Price: .2, ErrorRate: .2, TTFT: .2, Load: .2, Quality: .2}
		}
		return policy, OpenAIEvalRankingWeights{Price: weights.Cost, ErrorRate: weights.ErrorRate + weights.Stability*.6, TTFT: weights.TTFT + weights.Stability*.4, Load: weights.Load, Quality: weights.Quality, AbsolutePriorities: append([]string(nil), weights.AbsolutePriorities...)}
	default:
		return "", OpenAIEvalRankingWeights{}
	}
}

func openAIEvalRankingWeightsEqual(a, b OpenAIEvalRankingWeights) bool {
	if a.Price != b.Price || a.ErrorRate != b.ErrorRate || a.TTFT != b.TTFT || a.Load != b.Load || a.Quality != b.Quality || len(a.AbsolutePriorities) != len(b.AbsolutePriorities) {
		return false
	}
	for i := range a.AbsolutePriorities {
		if a.AbsolutePriorities[i] != b.AbsolutePriorities[i] {
			return false
		}
	}
	return true
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
func scoreOpenAIEvalRanking(policy string, weights OpenAIEvalRankingWeights, inputs []openAIEvalRankingInput, now time.Time, oauthRate *float64, priorSets ...map[int64]*OpenAIEvalAccountQualityPrior) []OpenAIEvalRankedAccount {
	return scoreOpenAIEvalRankingWithThresholds(policy, weights, inputs, now, oauthRate, defaultOpenAIEvalSchedulingThresholds(), priorSets...)
}

func scoreOpenAIEvalRankingWithThresholds(policy string, weights OpenAIEvalRankingWeights, inputs []openAIEvalRankingInput, now time.Time, oauthRate *float64, thresholds OpenAIEvalSchedulingThresholds, priorSets ...map[int64]*OpenAIEvalAccountQualityPrior) []OpenAIEvalRankedAccount {
	inputs = append([]openAIEvalRankingInput(nil), inputs...)
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
	// Missing runtime evidence uses the same neutral score across the pool;
	// never renormalize weights per account or invent successful samples.
	if policy == OpenAIEvalSchedulingPolicyCustomBalance {
		for i := range inputs {
			f := &inputs[i].factors
			if !f.ErrorRate.Known {
				f.ErrorRate.Score = .5
			}
			if !f.TTFT.Known {
				f.TTFT.Score = .5
			}
			if !f.Quality.Known {
				f.Quality.Score = 0
			}
		}
	}
	rows := make([]OpenAIEvalRankedAccount, 0, len(inputs))
	for _, input := range inputs {
		row := OpenAIEvalRankedAccount{AccountID: input.account.ID, AccountName: input.account.Name,
			Eligible: input.compatible && len(input.exclusions) == 0, ExclusionReasons: input.exclusions, UpstreamModels: input.upstream, Factors: input.factors}
		row.QualityBasis = "none"
		if rankingQualityFraction(row.Factors.Quality) != nil {
			row.QualityBasis = "exact"
		} else if openAIEvalRankingUsesQuality(policy, weights) && len(priorSets) > 0 {
			if prior := priorSets[0][row.AccountID]; prior != nil && now.Before(prior.ExpiresAt) {
				row.AccountQualityPrior = cloneAccountQualityPrior(prior)
				row.QualityBasis = openAIEvalQualityPriorBasis(prior)
			}
		}
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
			qualityScore := f.Quality.Score
			if row.AccountQualityPrior != nil {
				qualityScore = row.AccountQualityPrior.Ratio
			}
			row.Contributions = OpenAIEvalRankingWeights{Price: 100 * weights.Price * f.Price.Score, ErrorRate: 100 * weights.ErrorRate * f.ErrorRate.Score,
				TTFT: 100 * weights.TTFT * f.TTFT.Score, Load: 100 * weights.Load * f.Load.Score, Quality: 100 * weights.Quality * qualityScore, AbsolutePriorities: append([]string(nil), weights.AbsolutePriorities...)}
			c := row.Contributions
			row.PriorityScore = rankingPtr(c.Price + c.ErrorRate + c.TTFT + c.Load + c.Quality)
		}
		row.ThresholdReasons = rankingThresholdReasons(policy, thresholds, row.Factors)
		row.Factors.RuntimeRecovery = rankingRuntimeRecovery(policy, thresholds, row.Factors, now)
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if policy == OpenAIEvalSchedulingPolicyCustomBalance {
			if cmp := compareAbsoluteRankingPriorities(weights.AbsolutePriorities, rows[i], rows[j]); cmp != 0 {
				return cmp > 0
			}
		}
		return rankingPolicyLess(policy, rows[i], rows[j])
	})
	demoteRankingThresholdsOnePosition(policy, len(rows), func(i int) OpenAIEvalRankedAccount { return rows[i] }, func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	tier := 0
	for i := range rows {
		if rows[i].PriorityScore == nil {
			continue
		}
		rows[i].Rank = rankingPtr(i + 1)
		if policy == OpenAIEvalSchedulingPolicyAvoidDegradation {
			if i == 0 || compareRankingEffectiveQuality(rows[i-1], rows[i]) != 0 {
				tier++
			}
			rows[i].QualityTier = rankingPtr(tier)
		}
	}
	return rows
}

func compareAbsoluteRankingPriorities(priorities []string, a, b OpenAIEvalRankedAccount) int {
	for _, priority := range priorities {
		var left, right float64
		var leftKnown, rightKnown bool
		switch priority {
		case "cost":
			left, right = a.Factors.Price.Score, b.Factors.Price.Score
			leftKnown, rightKnown = a.Factors.Price.Known, b.Factors.Price.Known
		case "error_rate":
			left, right = a.Factors.ErrorRate.Score, b.Factors.ErrorRate.Score
			leftKnown, rightKnown = a.Factors.ErrorRate.Known, b.Factors.ErrorRate.Known
		case "ttft":
			left, right = a.Factors.TTFT.Score, b.Factors.TTFT.Score
			leftKnown, rightKnown = a.Factors.TTFT.Known, b.Factors.TTFT.Known
		case "load":
			left, right = a.Factors.Load.Score, b.Factors.Load.Score
			leftKnown, rightKnown = a.Factors.Load.Known, b.Factors.Load.Known
		case "quality":
			if cmp := compareRankingEffectiveQuality(a, b); cmp != 0 {
				return cmp
			}
			continue
		default:
			continue
		}
		if leftKnown != rightKnown {
			if leftKnown {
				return 1
			}
			return -1
		}
		if leftKnown && left != right {
			if left > right {
				return 1
			}
			return -1
		}
	}
	return 0
}

func compareRankingEffectiveQuality(a, b OpenAIEvalRankedAccount) int {
	fraction := func(row OpenAIEvalRankedAccount) *big.Rat {
		if exact := rankingQualityFraction(row.Factors.Quality); exact != nil {
			return exact
		}
		if row.AccountQualityPrior != nil {
			return row.AccountQualityPrior.fraction
		}
		return nil
	}
	left, right := fraction(a), fraction(b)
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}
	if cmp := left.Cmp(right); cmp != 0 {
		return cmp
	}
	if a.QualityBasis == "exact" && b.QualityBasis != "exact" {
		return 1
	}
	if b.QualityBasis == "exact" && a.QualityBasis != "exact" {
		return -1
	}
	return 0
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
	if f.RuntimeRecovery != nil && f.RuntimeRecovery.State == "waiting" {
		until = rankingPtr(f.RuntimeRecovery.NextTrialAt)
	}
	include := func(at *time.Time, duration time.Duration) {
		if at != nil {
			expiry := at.Add(duration)
			if until == nil || expiry.Before(*until) {
				until = &expiry
			}
		}
	}
	if (weights.ErrorRate > 0 || policy != "") && f.ErrorRate.Known {
		include(f.ErrorRate.ObservedAt, time.Duration(f.ErrorRate.WindowSeconds)*time.Second)
	}
	if (weights.TTFT > 0 || policy != "") && f.TTFT.Known {
		include(f.TTFT.ObservedAt, openAIRankingMetricTTL)
	}
	if openAIEvalRankingUsesQuality(policy, weights) && f.Quality.Known && f.Quality.ExpiresAt != nil {
		if until == nil || f.Quality.ExpiresAt.Before(*until) {
			until = f.Quality.ExpiresAt
		}
	}
	// Each matched probe may age out before the latest probe timestamp does.
	if (weights.ErrorRate > 0 || openAIEvalHasAbsolutePriority(weights.AbsolutePriorities, "error_rate")) && f.ErrorRate.Source != nil && *f.ErrorRate.Source == "v1_matched_probe" {
		if f.monitorExpiresAt != nil && (until == nil || f.monitorExpiresAt.Before(*until)) {
			until = f.monitorExpiresAt
		}
	}
	return until
}

func qualityFromLatestRuns(config *OpenAIEvalConfig, accountID int64, model, effort string, latest map[OpenAIEvalEvidenceKey]OpenAIEvalRun, now time.Time) OpenAIEvalRankingQuality {
	q := emptyOpenAIEvalRankingFactors().Quality
	recordEvidenceError := func(run OpenAIEvalRun) {
		if q.EvidenceErrorCode != "" || q.EvidenceErrorMessage != "" {
			return
		}
		code, message := run.Error, ""
		for _, sample := range run.Samples {
			if code == "" && sample.ErrorCode != "" {
				code = sample.ErrorCode
			}
			if message == "" && sample.ErrorMessage != "" {
				message = sample.ErrorMessage
			}
			if code != "" && message != "" {
				break
			}
		}
		if code != "" {
			q.EvidenceErrorCode = sanitizeOpenAIEvalText(code, 160)
		}
		if message != "" {
			q.EvidenceErrorMessage = sanitizeOpenAIEvalText(message, openAIEvalErrorLimit)
		}
	}
	for _, testType := range openAIEvalQualityTestTypes {
		interval, selected := openAIEvalQualityTestInterval(config, accountID, model, effort, testType)
		if !selected {
			continue
		}
		q.Selected++
		run, found := latest[OpenAIEvalEvidenceKey{accountID, openAIEvalQualityDimension(model), openAIEvalQualityDimension(effort), testType}]
		if !found || run.DiagnosticOnly {
			if found {
				recordEvidenceError(run)
			}
			continue
		}
		expires := run.FinishedAt.Add(openAIEvalQualityFreshness(interval, openAIEvalQualityRefreshSeconds(config)))
		if !expires.After(now) {
			q.State = "stale"
			recordEvidenceError(run)
			continue
		}
		if run.DataVersion != OpenAIEvalQualityDataVersion || run.FinishedAt.After(now) || run.FinishedAt.IsZero() || !openAIEvalQualityRunSourceSupported(run.TriggerSource) || run.ID <= 0 {
			recordEvidenceError(run)
			continue
		}
		normalizeOpenAIEvalAttributionRun(&run)
		counts := openAIEvalQualityCountsFromRun(&run)
		if !counts.valid() || counts.EvaluatedCount > openAIEvalQualityMaxSamples(testType) {
			recordEvidenceError(run)
			continue
		}
		if testType == OpenAIEvalTypeFingerprint && run.BaselineVersion != OpenAIEvalQualityBaselineVersion {
			recordEvidenceError(run)
			continue
		}
		if testType == OpenAIEvalTypeModelTrace && (run.Outcome.ModelTrace == nil || run.Outcome.ModelTrace.BankRevision != OpenAIEvalQualityModelTraceBankRevision) {
			recordEvidenceError(run)
			continue
		}
		status := run.Status
		if testType == OpenAIEvalTypeFingerprint {
			status = OpenAIEvalIdentityQualityStatus(run.Outcome.Fingerprint.NearestModel)
		}
		if testType == OpenAIEvalTypeModelTrace {
			status, _ = openAIEvalModelTraceVerdict(run.RequestedModel, run.Outcome.ModelTrace.Prediction)
		}
		aggregate := OpenAIEvalQualityAggregate{OpenAIEvalQualityCounts: counts, TestType: testType, OutcomeStatus: status, AttributionRuleVersion: openAIEvalQualityAttributionRuleVersion(testType)}
		if _, ok := aggregate.diagnosticStatus(); !ok {
			recordEvidenceError(run)
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
