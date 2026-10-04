package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	OpenAIEvalMinCandyInterval = 5 * time.Minute
	OpenAIEvalMinCandySamples  = 1
	OpenAIEvalMaxCandySamples  = 10
	OpenAIEvalRunLeaseTTL      = 20 * time.Minute
	OpenAIEvalRunRenewInterval = 10 * time.Minute
	OpenAIEvalRunMaxDuration   = 24 * time.Hour
	// openai_eval_schedule_state.interval_seconds is a PostgreSQL INTEGER.
	// This is a storage-safety limit, not a product limit on custom intervals.
	OpenAIEvalMaxIntervalSeconds     = int64(1<<31 - 1)
	OpenAIEvalMaxFingerprintRequests = 400
	OpenAIEvalMinStateProbeInterval  = 5 * time.Minute
	OpenAIEvalMinBPSAccountInterval  = 5 * time.Minute
)

type OpenAIEvalRunRequest struct {
	AccountID       int64  `json:"account_id"`
	TestType        string `json:"test_type"`
	RequestedModel  string `json:"requested_model"`
	ReasoningEffort string `json:"reasoning_effort"`
	SampleMode      string `json:"sample_mode,omitempty"`
	SampleCount     int    `json:"sample_count,omitempty"`
	MaxAttempts     *int   `json:"max_attempts,omitempty"`
}

type OpenAIEvalService struct {
	ranking     *OpenAIEvalRankingService
	repo        OpenAIEvalRepository
	accounts    AccountRepository
	accountTest *AccountTestService
	pricing     *PricingService
}

// Initialize restores the process-wide scheduling-effects switch before the
// first admin request or scheduled evaluation runs. A failed read leaves the
// safe default unchanged; callers decide whether startup should be fatal.
func (s *OpenAIEvalService) Initialize(ctx context.Context) error {
	if s == nil || s.repo == nil {
		return errors.New("OpenAI evaluation service is unavailable")
	}
	config, err := s.repo.GetConfig(ctx)
	if err != nil {
		return err
	}
	if config == nil {
		SetOpenAIEvalSchedulingPolicySnapshot(nil)
		return nil
	}
	if err := normalizeOpenAIEvalQualityConfig(config); err != nil {
		return err
	}
	SetOpenAIEvalSchedulingPolicySnapshot(config)
	if s.ranking != nil {
		s.ranking.mu.Lock()
		s.ranking.adoptLocked(config)
		s.ranking.mu.Unlock()
	}
	return nil
}

func (s *OpenAIEvalService) SetPricingService(pricing *PricingService) {
	if s != nil {
		s.pricing = pricing
	}
}

func NewOpenAIEvalService(repo OpenAIEvalRepository, accounts AccountRepository, accountTest *AccountTestService) *OpenAIEvalService {
	return &OpenAIEvalService{repo: repo, accounts: accounts, accountTest: accountTest}
}

func (s *OpenAIEvalService) GetConfig(ctx context.Context) (*OpenAIEvalConfig, error) {
	config, err := s.repo.GetConfig(ctx)
	if err == nil && config != nil {
		if normalizeErr := normalizeOpenAIEvalQualityConfig(config); normalizeErr != nil {
			return nil, normalizeErr
		}
		if config.MaxRequestAttempts == 0 {
			config.MaxRequestAttempts = OpenAIEvalDefaultMaxRequestAttempts
		}
		if s.ranking == nil {
			SetOpenAIEvalSchedulingPolicySnapshot(config)
		}
		if s.accounts != nil {
			accountCache := make(map[int64]*Account)
			accountFor := func(accountID int64) *Account {
				if account, ok := accountCache[accountID]; ok {
					return account
				}
				account, _ := s.accounts.GetByID(ctx, accountID)
				accountCache[accountID] = account
				return account
			}
			for i := range config.Accounts {
				route := &config.Accounts[i]
				route.DirectOAuthEligible = false
				route.BPSState = nil
				account := accountFor(route.AccountID)
				if account != nil && account.IsOpenAIOAuth() {
					state := readOpenAIBPSAccountState(account)
					route.BPSState = &state
				}
				if account != nil {
					route.DirectOAuthEligible = account.IsOpenAIOAuth() && !account.IsShadow() && !account.IsSyntheticUITest() && !account.IsOpenAIAgentIdentity()
				}
			}
			for i := range config.BPSAccounts {
				item := &config.BPSAccounts[i]
				account := accountFor(item.AccountID)
				if account == nil || !account.IsOpenAIOAuth() {
					item.State = "inactive"
					continue
				}
				state := readOpenAIBPSAccountState(account)
				item.Active = state.Active
				item.DegradedStreak = state.DegradedStreak
				item.HealthyStreak = state.HealthyStreak
				item.DisabledReason = state.DisabledReason
				if state.UpdatedAt.IsZero() {
					item.UpdatedAt = nil
				} else {
					updated := state.UpdatedAt
					item.UpdatedAt = &updated
				}
				item.State = openAIBPSConfigRuntimeState(config, *item, state)
			}
		}
	}
	return config, err
}

