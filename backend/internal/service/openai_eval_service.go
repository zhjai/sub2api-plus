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
	copy := *config
	config = &copy
	config.Accounts = append([]OpenAIEvalAccountConfig(nil), config.Accounts...)
	RetireOpenAIEvalBPS(config)
	if err := s.pruneDeletedAccountReferences(ctx, config, nil); err != nil {
		return err
	}
	if err := normalizeOpenAIEvalQualityConfig(config); err != nil {
		return err
	}
	if err := SetOpenAIEvalSchedulingPolicySnapshot(config); err != nil && !errors.Is(err, ErrOpenAIEvalQualityRefreshSuperseded) {
		return err
	}
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
		// Work on a projection; legacy cleanup must not mutate stored config on read.
		copy := *config
		config = &copy
		config.Accounts = append([]OpenAIEvalAccountConfig(nil), config.Accounts...)
		RetireOpenAIEvalBPS(config)
		if err := s.pruneDeletedAccountReferences(ctx, config, nil); err != nil {
			return nil, err
		}
		if err := s.pruneBackgroundControls(ctx, config, nil); err != nil {
			return nil, err
		}
		if err := s.projectBackgroundRuntime(ctx, config); err != nil {
			return nil, err
		}
		if normalizeErr := normalizeOpenAIEvalQualityConfig(config); normalizeErr != nil {
			return nil, normalizeErr
		}
		if config.MaxRequestAttempts == 0 {
			config.MaxRequestAttempts = OpenAIEvalDefaultMaxRequestAttempts
		}
		if s.ranking == nil {
			if err := SetOpenAIEvalSchedulingPolicySnapshot(config); err != nil && !errors.Is(err, ErrOpenAIEvalQualityRefreshSuperseded) {
				return nil, err
			}
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
				if account != nil {
					route.DirectOAuthEligible = account.IsOpenAIOAuth() && !account.IsShadow() && !account.IsSyntheticUITest() && !account.IsOpenAIAgentIdentity()
				}
			}

		}
	}
	return config, err
}

