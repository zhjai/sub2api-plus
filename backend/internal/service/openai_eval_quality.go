package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// A new question or reference bank must get a new contract. Historical
	// Candy-29 results must never be relabeled with this version.
	OpenAIEvalQualityDataVersion            = "sub2api-candy-21-v4-number-presence-cpa-97623969-modeltrace-97623969"
	OpenAIEvalQualityBaselineVersion        = "cpa-codex-candy-eval-97623969-v2"
	OpenAIEvalQualityModelTraceBankRevision = "sha256:a4e256c00444179b76f3855578660e66f30659df8c0122f1768dd05a8d705630"
	OpenAIEvalQualityVersion                = "logical-samples-per-type-v2"
	OpenAIEvalQualityAssessmentBasis        = "automatic_test_outcomes_v1"
	OpenAIEvalQualityTTL                    = 2 * time.Hour
	OpenAIEvalQualityMaxTTL                 = 2 * time.Duration(OpenAIEvalMaxIntervalSeconds) * time.Second
)

// OpenAIEvalQualityCounts describes logical samples with substantive model
// responses. Neither physical attempts nor planned requests belong here.
// The ratio is an observed pass fraction, not a probability of model identity.
type OpenAIEvalQualityCounts struct {
	EvaluatedCount     int `json:"evaluated_count"`
	PassCount          int `json:"pass_count"`
	SuspectedPassCount int `json:"suspected_pass_count"`
}

func (c OpenAIEvalQualityCounts) valid() bool {
	return c.EvaluatedCount > 0 && c.EvaluatedCount <= OpenAIEvalMaxFingerprintRequests+OpenAIEvalMaxCandySamples+OpenAIEvalModelTraceRequests &&
		c.PassCount >= 0 && c.PassCount <= c.EvaluatedCount &&
		c.SuspectedPassCount >= 0 && c.SuspectedPassCount <= c.EvaluatedCount-c.PassCount
}

func (c OpenAIEvalQualityCounts) Ratio() float64 {
	if !c.valid() {
		return 0
	}
	return float64(c.PassCount+c.SuspectedPassCount) / float64(c.EvaluatedCount)
}

// Each record is the final result of ONE logical sample after retry handling.
// Evaluated requires a completed, substantive response and a valid diagnostic.
// A wrong but valid Candy answer is evaluated with Status="warning".
type OpenAIEvalQualitySample struct {
	SampleID  string
	Evaluated bool
	Status    string
}

func CountOpenAIEvalQualitySamples(samples []OpenAIEvalQualitySample) (OpenAIEvalQualityCounts, error) {
	var counts OpenAIEvalQualityCounts
	seen := make(map[string]bool, len(samples))
	for _, sample := range samples {
		if sample.SampleID == "" || seen[sample.SampleID] {
			return OpenAIEvalQualityCounts{}, errors.New("quality samples require unique logical sample IDs")
		}
		seen[sample.SampleID] = true
		if !sample.Evaluated {
			continue
		}
		switch sample.Status {
		case "pass":
			counts.PassCount++
		case "suspected_normal":
			counts.SuspectedPassCount++
		case "warning", "suspected_warning":
		default:
			return OpenAIEvalQualityCounts{}, fmt.Errorf("non-evaluable quality status %q", sample.Status)
		}
		counts.EvaluatedCount++
	}
	if counts.EvaluatedCount > 0 && !counts.valid() {
		return OpenAIEvalQualityCounts{}, errors.New("invalid quality sample counts")
	}
	return counts, nil
}

// OpenAIEvalIdentityQualityStatus classifies a valid Fingerprint/ModelTrace
// attribution. The caller must first establish diagnostic validity.
func OpenAIEvalIdentityQualityStatus(prediction string) string {
	prediction = strings.ToLower(strings.TrimSpace(prediction))
	if prediction == "" {
		return "insufficient"
	}
	for _, part := range strings.FieldsFunc(prediction, func(r rune) bool { return r == '-' || r == '_' || r == ' ' || r == '/' || r == ':' || r == '.' }) {
		if part == "luna" {
			return "suspected_warning"
		}
	}
	return "suspected_normal"
}