func (s *OpenAIEvalService) SaveConfig(ctx context.Context, config *OpenAIEvalConfig, actorID int64) error {
	if config == nil {
		return errors.New("evaluation config is required")
	}
	if err := normalizeOpenAIEvalQualityConfig(config); err != nil {
		return err
	}
	if config.MaxRequestAttempts == 0 {
		config.MaxRequestAttempts = OpenAIEvalDefaultMaxRequestAttempts
	}
	if config.MaxRequestAttempts < 1 || config.MaxRequestAttempts > 10 {
		return errors.New("max_request_attempts must be between 1 and 10")
	}
	if len(config.Accounts) > 5000 {
		return errors.New("evaluation config exceeds the 5000 account-model-effort route limit")
	}
	if normalized, err := normalizeOpenAIEvalSchedulingPolicy(config.SchedulingPolicy); err != nil {
		return err
	} else {
		config.SchedulingPolicy = normalized
	}
	if config.SchedulingPolicy == OpenAIEvalSchedulingPolicyCustomBalance {
		weights, err := normalizeOpenAIEvalPolicyWeights(config.CustomBalance)
		if err != nil {
			return fmt.Errorf("custom balance: %w", err)
		}
		config.CustomBalance = weights
	}
	if len(config.BPSAccounts) > 5000 {
		return errors.New("BPS config exceeds the 5000 account limit")
	}
	seenBPS := make(map[int64]struct{}, len(config.BPSAccounts))
	for i := range config.BPSAccounts {
		item := &config.BPSAccounts[i]
		if item.AccountID <= 0 {
			return fmt.Errorf("invalid BPS account at index %d", i)
		}
		if _, exists := seenBPS[item.AccountID]; exists {
			return fmt.Errorf("duplicate BPS account at index %d", i)
		}
		seenBPS[item.AccountID] = struct{}{}
		item.Mode = normalizeOpenAIEvalBPSMode(item.Mode, false)
		if item.ProbeModel != "" && !isOpenAIEvalSupportedModel(item.ProbeModel) {
			return fmt.Errorf("invalid BPS probe model %q", item.ProbeModel)
		}
		if item.FailureThreshold == 0 {
			item.FailureThreshold = 3
		}
		if item.RecoveryThreshold == 0 {
			item.RecoveryThreshold = 2
		}
		if item.FailureThreshold < 1 || item.FailureThreshold > 10 || item.RecoveryThreshold < 1 || item.RecoveryThreshold > 10 {
			return fmt.Errorf("BPS account %d thresholds must be between 1 and 10", item.AccountID)
		}
		if item.IntervalSeconds == 0 {
			item.IntervalSeconds = int(OpenAIEvalMinBPSAccountInterval.Seconds())
		}
		if item.IntervalSeconds < int(OpenAIEvalMinBPSAccountInterval.Seconds()) || int64(item.IntervalSeconds) > OpenAIEvalMaxIntervalSeconds {
			return fmt.Errorf("BPS account %d interval must be at least %d seconds and fit database integer storage", item.AccountID, int(OpenAIEvalMinBPSAccountInterval.Seconds()))
		}
		// Runtime fields are a projection of accounts.extra and must never be
		// written back by the admin config endpoint.
		item.Active = false
		item.State = ""
		item.DisabledReason = ""
		item.DegradedStreak = 0
		item.HealthyStreak = 0
		item.UpdatedAt = nil
		if s.accounts == nil {
			return errors.New("account lookup is unavailable for BPS validation")
		}
		account, accountErr := s.accounts.GetByID(ctx, item.AccountID)
		if accountErr != nil || account == nil || !account.IsOpenAIOAuth() || account.IsShadow() || account.IsSyntheticUITest() || account.IsOpenAIAgentIdentity() {
			return fmt.Errorf("BPS account %d must be a direct OpenAI OAuth account", item.AccountID)
		}
	}
	for i := range config.Policies {
		rule := &config.Policies[i]
		if strings.TrimSpace(rule.RequestedModel) == "" || !isOpenAIEvalSupportedModel(rule.RequestedModel) {
			return fmt.Errorf("invalid scheduling policy rule at index %d", i)
		}
		if rule.ReasoningEffort != "" && !isAllowedOpenAIEvalReasoningEffort(rule.ReasoningEffort) {
			return fmt.Errorf("invalid scheduling policy effort %q", rule.ReasoningEffort)
		}
		if normalized, err := normalizeOpenAIEvalSchedulingPolicy(rule.Policy); err != nil || normalized == "" {
			if err != nil {
				return fmt.Errorf("policy rule %d: %w", i, err)
			}
			return fmt.Errorf("policy rule %d must specify a policy", i)
		} else {
			rule.Policy = normalized
		}
		if rule.Policy == OpenAIEvalSchedulingPolicyCustomBalance {
			if rule.CustomBalance == nil {
				return fmt.Errorf("policy rule %d: custom balance is required", i)
			}
			weights, err := normalizeOpenAIEvalPolicyWeights(*rule.CustomBalance)
			if err != nil {
				return fmt.Errorf("policy rule %d custom balance: %w", i, err)
			}
			rule.CustomBalance = &weights
		}
	}
	seen := make(map[string]struct{}, len(config.Accounts))
	for i := range config.Accounts {
		item := &config.Accounts[i]
		item.BPSState = nil              // runtime status is read-only, never persisted in route config
		item.DirectOAuthEligible = false // derived from the current account, never persisted
		item.BPSMode = normalizeOpenAIEvalBPSMode(item.BPSMode, item.BPSAuto)
		item.BPSAuto = item.BPSMode == OpenAIEvalBPSModeAuto
		if item.AccountID <= 0 || !isOpenAIEvalSupportedModel(item.RequestedModel) {
			return fmt.Errorf("invalid evaluation route at index %d", i)
		}
		if effort := strings.TrimSpace(item.ReasoningEffort); effort != "" && !isAllowedOpenAIEvalReasoningEffort(effort) {
			return fmt.Errorf("invalid reasoning effort %q", effort)
		}
		key := openAIEvalRouteKey(item.AccountID, item.RequestedModel, item.ReasoningEffort)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate evaluation route at index %d", i)
		}
		seen[key] = struct{}{}
		if err := validateOpenAIEvalSchedule(&item.CandySchedule, OpenAIEvalTypeCandy); err != nil {
			return fmt.Errorf("route %d Candy schedule: %w", i, err)
		}
		if item.CandySchedule.Enabled && item.CandySchedule.SampleCount == 0 {
			item.CandySchedule.SampleCount = OpenAIEvalMinCandySamples
		}
		if err := validateOpenAIEvalSchedule(&item.FingerprintSchedule, OpenAIEvalTypeFingerprint); err != nil {
			return fmt.Errorf("route %d Fingerprint schedule: %w", i, err)
		}
		if err := validateOpenAIEvalSchedule(&item.ModelTraceSchedule, OpenAIEvalTypeModelTrace); err != nil {
			return fmt.Errorf("route %d ModelTrace schedule: %w", i, err)
		}
		if err := validateOpenAIEvalSchedule(&item.StateProbeSchedule, OpenAIEvalTypeStateProbe); err != nil {
			return fmt.Errorf("route %d State Probe schedule: %w", i, err)
		}
		if item.StateProbeSchedule.Enabled {
			if s.accounts == nil {
				return errors.New("account lookup is unavailable for direct OAuth route validation")
			}
			account, accountErr := s.accounts.GetByID(ctx, item.AccountID)
			if accountErr != nil || account == nil || !account.IsOpenAIOAuth() || account.IsShadow() || account.IsSyntheticUITest() || account.IsOpenAIAgentIdentity() {
				return fmt.Errorf("route %d State Probe requires a direct OpenAI OAuth account", i)
			}
		}
	}
	if s.ranking != nil {
		s.ranking.mu.Lock()
	}
	if err := s.repo.SaveConfig(ctx, config, actorID); err != nil {
		if s.ranking != nil {
			s.ranking.mu.Unlock()
		}
		return err
	}
	if s.ranking != nil {
		s.ranking.adoptLocked(config)
		s.ranking.mu.Unlock()
		// A committed save is never reported as failed or retried because the
		// independent evaluation failed. Its structured status is in the DTO.
		_, _ = s.ranking.evaluate(ctx, "policy_saved", true)
	} else {
		SetOpenAIEvalSchedulingPolicySnapshot(config)
	}
	return nil
}