func (s *OpenAIEvalService) SaveConfig(ctx context.Context, config *OpenAIEvalConfig, actorID int64) error {
	if config == nil {
		return errors.New("evaluation config is required")
	}
	RetireOpenAIEvalBPS(config)
	if s.accounts != nil {
		previous, err := s.repo.GetConfig(ctx)
		if err != nil {
			return fmt.Errorf("load evaluation config for account validation: %w", err)
		}
		if previous != nil && config.Revision != 0 && config.Revision < previous.Revision {
			return ErrOpenAIEvalConfigRevisionConflict
		}
		if err := s.pruneDeletedAccountReferences(ctx, config, openAIEvalReferencedAccounts(previous)); err != nil {
			return err
		}
		allowed := map[int64]bool{}
		if previous != nil {
			for _, c := range previous.BackgroundControls {
				allowed[c.AccountID] = true
			}
		}
		if err := s.pruneBackgroundControls(ctx, config, allowed); err != nil {
			return err
		}
	}
	if err := s.validateBackgroundControls(ctx, config); err != nil {
		return err
	}
	rules, rulesErr := normalizeOpenAIEvalAccountPriorityRules(config.AccountPriorityRules)
	if rulesErr != nil {
		return rulesErr
	}
	config.AccountPriorityRules = rules
	checkedAccounts := make(map[int64]bool)
	for index, rule := range rules {
		if (rule.Enabled != nil && !*rule.Enabled) || checkedAccounts[rule.AccountID] {
			continue
		}
		if s.accounts == nil {
			return errors.New("account lookup is unavailable for account priority rule validation")
		}
		account, accountErr := s.accounts.GetByID(ctx, rule.AccountID)
		if accountErr != nil || account == nil {
			return fmt.Errorf("account priority rule %d references an unavailable account %d", index, rule.AccountID)
		}
		checkedAccounts[rule.AccountID] = true
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
	for i := range config.Policies {
		rule := &config.Policies[i]
		if strings.TrimSpace(rule.RequestedModel) == "" || len(rule.RequestedModel) > 200 || strings.ContainsAny(rule.RequestedModel, "\x00\r\n\t") {
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
		item.BPSMode = OpenAIEvalBPSModeForceOff
		item.BPSAuto = false
		prismRoute, prismErr := s.validatePrismEvalRoute(ctx, item.AccountID, item.RequestedModel, item.ReasoningEffort)
		if prismErr != nil {
			return fmt.Errorf("Prism evaluation route %d: %w", i, prismErr)
		}
		if item.AccountID <= 0 || (!prismRoute && !isOpenAIEvalSupportedModel(item.RequestedModel)) {
			return fmt.Errorf("invalid evaluation route at index %d", i)
		}
		if effort := strings.TrimSpace(item.ReasoningEffort); !prismRoute && effort != "" && !isAllowedOpenAIEvalReasoningEffort(effort) {
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
		if err := SetOpenAIEvalSchedulingPolicySnapshot(config); err != nil && !errors.Is(err, ErrOpenAIEvalQualityRefreshSuperseded) {
			return err
		}
	}
	return nil
}

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
	defer openAIEvalAdmissionFinished(ctx)
	if s == nil || s.repo == nil || s.accountTest == nil {
		return nil, errors.New("OpenAI evaluation service is unavailable")
	}
	request.TestType = strings.ToLower(strings.TrimSpace(request.TestType))
	request.RequestedModel = strings.TrimSpace(request.RequestedModel)
	request.ReasoningEffort = strings.TrimSpace(request.ReasoningEffort)
	if source == "scheduled" && request.ReasoningEffort == OpenAIEvalBPSAccountEffort {
		return nil, errors.New("BPS automatic probes have been retired; configure State Probe on a test target instead")
	}
	prismRoute, prismErr := s.validatePrismEvalRoute(ctx, request.AccountID, request.RequestedModel, request.ReasoningEffort)
	if prismErr != nil {
		return nil, prismErr
	}
	if request.AccountID <= 0 || (!prismRoute && !isOpenAIEvalSupportedModel(request.RequestedModel)) {
		return nil, errors.New("a supported OpenAI model and account are required")
	}
	if !prismRoute && request.ReasoningEffort != "" && !isAllowedOpenAIEvalReasoningEffort(request.ReasoningEffort) {
		return nil, fmt.Errorf("unsupported reasoning effort %q", request.ReasoningEffort)
	}
	if request.TestType != OpenAIEvalTypeCandy && request.TestType != OpenAIEvalTypeFingerprint && request.TestType != OpenAIEvalTypeModelTrace && request.TestType != OpenAIEvalTypeStateProbe {
		return nil, fmt.Errorf("unsupported OpenAI evaluation type %q", request.TestType)
	}
	if source != "manual" && source != "scheduled" {
		return nil, fmt.Errorf("unsupported evaluation trigger source %q", source)
	}
	if source == "scheduled" {
		ctx = context.WithValue(ctx, openAIEvalAutomaticKey{}, true)
		if err := s.accountTest.checkOpenAIEvalAutomaticAccount(ctx, &Account{ID: request.AccountID}); err != nil {
			return nil, err
		}
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
	// A state probe is a linked mint/continue pair. Keep its bounded retry
	// contract at three chains (six physical sends) even if the shared eval
	// retry setting is higher.
	if request.TestType == OpenAIEvalTypeStateProbe && maximum > 3 {
		maximum = 3
	}
	target, err := s.accountTest.ResolveOpenAIEvalTarget(ctx, request.AccountID, request.RequestedModel)
	if err != nil {
		return nil, err
	}
	leaseKey := openAIEvalRouteKey(request.AccountID, request.RequestedModel, request.ReasoningEffort) + ":" + request.TestType
	if source == "scheduled" {
		namespace, namespaceErr := s.backgroundNamespace(ctx, request.AccountID)
		if namespaceErr != nil {
			return nil, namespaceErr
		}
		if namespace == "" {
			return nil, errors.New("background credential namespace unavailable")
		}
		leaseKey = "background:" + namespace + ":" + request.TestType
	}
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
	go func(leaseCtx context.Context) {
		defer close(renewDone)
		s.renewOpenAIEvalLease(leaseCtx, leaseKey, owner, &lostLease, &leaseErrMu, &leaseErr, cancelRun)
	}(runCtx)
	defer func() {
		cancelRun()
		<-renewDone
	}()

	if request.TestType == OpenAIEvalTypeStateProbe && !isOpenAIStateProbeTarget(target) {
		return nil, errors.New("State Probe requires a direct OpenAI OAuth account")
	}
	if prismRoute && !prismEvalBaselineSupported(request.TestType, target.UpstreamModel) {
		return nil, errors.New("unsupported Prism evaluation: requested model has no applicable versioned baseline")
	}
	start := time.Now().UTC()
	expectedSamples := request.SampleCount
	switch request.TestType {
	case OpenAIEvalTypeFingerprint:
		expectedSamples, _ = OpenAIEvalFingerprintSampleCount(request.SampleMode)
	case OpenAIEvalTypeModelTrace:
		expectedSamples = OpenAIEvalModelTraceRequests
	case OpenAIEvalTypeStateProbe:
		expectedSamples = 2 * maximum
	}
	// A successful state probe needs one pair; later pairs are retries and
	// charge additional available budget rather than reserving worst-case work.
	nominal := expectedSamples
	if request.TestType == OpenAIEvalTypeStateProbe {
		nominal = 2
	}
	controlledCtx, releaseBudget, err := s.beginBackgroundRun(runCtx, target, request, source, owner, nominal)
	if err != nil {
		return nil, err
	}
	defer releaseBudget()
	openAIEvalAdmissionFinished(ctx)
	runCtx = controlledCtx
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
		// Account activity is a validity boundary. Scheduling participation is
		// only a boundary for automatic runs; a manual administrator evaluation
		// remains quality evidence when routing is paused.
		DiagnosticOnly: !target.Account.IsActive() || (source == "scheduled" && !target.Account.Schedulable),
	}
	run.Protocol = "responses"
	if !target.Credential.IsOpenAIOAuthLike() && shouldForwardOpenAIResponsesViaRawChatCompletions(target.Account) {
		run.Protocol = "chat_completions"
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
		if s.accounts != nil {
			if source == "scheduled" {
				if latest, readErr := s.accounts.GetByID(context.WithoutCancel(ctx), target.Account.ID); readErr == nil && latest != nil && (!latest.IsActive() || !latest.Schedulable) {
					run.DiagnosticOnly = true
				}
			}
		}
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
		diagnostics := openAIEvalRunDiagnostics(runCtx)
		deferred := diagnostics.DeferredReason != "" || isOpenAIEvalDeferredCode(run.Error) || lostLease.Load() || runCtx.Err() != nil
		for _, sample := range run.Samples {
			deferred = deferred || isOpenAIEvalDeferredCode(sample.ErrorCode)
		}
		if controller, _ := runCtx.Value(openAIEvalControllerKey{}).(*openAIEvalController); controller != nil && !controller.deadline.IsZero() && !time.Now().Before(controller.deadline) {
			deferred = true
			diagnostics.DeferredReason = "sampling_window_exhausted"
		}
		if deferred {
			run.DiagnosticOnly = true
			run.Status = "inconclusive"
			if diagnostics.DeferredReason != "" {
				run.Error = diagnostics.DeferredReason
			}
			run.Outcome = OpenAIEvalOutcome{Status: "inconclusive", Reason: run.Error, SampleCount: run.CompletedSamples, ExpectedCount: run.ExpectedSamples, Confidence: "none", Scheduling: "disabled"}
		}
		if target.Credential.IsOpenAIOAuthLike() && request.TestType != OpenAIEvalTypeStateProbe {
			run.Outcome.RequestProfile = openAIEvalCodexRequestProfile
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
		if qualityErr := func() error {
			if run.DiagnosticOnly {
				return nil
			}
			return s.recordOpenAIEvalQualityResult(finishCtx, runID, run)
		}(); qualityErr != nil {
			logger.LegacyPrintf("service.openai_eval", "[OpenAI Eval] quality update failed run=%d: %v", runID, qualityErr)
		}
		// Diagnostic quality changes preference only. It must not fabricate
		// operational health failures or hard-exclude a production route.
		if !run.DiagnosticOnly && shouldRecordOpenAIEvalRouteHealth(request.TestType) {
			s.recordRouteHealth(finishCtx, target.Account.ID, request.RequestedModel, request.ReasoningEffort, hardFailure, hardFailureCode)
		}
		return run, runErr
	}
	if request.TestType == OpenAIEvalTypeStateProbe {
		probe := s.accountTest.RunOpenAIStateProbeAttempts(runCtx, target, maximum)
		run.RequestCount = probe.RequestCount
		run.Samples = probe.Samples
		run.CompletedSamples = len(probe.Samples)
		run.Outcome = OpenAIEvalOutcome{Status: probe.Verdict, Reason: probe.Failure, SampleCount: probe.RequestCount, ExpectedCount: expectedSamples, Confidence: "low", Scheduling: "alert_only", StateProbe: probe}
		run.Status = probe.Verdict
		return finish(nil) // State Probe is diagnostic only; never switches routes.
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
				if isOpenAIEvalDeferredCode(record.ErrorCode) {
					run.Error = record.ErrorCode
					return finish(nil)
				}
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
			run.Outcome = modelTraceSchedulingOutcome(openAIEvalBaselineModel(target, run.RequestedModel), trace, traceErr)
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
				if isOpenAIEvalDeferredCode(code) {
					run.Error = code
					return finish(nil)
				}
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
	result := ScoreOpenAIEvalFingerprint(openAIEvalBaselineModel(target, request.RequestedModel), samples, OpenAIEvalFingerprintBaselines, required)
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
	if s == nil || s.accounts == nil || accountID <= 0 {
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
	case "pass":
		if run.TestType != OpenAIEvalTypeModelTrace {
			return
		}
	case "attributed", "consistent", "different", "uncertain", "suspected_normal", "warning":
	default:
		return
	}
	model := ""
	switch run.TestType {
	case OpenAIEvalTypeModelTrace:
		if trace := run.Outcome.ModelTrace; openAIEvalModelTraceUsedOutputsValid(trace) {
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
	if run.TestType == OpenAIEvalTypeModelTrace {
		metadata := OpenAIEvalAttributionPolicy{OriginalStatus: run.Status, OriginalReason: run.Outcome.Reason, OriginalRuleVersion: "legacy-unversioned"}
		if run.Outcome.Attribution != nil {
			metadata = *run.Outcome.Attribution
		}
		metadata.RuleVersion = OpenAIEvalModelTraceRuleVersion
		run.Outcome.Attribution = &metadata
		run.Status, run.Outcome.Reason = openAIEvalModelTraceVerdict(run.RequestedModel, model)
	} else {
		run.Status, run.Outcome.Reason = openAIEvalAttributionVerdict(model)
	}
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
