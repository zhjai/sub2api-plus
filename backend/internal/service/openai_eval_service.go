package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/Wei-Shaw/sub2api/internal/service/cpaeval"
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
	OpenAIEvalMaxFingerprintRequests = cpaeval.FingerprintTarget
	OpenAIEvalMinStateProbeInterval  = 5 * time.Minute
)

type OpenAIEvalRunRequest struct {
	Concurrency     int    `json:"concurrency,omitempty"`
	Variant         string `json:"variant,omitempty"`
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

// OpenAIEvalDetectorScheduleRepository reconciles legacy automatic detector
// plans with persisted scheduler state before any scheduled evaluation starts.
type OpenAIEvalDetectorScheduleRepository interface {
	ReconcileDetectorSchedules(context.Context) error
}

// Initialize restores the process-wide scheduling-effects switch before the
// first admin request or scheduled evaluation runs. A failed read leaves the
// safe default unchanged; callers decide whether startup should be fatal.
func (s *OpenAIEvalService) Initialize(ctx context.Context) error {
	if s == nil || s.repo == nil {
		return errors.New("OpenAI evaluation service is unavailable")
	}
	if reconciler, ok := s.repo.(OpenAIEvalDetectorScheduleRepository); ok {
		if err := reconciler.ReconcileDetectorSchedules(ctx); err != nil {
			return fmt.Errorf("reconcile detector schedules: %w", err)
		}
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
	NormalizeOpenAIEvalDetectorConfig(config)
	NormalizeOpenAIEvalModelPolicies(config)
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
		NormalizeOpenAIEvalDetectorConfig(config)
		NormalizeOpenAIEvalModelPolicies(config)
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
	beforeDetectorConfig, _ := json.Marshal(config.Accounts)
	RetireOpenAIEvalBPS(config)
	NormalizeOpenAIEvalDetectorConfig(config)
	NormalizeOpenAIEvalModelPolicies(config)
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
		item.RequestedModel = strings.TrimSpace(item.RequestedModel)
		// Saving a target must not depend on the bundled detector/catalog
		// version. Unknown models keep their configuration; normalization above
		// disables automatic fingerprinting and explains it in Warnings.
		if item.AccountID <= 0 || item.RequestedModel == "" || len(item.RequestedModel) > 200 || strings.ContainsAny(item.RequestedModel, "\x00\r\n\t ") {
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
	afterDetectorConfig, _ := json.Marshal(config.Accounts)
	if string(beforeDetectorConfig) != string(afterDetectorConfig) {
		// The repository save is already committed; an audit transport failure
		// cannot safely be reported as a failed configuration save.
		if auditErr := s.repo.RecordAuditEvent(ctx, actorID, "evaluation_detector_config_normalized", map[string]any{"before": json.RawMessage(beforeDetectorConfig), "after": json.RawMessage(afterDetectorConfig), "warnings": config.Warnings}); auditErr != nil {
			logger.LegacyPrintf("service.openai_eval", "[OpenAI Eval] normalization audit failed: %v", auditErr)
		}
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
		schedule.SampleMode = ""
		var err error
		schedule.Concurrency, err = normalizeOpenAIEvalFingerprintConcurrency(schedule.Concurrency)
		if err != nil {
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
	if request.AccountID <= 0 || !isOpenAIEvalSupportedModel(request.RequestedModel) {
		return nil, errors.New("a supported OpenAI model and account are required")
	}
	if request.ReasoningEffort != "" && !isAllowedOpenAIEvalReasoningEffort(request.ReasoningEffort) {
		return nil, fmt.Errorf("unsupported reasoning effort %q", request.ReasoningEffort)
	}
	if request.TestType == OpenAIEvalTypeModelTrace {
		return nil, errors.New("ModelTrace has been retired; use fingerprint")
	}
	if request.TestType != OpenAIEvalTypeCandy && request.TestType != OpenAIEvalTypeFingerprint && request.TestType != OpenAIEvalTypePelican && request.TestType != OpenAIEvalTypeStateProbe {
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
		var err error
		request.Concurrency, err = normalizeOpenAIEvalFingerprintConcurrency(request.Concurrency)
		if err != nil {
			return nil, err
		}
		request.SampleMode = ""
		if source == "scheduled" && !cpaeval.ModelEnrolled(request.RequestedModel) {
			return nil, errors.New("automatic fingerprint requires an enrolled model")
		}
	}
	if request.TestType == OpenAIEvalTypePelican {
		if source != "manual" {
			return nil, errors.New("Pelican is manual only")
		}
		if request.Variant == "" {
			request.Variant = "original"
		}
		if _, ok := cpaeval.FindPelicanVariant(request.Variant); !ok {
			return nil, fmt.Errorf("unsupported Pelican variant %q", request.Variant)
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

	start := time.Now().UTC()
	expectedSamples := request.SampleCount
	switch request.TestType {
	case OpenAIEvalTypeFingerprint:
		expectedSamples, _ = OpenAIEvalFingerprintSampleCount(request.SampleMode)
	case OpenAIEvalTypePelican:
		expectedSamples = 1
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
	runCtx = withOpenAIEvalKind(controlledCtx, request.TestType)
	run := &OpenAIEvalRun{
		AccountID: request.AccountID, TestType: request.TestType,
		RequestedModel: request.RequestedModel, UpstreamModel: target.UpstreamModel,
		ReasoningEffort: request.ReasoningEffort, DataVersion: OpenAIEvalDataVersion, BaselineVersion: OpenAIEvalBaselineVersion,
		Status: "running", StartedAt: start, TriggeredBy: actorID, TriggerSource: source,
		Outcome:         OpenAIEvalOutcome{Status: "running", Reason: "sampling", SampleCount: 0, ExpectedCount: expectedSamples, Confidence: "none", Scheduling: "disabled"},
		SampleCount:     expectedSamples,
		ExpectedSamples: expectedSamples,
		Phase:           "sampling",
		Samples:         make([]OpenAIEvalSampleRecord, 0),
		// Account activity is a validity boundary. Scheduling participation is
		// only a boundary for automatic runs; a manual administrator evaluation
		// remains quality evidence when routing is paused.
		DiagnosticOnly: request.TestType == OpenAIEvalTypePelican || !target.Account.IsActive() || (source == "scheduled" && !target.Account.Schedulable),
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
		lmFingerprint, pelican := run.Outcome.LMFingerprint, run.Outcome.Pelican
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
		run.Outcome.LMFingerprint, run.Outcome.Pelican = lmFingerprint, pelican
		if request.TestType == OpenAIEvalTypePelican {
			run.DiagnosticOnly = true
			if runErr != nil || deferred || run.Status != "completed" {
				run.Status, run.Outcome.Status = "failed", "failed"
				run.PelicanHTML = ""
				if run.Outcome.Pelican != nil {
					run.Outcome.Pelican.HTMLBytes = 0
					run.Outcome.Pelican.HTMLSHA256 = ""
				}
			}
			run.Outcome.Scheduling = "disabled"
		}
		if target.Credential.IsOpenAIOAuthLike() && request.TestType != OpenAIEvalTypeStateProbe {
			run.Outcome.RequestProfile = openAIEvalCodexRequestProfile
		} else if run.Protocol == "responses" && request.TestType != OpenAIEvalTypeStateProbe {
			run.Outcome.RequestProfile = openAIEvalAPIKeyCodexRequestProfile
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
			record.ProbeID = fmt.Sprintf("candy-179d6dae-%d", i+1)
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
			if cpaeval.HasStandalone21(result.Text) {
				run.Samples[len(run.Samples)-1].NormalizedAnswer = "21"
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

	if request.TestType == OpenAIEvalTypePelican {
		variant, _ := cpaeval.FindPelicanVariant(request.Variant)
		pelicanCtx := withOpenAIEvalAccept(runCtx, func(answer string) error {
			page, err := variant.Page(answer)
			if err == nil && len(page) > 300<<10 {
				return errors.New("Pelican HTML exceeds 300 KiB")
			}
			return err
		})
		response, record, sampleErr := s.accountTest.runOpenAIEvalSampleAttempts(pelicanCtx, target, variant.Prompt, request.ReasoningEffort, maximum)
		record.ProbeID = "pelican-" + variant.ID
		record.Answer = "" // only the independent artifact table contains the page
		run.Samples = append(run.Samples, record)
		run.RequestCount, run.CompletedSamples = record.Attempts, 1
		run.Outcome = OpenAIEvalOutcome{Status: "failed", Confidence: "none", Scheduling: "disabled", ExpectedCount: 1,
			Pelican: &OpenAIEvalPelicanResult{Variant: variant.ID, Attempts: record.Attempts}}
		run.Status = "failed"
		if response != nil {
			run.InputTokens, run.OutputTokens = response.InputTokens, response.OutputTokens
		}
		if sampleErr == nil && response != nil {
			page, pageErr := variant.Page(response.Text)
			if pageErr == nil && len(page) > 300<<10 {
				pageErr = errors.New("Pelican HTML exceeds 300 KiB")
			}
			if pageErr != nil {
				sampleErr = &OpenAIEvalRequestError{Code: "invalid_pelican_page", Message: pageErr.Error(), Attempted: true}
				run.Samples[0].Valid = false
				run.Samples[0].ErrorCode, run.Samples[0].ErrorMessage = "invalid_pelican_page", sanitizeOpenAIEvalText(pageErr.Error(), openAIEvalErrorLimit)
			} else {
				run.PelicanHTML = page
				run.Outcome.Pelican.HTMLBytes = len(page)
				digest := sha256.Sum256([]byte(page))
				run.Outcome.Pelican.HTMLSHA256 = hex.EncodeToString(digest[:])
				run.Status, run.Outcome.Status, run.Outcome.SampleCount = "completed", "completed", 1
			}
		}
		if sampleErr != nil {
			run.Error = safeOpenAIEvalErrorCode(sampleErr)
			run.Outcome.Reason = run.Error
		}
		if err := runCtx.Err(); err != nil {
			return finish(err)
		}
		return finish(nil)
	}

	result := s.collectOpenAIEvalFingerprint(runCtx, target, request.RequestedModel, request.Concurrency, maximum, run, persistProgress)
	run.Outcome = openAIEvalLMFingerprintOutcome(request.RequestedModel, result)
	run.Status = run.Outcome.Status
	if err := runCtx.Err(); err != nil {
		return finish(err)
	}
	return finish(nil)
}

func shouldRecordOpenAIEvalRouteHealth(testType string) bool {
	switch testType {
	case OpenAIEvalTypeCandy, OpenAIEvalTypeFingerprint, OpenAIEvalTypeModelTrace, OpenAIEvalTypeStateProbe, OpenAIEvalTypePelican:
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
	health.ReasoningEffort = ""
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
	if run == nil || run.Outcome.LMFingerprint != nil {
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

// NormalizeOpenAIEvalDetectorConfig projects legacy identity schedules onto
// the new detector. Call on a copy when reading persisted configuration.
func NormalizeOpenAIEvalDetectorConfig(config *OpenAIEvalConfig) {
	if config == nil {
		return
	}
	config.Warnings = nil
	for i := range config.Accounts {
		route := &config.Accounts[i]
		route.FingerprintSchedule = openAIEvalDetectorSchedule(*route)
		route.ModelTraceSchedule.Enabled = false
		route.FingerprintSchedule.SampleMode = ""
		route.FingerprintSchedule.SampleCount = 0
		if route.FingerprintSchedule.Concurrency == 0 {
			route.FingerprintSchedule.Concurrency = cpaeval.FingerprintTarget
		}
		if !cpaeval.ModelEnrolled(route.RequestedModel) {
			route.FingerprintSchedule.Enabled = false
			config.Warnings = append(config.Warnings, fmt.Sprintf("account %d model %s: automatic fingerprint disabled (model_not_enrolled)", route.AccountID, route.RequestedModel))
		}
	}
}

func openAIEvalDetectorSchedule(route OpenAIEvalAccountConfig) OpenAIEvalSchedule {
	schedule := route.FingerprintSchedule
	if !schedule.Enabled && route.ModelTraceSchedule.Enabled {
		schedule = route.ModelTraceSchedule
	}
	if !cpaeval.ModelEnrolled(route.RequestedModel) {
		schedule.Enabled = false
	}
	return schedule
}

func normalizeOpenAIEvalFingerprintConcurrency(n int) (int, error) {
	if n == 0 {
		return cpaeval.FingerprintTarget, nil
	}
	if n < 1 || n > cpaeval.FingerprintTarget {
		return 0, errors.New("fingerprint concurrency must be between 1 and 3")
	}
	return n, nil
}

// A challenge holds a process-wide slot through its bounded retries, exactly
// as the plugin does. Every physical send also takes the existing account/RPM
// permits inside RunOpenAIEvalSample.
var openAIEvalFingerprintSlots = make(chan struct{}, cpaeval.FingerprintGlobalConcurrency)

func (s *OpenAIEvalService) collectOpenAIEvalFingerprint(ctx context.Context, target *OpenAIEvalTarget, model string, concurrency, maximum int, run *OpenAIEvalRun, progress func()) *OpenAIEvalLMFingerprintResult {
	challenges := cpaeval.FingerprintChallenges()
	result := &OpenAIEvalLMFingerprintResult{
		DetectorRevision: cpaeval.DetectorRevision(), Concurrency: concurrency,
		ComparedAs: cpaeval.FingerprintModelID(model), Enrolled: cpaeval.ModelEnrolled(model),
		Samples: make([]OpenAIEvalLMFingerprintSample, len(challenges)),
	}
	type collected struct {
		index    int
		sample   OpenAIEvalLMFingerprintSample
		record   OpenAIEvalSampleRecord
		response *OpenAIEvalSampleResponse
	}
	jobs := make(chan int, len(challenges))
	results := make(chan collected, len(challenges))
	for i := range challenges {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				challenge := challenges[index]
				item := collected{index: index, sample: OpenAIEvalLMFingerprintSample{FingerprintChallenge: challenge}}
				sampleCtx := withOpenAIEvalInstructions(ctx, challenge.Instructions)
				sampleCtx = withOpenAIEvalAccept(sampleCtx, func(text string) error {
					_, _, err := cpaeval.FingerprintText(text, challenge.ExpectedCount)
					return err
				})
				var err error
				select {
				case openAIEvalFingerprintSlots <- struct{}{}:
					item.response, item.record, err = s.accountTest.runOpenAIEvalSampleAttempts(sampleCtx, target, challenge.Prompt, "", maximum)
					<-openAIEvalFingerprintSlots
				case <-ctx.Done():
					err = openAIEvalIOError(ctx, ctx.Err(), 0)
					item.record.ErrorCode, item.record.ErrorMessage = safeOpenAIEvalErrorCode(err), sanitizeOpenAIEvalText(err.Error(), openAIEvalErrorLimit)
				}
				item.record.ProbeID = fmt.Sprintf("lm-fingerprint-%d", index+1)
				item.sample.Attempts = item.record.Attempts
				if item.response != nil {
					var textErr error
					item.sample.Text, item.sample.ParsedNumbers, textErr = cpaeval.FingerprintText(item.response.Text, challenge.ExpectedCount)
					item.sample.Accepted = err == nil && textErr == nil
				}
				if err != nil {
					item.sample.Error = item.record.ErrorMessage
				}
				item.record.Valid = item.sample.Accepted
				results <- item
			}
		}()
	}
	go func() { workers.Wait(); close(results) }()
	records := make([]OpenAIEvalSampleRecord, len(challenges))
	outputs := make([]cpaeval.FingerprintOutput, len(challenges))
	for item := range results {
		result.Samples[item.index] = item.sample
		records[item.index] = item.record
		// Failed transport/tool responses must never enter attribution merely
		// because they contain a numeric prefix. Invalid completed text is kept
		// in the sample diagnostics but contributes no evidence.
		if item.sample.Accepted {
			outputs[item.index].Text = item.sample.Text
		}
		outputs[item.index].ExpectedCount = item.sample.ExpectedCount
		run.RequestCount += item.record.Attempts
		if item.record.Attempts > 0 {
			run.CompletedSamples++
		}
		if item.response != nil {
			run.InputTokens += item.response.InputTokens
			run.OutputTokens += item.response.OutputTokens
		}
		run.Samples = append(run.Samples, item.record)
		progress()
	}
	run.Samples = records
	attribution, err := cpaeval.AnalyzeFingerprint(outputs)
	result.Attribution = attribution
	result.CollectionStatus = "failed"
	if attribution.Used == cpaeval.FingerprintTarget {
		result.CollectionStatus = "completed"
	} else if attribution.Used > 0 {
		result.CollectionStatus = "partial"
	}
	if err != nil && run.Error == "" {
		for _, sample := range run.Samples {
			if sample.ErrorCode != "" {
				run.Error = sample.ErrorCode
				break
			}
		}
		if run.Error == "" {
			run.Error = "insufficient_valid_samples"
		}
	}
	return result
}

func openAIEvalLMFingerprintOutcome(model string, result *OpenAIEvalLMFingerprintResult) OpenAIEvalOutcome {
	outcome := OpenAIEvalOutcome{Status: "insufficient", Reason: "insufficient_valid_samples", Confidence: "none", Scheduling: "alert_only", ExpectedCount: cpaeval.FingerprintTarget, LMFingerprint: result}
	if result == nil {
		return outcome
	}
	outcome.SampleCount = result.Attribution.Used
	if !cpaeval.ModelEnrolled(model) {
		outcome.Reason = "model_not_enrolled"
		return outcome
	}
	if result.Attribution.Used <= 0 {
		return outcome
	}
	outcome.Confidence = "low"
	if result.Attribution.Decision == "not_model" {
		outcome.Status, outcome.Reason = "warning", "not_model"
	} else if result.Attribution.Prediction != "" {
		outcome.Status, outcome.Reason = "warning", "fingerprint_target_mismatch"
		if cpaeval.FingerprintModelID(result.Attribution.Prediction) == cpaeval.FingerprintModelID(model) {
			outcome.Status, outcome.Reason, outcome.Score = "pass", "fingerprint_target_match", 1
		}
	}
	return outcome
}