func openAIBPSConfigRuntimeState(config *OpenAIEvalConfig, item OpenAIEvalBPSAccountConfig, state OpenAIBPSAccountState) string {
	if strings.TrimSpace(state.DisabledReason) != "" {
		return "locked"
	}
	mode := normalizeOpenAIEvalBPSMode(item.Mode, false)
	if mode == OpenAIEvalBPSModeForceOff || (mode == OpenAIEvalBPSModeAuto && (config == nil || !config.BPSAutoEnabled)) {
		return "inactive"
	}
	if mode == OpenAIEvalBPSModeForceOn || state.Active {
		return "bps"
	}
	return "native"
}

// OpenAIEvalIntervalDuration converts a persisted schedule interval without
// allowing integer multiplication to wrap time.Duration.
func OpenAIEvalIntervalDuration(seconds int) (time.Duration, error) {
	if seconds < 0 || int64(seconds) > OpenAIEvalMaxIntervalSeconds {
		return 0, errors.New("interval does not fit database integer storage")
	}
	return time.Duration(seconds) * time.Second, nil
}

func OpenAIEvalMaxJitterSeconds(intervalSeconds int) int {
	if intervalSeconds <= 0 {
		return 0
	}
	maximum := intervalSeconds / 2
	if maximum > 3600 {
		maximum = 3600
	}
	remaining := OpenAIEvalMaxIntervalSeconds - int64(intervalSeconds)
	if remaining < int64(maximum) {
		if remaining <= 0 {
			return 0
		}
		maximum = int(remaining)
	}
	return maximum
}