// OpenAIEvalQualityAggregate stores a test type's latest scheduled outcome.
// An unknown outcome has zero counts and cannot be used as scored evidence.
type OpenAIEvalQualityAggregate struct {
	OpenAIEvalQualityCounts
	Version         string    `json:"version"`
	DataVersion     string    `json:"data_version"`
	AccountID       int64     `json:"account_id"`
	RequestedModel  string    `json:"requested_model"`
	ReasoningEffort string    `json:"reasoning_effort"`
	RunID           int64     `json:"run_id"`
	TestType        string    `json:"test_type"`
	TriggerSource   string    `json:"trigger_source"`
	OutcomeStatus   string    `json:"outcome_status,omitempty"`
	EvaluatedAt     time.Time `json:"evaluated_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// Scheduler counts use one final outcome per selected automatic diagnostic,
// independent of its sample count. Raw sample evidence stays in account.Extra.
type OpenAIEvalQualityAssessment struct {
	EvaluatedCount     int       `json:"evaluated_count"`
	PassCount          int       `json:"pass_count"`
	SuspectedPassCount int       `json:"suspected_pass_count"`
	ExpiresAt          time.Time `json:"expires_at"`
}

func (q OpenAIEvalQualityAssessment) Ratio() float64 {
	if q.EvaluatedCount == 0 {
		return 0
	}
	return float64(q.PassCount+q.SuspectedPassCount) / float64(q.EvaluatedCount)
}

func (q OpenAIEvalQualityAggregate) diagnosticStatus() (string, bool) {
	if !q.OpenAIEvalQualityCounts.valid() {
		return "", false
	}
	var status string
	switch q.TestType {
	case OpenAIEvalTypeCandy:
		if q.SuspectedPassCount != 0 {
			return "", false
		}
		status = "warning"
		if q.PassCount == q.EvaluatedCount {
			status = "pass"
		}
	case OpenAIEvalTypeFingerprint, OpenAIEvalTypeModelTrace:
		if q.PassCount != 0 || (q.SuspectedPassCount != 0 && q.SuspectedPassCount != q.EvaluatedCount) {
			return "", false
		}
		status = "suspected_warning"
		if q.SuspectedPassCount == q.EvaluatedCount {
			status = "suspected_normal"
		}
	default:
		return "", false
	}
	// v2 evidence predating outcome_status is compatible: the recorder only
	// accepted completed runs, with all-or-none identity attribution counts.
	return status, q.OutcomeStatus == "" || q.OutcomeStatus == status
}

func openAIEvalQualityDimension(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func OpenAIEvalQualityExtraKeyFor(model, effort string, testTypes ...string) string {
	testType := OpenAIEvalTypeCandy
	if len(testTypes) > 0 {
		testType = testTypes[0]
	}
	// JSON tuple encoding avoids separator collisions and does not alias models.
	dimension, _ := json.Marshal([]string{OpenAIEvalQualityVersion, OpenAIEvalQualityDataVersion,
		openAIEvalQualityDimension(model), openAIEvalQualityDimension(effort), testType})
	digest := sha256.Sum256(dimension)
	return "openai_eval_quality_v2_" + hex.EncodeToString(digest[:])
}

func openAIEvalQualityMaxSamples(testType string) int {
	switch testType {
	case OpenAIEvalTypeCandy:
		return OpenAIEvalMaxCandySamples
	case OpenAIEvalTypeFingerprint:
		return OpenAIEvalMaxFingerprintRequests
	case OpenAIEvalTypeModelTrace:
		return OpenAIEvalModelTraceRequests
	default:
		return 0
	}
}

func (q OpenAIEvalQualityAggregate) validFor(accountID int64, model, effort string, now time.Time) bool {
	_, validOutcome := q.diagnosticStatus()
	return validOutcome && q.Version == OpenAIEvalQualityVersion && q.DataVersion == OpenAIEvalQualityDataVersion &&
		q.AccountID == accountID && accountID > 0 && q.RunID > 0 && q.TriggerSource == "scheduled" &&
		q.RequestedModel != "" && q.RequestedModel == openAIEvalQualityDimension(model) &&
		q.ReasoningEffort == openAIEvalQualityDimension(effort) && q.OpenAIEvalQualityCounts.valid() &&
		q.EvaluatedCount <= openAIEvalQualityMaxSamples(q.TestType) &&
		!q.EvaluatedAt.IsZero() && !q.EvaluatedAt.After(now) &&
		q.ExpiresAt.After(q.EvaluatedAt) && q.ExpiresAt.Sub(q.EvaluatedAt) <= OpenAIEvalQualityMaxTTL && now.Before(q.ExpiresAt)
}

var openAIEvalQualityTestTypes = []string{OpenAIEvalTypeCandy, OpenAIEvalTypeFingerprint, OpenAIEvalTypeModelTrace}

// ReadOpenAIEvalQualityFromAccount exposes one diagnostic's raw logical sample
// evidence. Cross-type routing ratios are derived only from selected outcomes.
func ReadOpenAIEvalQualityFromAccount(account *Account, model, effort string, now time.Time, testTypes ...string) (OpenAIEvalQualityAggregate, bool) {
	testType := OpenAIEvalTypeCandy
	if len(testTypes) > 0 {
		testType = testTypes[0]
	}
	return readOpenAIEvalQualityEvidence(account, model, effort, testType, now)
}

func readOpenAIEvalQualityEvidence(account *Account, model, effort, testType string, now time.Time) (OpenAIEvalQualityAggregate, bool) {
	if !OpenAIEvalEffectsEnabled() || account == nil || account.Extra == nil {
		return OpenAIEvalQualityAggregate{}, false
	}
	raw, exists := account.Extra[OpenAIEvalQualityExtraKeyFor(model, effort, testType)]
	if !exists {
		return OpenAIEvalQualityAggregate{}, false
	}
	payload, err := json.Marshal(raw)
	if err != nil || len(payload) > 4096 {
		return OpenAIEvalQualityAggregate{}, false
	}
	var quality OpenAIEvalQualityAggregate
	if json.Unmarshal(payload, &quality) != nil || quality.TestType != testType || !quality.validFor(account.ID, model, effort, now) {
		return OpenAIEvalQualityAggregate{}, false
	}
	return quality, true
}

// Call only AFTER FinishRun succeeds, using its persisted run ID. The caller
// supplies counts from final logical samples, never run.RequestCount.
// Manual diagnostics remain alert-only even when routing effects are enabled.
func (s *OpenAIEvalService) recordOpenAIEvalQuality(ctx context.Context, runID int64, run *OpenAIEvalRun, counts OpenAIEvalQualityCounts) error {
	if s == nil || s.accounts == nil || s.repo == nil || !OpenAIEvalEffectsEnabled() || run == nil || run.TriggerSource != "scheduled" {
		return nil
	}
	if run.Error != "" {
		return nil
	}
	switch run.Status {
	case "pass", "warning", "suspected_normal", "suspected_warning":
	default:
		return nil
	}
	switch run.TestType {
	case OpenAIEvalTypeCandy:
		if counts.SuspectedPassCount != 0 {
			return errors.New("Candy quality requires scored passes")
		}
	case OpenAIEvalTypeFingerprint:
		result := run.Outcome.Fingerprint
		if run.BaselineVersion != OpenAIEvalQualityBaselineVersion || result == nil || result.NearestModel == "" || result.ValidSamples != counts.EvaluatedCount {
			return errors.New("fingerprint quality evidence does not match the pinned baseline or counts")
		}
		if !openAIEvalIdentityQualityCountsMatch(result.NearestModel, counts) {
			return errors.New("fingerprint quality attribution and counts disagree")
		}
	case OpenAIEvalTypeModelTrace:
		result := run.Outcome.ModelTrace
		if result == nil || result.BankRevision != OpenAIEvalQualityModelTraceBankRevision || result.Prediction == "" || result.UsedOutputs != counts.EvaluatedCount {
			return errors.New("ModelTrace quality evidence does not match the pinned bank or counts")
		}
		for _, sample := range result.Samples {
			if sample.Error != "" {
				return nil
			}
		}
		if !openAIEvalIdentityQualityCountsMatch(result.Prediction, counts) {
			return errors.New("ModelTrace quality attribution and counts disagree")
		}
	default:
		return nil
	}
	config, err := s.repo.GetConfig(ctx)
	if err != nil {
		return err
	}
	interval, automatic := openAIEvalQualityTestInterval(config, run.AccountID, run.RequestedModel, run.ReasoningEffort, run.TestType)
	if !automatic || config == nil || !config.EffectsEnabled {
		return nil
	}
	freshness := openAIEvalQualityFreshness(interval, openAIEvalQualityRefreshSeconds(config))
	now := time.Now().UTC()
	quality := OpenAIEvalQualityAggregate{
		OpenAIEvalQualityCounts: counts, Version: OpenAIEvalQualityVersion, DataVersion: run.DataVersion,
		AccountID: run.AccountID, RequestedModel: openAIEvalQualityDimension(run.RequestedModel),
		ReasoningEffort: openAIEvalQualityDimension(run.ReasoningEffort), RunID: runID,
		TestType: run.TestType, TriggerSource: run.TriggerSource, OutcomeStatus: run.Status,
		EvaluatedAt: run.FinishedAt, ExpiresAt: run.FinishedAt.Add(freshness),
	}
	if run.TestType == OpenAIEvalTypeFingerprint {
		quality.OutcomeStatus = OpenAIEvalIdentityQualityStatus(run.Outcome.Fingerprint.NearestModel)
	} else if run.TestType == OpenAIEvalTypeModelTrace {
		quality.OutcomeStatus = OpenAIEvalIdentityQualityStatus(run.Outcome.ModelTrace.Prediction)
	}
	if !quality.validFor(run.AccountID, run.RequestedModel, run.ReasoningEffort, now) {
		return errors.New("invalid or outdated evaluation quality evidence")
	}
	return s.persistOpenAIEvalQuality(ctx, quality)
}

// A newer unsuccessful automatic run invalidates the preceding scored result.
// Persist an ordered marker instead of deleting the key: an older finishing
// attempt must not restore a stale pass after the unknown outcome.
func (s *OpenAIEvalService) recordOpenAIEvalQualityResult(ctx context.Context, runID int64, run *OpenAIEvalRun) error {
	if s == nil || s.accounts == nil || s.repo == nil || !OpenAIEvalEffectsEnabled() || run == nil || run.TriggerSource != "scheduled" || run.TestType == OpenAIEvalTypeStateProbe {
		return nil
	}
	counts := openAIEvalQualityCountsFromRun(run)
	if counts.valid() {
		return s.recordOpenAIEvalQuality(ctx, runID, run, counts)
	}
	if run.Status == "running" || run.FinishedAt.IsZero() || runID <= 0 || run.DataVersion != OpenAIEvalQualityDataVersion {
		return nil
	}
	if run.TestType != OpenAIEvalTypeCandy && run.TestType != OpenAIEvalTypeFingerprint && run.TestType != OpenAIEvalTypeModelTrace {
		return nil
	}
	config, err := s.repo.GetConfig(ctx)
	if err != nil {
		return err
	}
	interval, automatic := openAIEvalQualityTestInterval(config, run.AccountID, run.RequestedModel, run.ReasoningEffort, run.TestType)
	if !automatic || config == nil || !config.EffectsEnabled {
		return nil
	}
	return s.persistOpenAIEvalQuality(ctx, OpenAIEvalQualityAggregate{
		Version: OpenAIEvalQualityVersion, DataVersion: run.DataVersion,
		AccountID: run.AccountID, RequestedModel: openAIEvalQualityDimension(run.RequestedModel),
		ReasoningEffort: openAIEvalQualityDimension(run.ReasoningEffort), RunID: runID,
		TestType: run.TestType, TriggerSource: run.TriggerSource, OutcomeStatus: "unknown",
		EvaluatedAt: run.FinishedAt, ExpiresAt: run.FinishedAt.Add(openAIEvalQualityFreshness(interval, openAIEvalQualityRefreshSeconds(config))),
	})
}

func (s *OpenAIEvalService) persistOpenAIEvalQuality(ctx context.Context, quality OpenAIEvalQualityAggregate) error {
	runID := quality.RunID
	run := quality
	key := OpenAIEvalQualityExtraKeyFor(run.RequestedModel, run.ReasoningEffort, run.TestType)
	// Different diagnostic types have different runner leases. Serialize their
	// leaf-key updates so an older finisher cannot replace newer evidence.
	leaseKey := fmt.Sprintf("eval-quality:%d:%s", run.AccountID, key)
	owner, err := newOpenAIEvalLeaseOwner()
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	acquired, err := s.repo.AcquireLease(writeCtx, leaseKey, owner, 10*time.Second)
	if err != nil {
		return err
	}
	if !acquired {
		return errors.New("evaluation quality update busy")
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer releaseCancel()
		_ = s.repo.ReleaseLease(releaseCtx, leaseKey, owner)
	}()
	account, err := s.accounts.GetByID(writeCtx, run.AccountID)
	if err != nil {
		return err
	}
	if account == nil || account.ID != run.AccountID {
		return errors.New("evaluation quality account mismatch")
	}
	if previous, ok := readOpenAIEvalQualityRecord(account, run.RequestedModel, run.ReasoningEffort, run.TestType); ok &&
		(previous.EvaluatedAt.After(quality.EvaluatedAt) || (previous.EvaluatedAt.Equal(quality.EvaluatedAt) && previous.RunID >= runID)) {
		return nil
	}
	if !OpenAIEvalEffectsEnabled() {
		return nil
	}
	payload, err := json.Marshal(quality)
	if err != nil {
		return err
	}
	return s.accounts.UpdateExtra(writeCtx, run.AccountID, map[string]any{key: json.RawMessage(payload)})
}

// Read ordering metadata even for unknown/expired records; otherwise a delayed
// old successful run could overwrite a newer marker excluded by validFor.
func readOpenAIEvalQualityRecord(account *Account, model, effort, testType string) (OpenAIEvalQualityAggregate, bool) {
	if account == nil {
		return OpenAIEvalQualityAggregate{}, false
	}
	raw := account.Extra[OpenAIEvalQualityExtraKeyFor(model, effort, testType)]
	payload, err := json.Marshal(raw)
	if err != nil || len(payload) > 8192 {
		return OpenAIEvalQualityAggregate{}, false
	}
	var quality OpenAIEvalQualityAggregate
	if json.Unmarshal(payload, &quality) != nil || quality.Version != OpenAIEvalQualityVersion || quality.DataVersion != OpenAIEvalQualityDataVersion || quality.AccountID != account.ID || quality.RequestedModel != openAIEvalQualityDimension(model) || quality.ReasoningEffort != openAIEvalQualityDimension(effort) || quality.TestType != testType || quality.TriggerSource != "scheduled" || quality.RunID <= 0 || quality.EvaluatedAt.IsZero() {
		return OpenAIEvalQualityAggregate{}, false
	}
	return quality, true
}

func openAIEvalQualityTestInterval(config *OpenAIEvalConfig, accountID int64, model, effort, testType string) (int, bool) {
	if config == nil {
		return 0, false
	}
	for _, route := range config.Accounts {
		if route.AccountID != accountID || openAIEvalQualityDimension(route.RequestedModel) != openAIEvalQualityDimension(model) || openAIEvalQualityDimension(route.ReasoningEffort) != openAIEvalQualityDimension(effort) {
			continue
		}
		var schedule OpenAIEvalSchedule
		switch testType {
		case OpenAIEvalTypeCandy:
			schedule = route.CandySchedule
		case OpenAIEvalTypeFingerprint:
			schedule = route.FingerprintSchedule
		case OpenAIEvalTypeModelTrace:
			schedule = route.ModelTraceSchedule
		}
		return schedule.IntervalSeconds, schedule.Enabled
	}
	return 0, false
}

func openAIEvalQualityFreshness(testInterval, refreshInterval int) time.Duration {
	seconds := int64(300)
	if int64(testInterval) > seconds {
		seconds = int64(testInterval)
	}
	if int64(refreshInterval) > seconds {
		seconds = int64(refreshInterval)
	}
	if seconds > OpenAIEvalMaxIntervalSeconds {
		seconds = OpenAIEvalMaxIntervalSeconds
	}
	return 2 * time.Duration(seconds) * time.Second
}

func openAIEvalQualityRefreshSeconds(config *OpenAIEvalConfig) int {
	if config == nil || config.QualityRefreshIntervalSeconds == 0 {
		return OpenAIEvalDefaultQualityRefreshIntervalSeconds
	}
	return config.QualityRefreshIntervalSeconds
}

func openAIEvalIdentityQualityCountsMatch(prediction string, counts OpenAIEvalQualityCounts) bool {
	suspected := 0
	if OpenAIEvalIdentityQualityStatus(prediction) == "suspected_normal" {
		suspected = counts.EvaluatedCount
	}
	return counts.PassCount == 0 && counts.SuspectedPassCount == suspected
}