func validateOpenAIEvalSchedule(schedule *OpenAIEvalSchedule, testType string) error {
	if !schedule.Enabled {
		return nil
	}
	minimum := int(OpenAIEvalMinCandyInterval.Seconds())
	if testType == OpenAIEvalTypeCandy {
		if schedule.SampleCount == 0 {
			schedule.SampleCount = OpenAIEvalMinCandySamples
		}
		if schedule.SampleCount < OpenAIEvalMinCandySamples || schedule.SampleCount > OpenAIEvalMaxCandySamples {
			return fmt.Errorf("sample count must be between %d and %d", OpenAIEvalMinCandySamples, OpenAIEvalMaxCandySamples)
		}
	}
	if testType == OpenAIEvalTypeFingerprint {
		minimum = int(OpenAIEvalMinFingerprintInterval.Seconds())
		if _, err := OpenAIEvalFingerprintSampleCount(schedule.SampleMode); err != nil {
			return err
		}
	}
	if testType == OpenAIEvalTypeModelTrace {
		minimum = int(OpenAIEvalModelTraceMinInterval.Seconds())
	}
	if testType == OpenAIEvalTypeStateProbe {
		minimum = int(OpenAIEvalMinStateProbeInterval.Seconds())
	}
	if schedule.IntervalSeconds < minimum {
		return fmt.Errorf("interval must be at least %d seconds", minimum)
	}
	if _, err := OpenAIEvalIntervalDuration(schedule.IntervalSeconds); err != nil {
		return err
	}
	if schedule.JitterSeconds < 0 || schedule.JitterSeconds > OpenAIEvalMaxJitterSeconds(schedule.IntervalSeconds) {
		return fmt.Errorf("jitter must be between 0 and %d seconds", OpenAIEvalMaxJitterSeconds(schedule.IntervalSeconds))
	}
	return nil
}

func (s *OpenAIEvalService) Run(ctx context.Context, request OpenAIEvalRunRequest, actorID int64, source string) (*OpenAIEvalRun, error) {
	if s == nil || s.repo == nil || s.accountTest == nil {
		return nil, errors.New("OpenAI evaluation service is unavailable")
	}
	request.TestType = strings.ToLower(strings.TrimSpace(request.TestType))
	request.RequestedModel = strings.TrimSpace(request.RequestedModel)
	request.ReasoningEffort = strings.TrimSpace(request.ReasoningEffort)
	if request.AccountID <= 0 || !isOpenAIEvalSupportedModel(request.RequestedModel) {
		return nil, errors.New("a supported OpenAI model and account are required")
	}
	// Account-scoped automatic BPS probes use an internal scheduler sentinel,
	// not a public reasoning-effort value. Normalize that private dimension
	// before applying the public effort allow-list.
	isBPSAccountProbe := source == "scheduled" && request.TestType == OpenAIEvalTypeStateProbe && request.ReasoningEffort == OpenAIEvalBPSAccountEffort
	if isBPSAccountProbe {
		request.ReasoningEffort = ""
	}
	if request.ReasoningEffort != "" && !isAllowedOpenAIEvalReasoningEffort(request.ReasoningEffort) {
		return nil, fmt.Errorf("unsupported reasoning effort %q", request.ReasoningEffort)
	}
	if request.TestType != OpenAIEvalTypeCandy && request.TestType != OpenAIEvalTypeFingerprint && request.TestType != OpenAIEvalTypeModelTrace && request.TestType != OpenAIEvalTypeStateProbe {
		return nil, fmt.Errorf("unsupported OpenAI evaluation type %q", request.TestType)
	}
	if source != "manual" && source != "scheduled" {
		return nil, fmt.Errorf("unsupported evaluation trigger source %q", source)
	}
	if request.TestType == OpenAIEvalTypeFingerprint {
		if _, err := OpenAIEvalFingerprintSampleCount(request.SampleMode); err != nil {
			return nil, err
		}
	}
	if request.TestType == OpenAIEvalTypeCandy {
		if request.SampleCount == 0 {
			request.SampleCount = OpenAIEvalMinCandySamples
		}
		if request.SampleCount < OpenAIEvalMinCandySamples || request.SampleCount > OpenAIEvalMaxCandySamples {
			return nil, fmt.Errorf("sample count must be between %d and %d", OpenAIEvalMinCandySamples, OpenAIEvalMaxCandySamples)
		}
	}

	if request.TestType == OpenAIEvalTypeStateProbe {
		// Ticket identity belongs to the OAuth account and model, not effort.
		request.ReasoningEffort = ""
	}
	maximum := OpenAIEvalDefaultMaxRequestAttempts
	if request.MaxAttempts != nil {
		maximum = *request.MaxAttempts
	} else {
		config, configErr := s.repo.GetConfig(ctx)
		if configErr != nil {
			return nil, configErr
		}
		if config != nil && config.MaxRequestAttempts != 0 {
			maximum = config.MaxRequestAttempts
		}
	}
	if maximum < 1 || maximum > 10 {
		return nil, errors.New("max_attempts must be between 1 and 10")
	}
	if request.TestType == OpenAIEvalTypeStateProbe && request.MaxAttempts != nil && maximum != 1 {
		return nil, errors.New("State Probe retries are unsupported; max_attempts must be 1 for the linked ticket chain")
	}
	leaseKey := openAIEvalRouteKey(request.AccountID, request.RequestedModel, request.ReasoningEffort) + ":" + request.TestType
	owner, err := newOpenAIEvalLeaseOwner()
	if err != nil {
		return nil, err
	}
	runCtx, cancelRun := context.WithTimeout(ctx, OpenAIEvalRunMaxDuration)
	defer cancelRun()
	acquired, err := s.repo.AcquireLease(runCtx, leaseKey, owner, OpenAIEvalRunLeaseTTL)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, errors.New("an evaluation for this account/model/effort is already running")
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer releaseCancel()
		_ = s.repo.ReleaseLease(releaseCtx, leaseKey, owner)
	}()
	var lostLease atomic.Bool
	var leaseErrMu sync.Mutex
	var leaseErr error
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		s.renewOpenAIEvalLease(runCtx, leaseKey, owner, &lostLease, &leaseErrMu, &leaseErr, cancelRun)
	}()
	defer func() {
		cancelRun()
		<-renewDone
	}()

	targetModel := request.RequestedModel
	if isBPSAccountProbe {
		if config, configErr := s.repo.GetConfig(runCtx); configErr == nil {
			if bps, ok := openAIEvalBPSAccountConfigFor(config, request.AccountID); ok && strings.TrimSpace(bps.ProbeModel) != "" {
				targetModel = strings.TrimSpace(bps.ProbeModel)
			}
		}
	}
	target, err := s.accountTest.ResolveOpenAIEvalTarget(runCtx, request.AccountID, targetModel)
	if err != nil {
		return nil, err
	}
	if request.TestType == OpenAIEvalTypeStateProbe && !isOpenAIStateProbeTarget(target) {
		return nil, errors.New("State Probe requires a direct OpenAI OAuth account")
	}
	start := time.Now().UTC()
	expectedSamples := request.SampleCount
	switch request.TestType {
	case OpenAIEvalTypeFingerprint:
		expectedSamples, _ = OpenAIEvalFingerprintSampleCount(request.SampleMode)
	case OpenAIEvalTypeModelTrace:
		expectedSamples = OpenAIEvalModelTraceRequests
	case OpenAIEvalTypeStateProbe:
		expectedSamples = 2
	}
	run := &OpenAIEvalRun{
		AccountID: request.AccountID, TestType: request.TestType,
		RequestedModel: request.RequestedModel, UpstreamModel: target.UpstreamModel,
		ReasoningEffort: request.ReasoningEffort, DataVersion: OpenAIEvalDataVersion,
		Status: "running", StartedAt: start, TriggeredBy: actorID, TriggerSource: source,
		Outcome:         OpenAIEvalOutcome{Status: "running", Reason: "sampling", SampleCount: 0, ExpectedCount: expectedSamples, Confidence: "none", Scheduling: "disabled"},
		SampleCount:     expectedSamples,
		ExpectedSamples: expectedSamples,
		Phase:           "sampling",
		Samples:         make([]OpenAIEvalSampleRecord, 0),
	}
	runID, err := s.repo.CreateRun(runCtx, run)
	if err != nil {
		return nil, err
	}
	persistProgress := func() {
		updater, ok := s.repo.(OpenAIEvalProgressRepository)
		if !ok {
			return
		}
		run.Status = "running"
		run.DurationMS = time.Since(start).Milliseconds()
		run.Outcome = OpenAIEvalOutcome{
			Status: "running", Reason: run.Phase, SampleCount: run.CompletedSamples,
			ExpectedCount: run.ExpectedSamples, Confidence: "none", Scheduling: "disabled",
		}
		progressCtx, progressCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer progressCancel()
		if progressErr := updater.UpdateRunProgress(progressCtx, runID, run); progressErr != nil {
			logger.LegacyPrintf("service.openai_eval", "[OpenAI Eval] progress update failed run=%d completed=%d expected=%d: %v", runID, run.CompletedSamples, run.ExpectedSamples, progressErr)
		}
	}
	hardFailure := false
	hardFailureCode := ""
	markHardFailure := func(err error) {
		code := safeOpenAIEvalErrorCode(err)
		if !isHardOpenAIEvalFailure(code) {
			return
		}
		hardFailure = true
		if hardFailureCode == "" {
			hardFailureCode = code
		}
	}
	finish := func(runErr error) (*OpenAIEvalRun, error) {
		run.FinishedAt = time.Now().UTC()
		run.DurationMS = run.FinishedAt.Sub(start).Milliseconds()
		if lostLease.Load() {
			leaseErrMu.Lock()
			lostErr := leaseErr
			leaseErrMu.Unlock()
			if lostErr == nil {
				lostErr = errors.New("lease no longer belongs to this runner")
			}
			runErr = errors.Join(runErr, fmt.Errorf("evaluation lease lost: %w", lostErr))
		}
		if runErr != nil {
			markHardFailure(runErr)
			run.Status = "error"
			run.Error = safeOpenAIEvalErrorCode(runErr)
			run.Outcome = OpenAIEvalOutcome{Status: "error", Reason: run.Error, SampleCount: run.CompletedSamples, ExpectedCount: run.ExpectedSamples, Confidence: "none", Scheduling: "alert_only"}
		}
		if run.Error == "" {
			for _, sample := range run.Samples {
				if sample.ErrorMessage != "" {
					run.Error = sample.ErrorCode
					break
				}
			}
		}
		run.CostEstimateUSD = s.estimateRunCost(target.UpstreamModel, request.RequestedModel, run.InputTokens, run.OutputTokens)
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer finishCancel()
		if saveErr := s.repo.FinishRun(finishCtx, runID, run); saveErr != nil {
			if runErr != nil {
				return run, errors.Join(runErr, saveErr)
			}
			return run, saveErr
		}
		if qualityErr := s.recordOpenAIEvalQualityResult(finishCtx, runID, run); qualityErr != nil {
			logger.LegacyPrintf("service.openai_eval", "[OpenAI Eval] quality update failed run=%d: %v", runID, qualityErr)
		}
		// Diagnostic quality changes preference only. It must not fabricate
		// operational health failures or hard-exclude a production route.
		if shouldRecordOpenAIEvalRouteHealth(request.TestType) {
			s.recordRouteHealth(finishCtx, target.Account.ID, request.RequestedModel, request.ReasoningEffort, hardFailure, hardFailureCode)
		}
		return run, runErr
	}
	if request.TestType == OpenAIEvalTypeStateProbe {
		probe := s.accountTest.RunOpenAIStateProbe(runCtx, target)
		run.RequestCount = probe.RequestCount
		run.Samples = probe.Samples
		run.CompletedSamples = len(probe.Samples)
		run.Outcome = OpenAIEvalOutcome{Status: probe.Verdict, Reason: probe.Failure, SampleCount: probe.RequestCount, ExpectedCount: 2, Confidence: "low", Scheduling: "alert_only", StateProbe: probe}
		run.Status = probe.Verdict
		completed, finishErr := finish(nil)
		if finishErr == nil {
			s.applyOpenAIStateProbeBPS(ctx, target, probe, isBPSAccountProbe)
		}
		return completed, finishErr
	}

	if request.TestType == OpenAIEvalTypeCandy {
		candyCtx := withOpenAIEvalFullCandyAnswer(runCtx)
		allPassed := true
		validSamples := 0
		for i := 0; i < request.SampleCount; i++ {
			run.Phase = "sampling"
			result, record, sampleErr := s.accountTest.runOpenAIEvalSampleAttempts(candyCtx, target, OpenAIEvalCandyPrompt, request.ReasoningEffort, maximum)
			record.ProbeID = fmt.Sprintf("candy-21-v4-97623969-%d", i+1)
			run.RequestCount += record.Attempts
			run.CompletedSamples++
			run.Samples = append(run.Samples, record)
			if result != nil {
				run.InputTokens += result.InputTokens
				run.OutputTokens += result.OutputTokens
			}
			if ctxErr := runCtx.Err(); ctxErr != nil {
				return finish(ctxErr)
			}
			if sampleErr != nil {
				markHardFailure(sampleErr)
				if run.Error == "" {
					run.Error = record.ErrorCode
				}
				allPassed = false
				persistProgress()
				continue
			}
			validSamples++
			outcome := ScoreOpenAIEvalCandy(result.Text)
			allPassed = allPassed && outcome.Status == "pass"
			if value, ok := leadingOpenAIEvalCandyAnswer(result.Text); ok {
				run.Samples[len(run.Samples)-1].NormalizedAnswer = fmt.Sprint(value)
			}
			run.Samples[len(run.Samples)-1].ErrorCode = outcome.Reason
			persistProgress()
		}
		if validSamples < request.SampleCount {
			// A transport/upstream failure means this run did not produce
			// enough evidence for a canary verdict. Keep it neutral in the UI
			// and let the operational error code explain what to retry.
			run.Outcome = OpenAIEvalOutcome{Status: "insufficient", Reason: "insufficient_valid_samples", Score: 0, SampleCount: validSamples, ExpectedCount: request.SampleCount, Confidence: "none", Scheduling: "alert_only"}
			run.Status = "insufficient"
		} else if allPassed {
			run.Outcome = OpenAIEvalOutcome{Status: "pass", Reason: "all_public_candy_variants_passed", Score: 1, SampleCount: validSamples, ExpectedCount: request.SampleCount, Confidence: "low", Scheduling: "alert_only"}
			run.Status = "pass"
		} else {
			run.Outcome = OpenAIEvalOutcome{Status: "warning", Reason: "one_or_more_public_candy_variants_failed", Score: 0, SampleCount: validSamples, ExpectedCount: request.SampleCount, Confidence: "low", Scheduling: "alert_only"}
			run.Status = "warning"
		}
		return finish(nil)
	}

	if request.TestType == OpenAIEvalTypeModelTrace {
		trace, count, inputTokens, outputTokens, traceErr := s.runModelTrace(runCtx, target, request.ReasoningEffort, maximum)
		run.RequestCount = count
		run.InputTokens = inputTokens
		run.OutputTokens = outputTokens
		if trace != nil {
			for i := range trace.Samples {
				sample := trace.Samples[i]
				run.Samples = append(run.Samples, OpenAIEvalSampleRecord{ProbeID: fmt.Sprintf("modeltrace-%d", i+1), Answer: sample.Answer, Attempts: sample.Attempts, Valid: sample.Valid, ErrorCode: sample.Error, ErrorMessage: sample.ErrorMessage, AttemptErrors: sample.AttemptErrors, HTTPStatus: sample.HTTPStatus})
				trace.Samples[i].Prompt = ""
				trace.Samples[i].Text = ""
			}
			run.CompletedSamples = len(run.Samples)
			run.Outcome = modelTraceSchedulingOutcome(trace, traceErr)
			run.Status = run.Outcome.Status
		}
		if traceErr != nil && (trace == nil || trace.UsedOutputs == 0) {
			// An incomplete collection is a neutral result, not a route health
			// failure. It should be retried or inspected manually.
			run.Error = safeOpenAIEvalErrorCode(traceErr)
		}
		if ctxErr := runCtx.Err(); ctxErr != nil {
			return finish(ctxErr)
		}
		return finish(nil)
	}

	cells, repeats, _ := OpenAIEvalFingerprintPlan(request.SampleMode)
	required := cells * repeats
	run.BaselineVersion = OpenAIEvalBaselineVersion
	samples := make([]OpenAIEvalSample, 0, required)
	for i := 0; i < cells; i++ {
		probe := OpenAIEvalFingerprintProbes[i]
		for sampleIndex := 0; sampleIndex < repeats; sampleIndex++ {
			prompts := probe.Prompts
			prompt := prompts[sampleIndex%len(prompts)]
			fullPrompt := probe.Instructions + "\n" + prompt
			result, record, sampleErr := s.accountTest.runOpenAIEvalSampleAttempts(runCtx, target, fullPrompt, request.ReasoningEffort, maximum)
			record.ProbeID = probe.ID
			run.RequestCount += record.Attempts
			run.CompletedSamples++
			run.Samples = append(run.Samples, record)
			if result != nil {
				run.InputTokens += result.InputTokens
				run.OutputTokens += result.OutputTokens
			}
			if ctxErr := runCtx.Err(); ctxErr != nil {
				return finish(ctxErr)
			}
			if sampleErr != nil {
				markHardFailure(sampleErr)
				code := safeOpenAIEvalErrorCode(sampleErr)
				if run.Error == "" {
					run.Error = code
				}
				samples = append(samples, OpenAIEvalSample{ProbeID: probe.ID, Error: code})
				if run.CompletedSamples%5 == 0 || run.CompletedSamples == required {
					persistProgress()
				}
				continue
			}
			normalized, valid := NormalizeOpenAIEvalFingerprintAnswer(result.Text, probe)
			sample := OpenAIEvalSample{ProbeID: probe.ID, Answer: normalized}
			if !valid {
				sample.Error = "invalid_probe_answer"
				record.ErrorMessage = "completed response was not a valid answer for this probe"
			}
			samples = append(samples, sample)
			record.NormalizedAnswer, record.Valid, record.ErrorCode = normalized, valid, sample.Error
			run.Samples[len(run.Samples)-1] = record
			if run.CompletedSamples%5 == 0 || run.CompletedSamples == required {
				persistProgress()
			}
		}
	}
	result := ScoreOpenAIEvalFingerprint(request.RequestedModel, samples, OpenAIEvalFingerprintBaselines, required)
	run.Outcome = OpenAIEvalOutcome{Status: result.Status, Reason: result.Reason, SampleCount: result.ValidSamples, ExpectedCount: required, Confidence: "low", Scheduling: "alert_only", Fingerprint: &result}
	run.Status = result.Status
	return finish(nil)
}

func shouldRecordOpenAIEvalRouteHealth(testType string) bool {
	switch testType {
	case OpenAIEvalTypeCandy, OpenAIEvalTypeFingerprint, OpenAIEvalTypeModelTrace, OpenAIEvalTypeStateProbe:
		return false
	default:
		return true
	}
}

func isHardOpenAIEvalFailure(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "rate_limit", "invalid_api_key", "model_not_found", "previous_response_not_found", "timeout", "context_deadline_exceeded", "response_incomplete", "response_failed", "http_401", "http_403", "http_429", "http_5xx", "upstream_error":
		return true
	default:
		return false
	}
}

func (s *OpenAIEvalService) recordRouteHealth(ctx context.Context, accountID int64, model, effort string, hardFailure bool, failureCode string) {
	if s == nil || s.accounts == nil || !OpenAIEvalEffectsEnabled() || accountID <= 0 {
		return
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	account, err := s.accounts.GetByID(lookupCtx, accountID)
	if err != nil || account == nil {
		return
	}
	now := time.Now().UTC()
	health, _ := readOpenAIEvalRouteHealthStateFromAccount(account, model, effort)
	health = updateOpenAIEvalRouteHealth(health, accountID, model, effort, now, hardFailure, failureCode)
	payload, err := json.Marshal(health)
	if err != nil {
		return
	}
	_ = s.accounts.UpdateExtra(lookupCtx, accountID, map[string]any{OpenAIEvalRouteHealthExtraKeyFor(model, effort): json.RawMessage(payload)})
}

func updateOpenAIEvalRouteHealth(health OpenAIEvalRouteHealth, accountID int64, model, effort string, now time.Time, hardFailure bool, failureCode string) OpenAIEvalRouteHealth {
	if hardFailure {
		if !health.UpdatedAt.IsZero() && now.Sub(health.UpdatedAt) <= OpenAIEvalRouteHealthTTL {
			health.HardFailureStreak++
		} else {
			health.HardFailureStreak = 1
		}
		health.Penalty = 0
		health.PenaltyUntil = time.Time{}
		if health.HardFailureStreak >= 2 {
			health.Penalty = 1
			health.PenaltyUntil = now.Add(OpenAIEvalRouteHealthTTL)
		}
		health.LastFailureCode = failureCode
	} else {
		health.HardFailureStreak = 0
		health.Penalty = 0
		health.PenaltyUntil = time.Time{}
		health.LastFailureCode = ""
	}
	health.AccountID = accountID
	health.RequestedModel = strings.TrimSpace(model)
	health.ReasoningEffort = strings.TrimSpace(effort)
	health.UpdatedAt = now
	return health
}

func (s *OpenAIEvalService) ListRuns(ctx context.Context, filter OpenAIEvalRunFilter) ([]OpenAIEvalRun, error) {
	runs, err := s.repo.ListRuns(ctx, filter)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		normalizeOpenAIEvalAttributionRun(&runs[i])
	}
	return runs, nil
}

// Apply the current attribution policy when reading history without rewriting
// stored evidence. Failed/incomplete runs never become likely-normal results.
func normalizeOpenAIEvalAttributionRun(run *OpenAIEvalRun) {
	if run == nil {
		return
	}
	switch run.Status {
	case "attributed", "consistent", "different", "uncertain", "suspected_normal", "warning":
	default:
		return
	}
	model := ""
	switch run.TestType {
	case OpenAIEvalTypeModelTrace:
		if trace := run.Outcome.ModelTrace; trace != nil && trace.UsedOutputs > 0 {
			model = trace.Prediction
		}
	case OpenAIEvalTypeFingerprint:
		if fp := run.Outcome.Fingerprint; fp != nil && fp.MeanJSD != nil && fp.CellCount >= 4 && fp.ValidSamples >= fp.RequiredSamples {
			model = fp.NearestModel
		}
	default:
		return
	}
	if strings.TrimSpace(model) == "" {
		return
	}
	run.Status, run.Outcome.Reason = openAIEvalAttributionVerdict(model)
	run.Outcome.Status = run.Status
	run.Outcome.Confidence = "low"
	run.Outcome.Scheduling = "alert_only"
	if fp := run.Outcome.Fingerprint; run.TestType == OpenAIEvalTypeFingerprint && fp != nil {
		copy := *fp
		copy.Status, copy.Reason = run.Status, run.Outcome.Reason
		run.Outcome.Fingerprint = &copy
	}
}

func (s *OpenAIEvalService) ListAuditEvents(ctx context.Context, limit int) ([]OpenAIEvalAuditEvent, error) {
	return s.repo.ListAuditEvents(ctx, limit)
}

func (s *OpenAIEvalService) renewOpenAIEvalLease(ctx context.Context, key, owner string, lost *atomic.Bool, errMu *sync.Mutex, leaseErr *error, cancel context.CancelFunc) {
	ticker := time.NewTicker(OpenAIEvalRunRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewed, err := s.repo.RenewLease(ctx, key, owner, OpenAIEvalRunLeaseTTL)
			if err != nil || !renewed {
				lost.Store(true)
				if err == nil {
					err = errors.New("lease no longer belongs to this runner")
				}
				errMu.Lock()
				*leaseErr = err
				errMu.Unlock()
				cancel()
				return
			}
		}
	}
}

func (s *OpenAIEvalService) estimateRunCost(upstreamModel, requestedModel string, inputTokens, outputTokens int64) *float64 {
	if s == nil || s.pricing == nil {
		return nil
	}
	pricing := s.pricing.GetIdentifiedModelPricing(upstreamModel)
	if pricing == nil {
		pricing = s.pricing.GetIdentifiedModelPricing(requestedModel)
	}
	if pricing == nil || pricing.TokenPricingAbsent || pricing.InputCostPerToken <= 0 || pricing.OutputCostPerToken <= 0 {
		return nil
	}
	value := float64(inputTokens)*pricing.InputCostPerToken + float64(outputTokens)*pricing.OutputCostPerToken
	return &value
}

func openAIEvalRouteKey(accountID int64, model, effort string) string {
	return fmt.Sprintf("%d:%s:%s", accountID, strings.ToLower(strings.TrimSpace(model)), strings.ToLower(strings.TrimSpace(effort)))
}

func newOpenAIEvalLeaseOwner() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create evaluation lease owner: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func safeOpenAIEvalErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var classified *OpenAIEvalRequestError
	if errors.As(err, &classified) {
		return classified.Code
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	message := strings.ToLower(err.Error())
	for _, code := range []string{"rate_limit", "invalid_api_key", "model_not_found", "previous_response_not_found", "timeout", "context_deadline_exceeded", "response_incomplete", "response_failed", "http_401", "http_403", "http_429", "http_5xx"} {
		if strings.Contains(message, code) {
			return code
		}
	}
	return "upstream_error"
}

func openAIEvalMeanCost(inputTokens, outputTokens int64, inputPricePerMillion, outputPricePerMillion float64) float64 {
	if inputTokens < 0 || outputTokens < 0 || inputPricePerMillion < 0 || outputPricePerMillion < 0 {
		return math.NaN()
	}
	return (float64(inputTokens)*inputPricePerMillion + float64(outputTokens)*outputPricePerMillion) / 1_000_000
}
