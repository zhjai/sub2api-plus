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
)

const (
	OpenAIEvalMinCandyInterval       = 15 * time.Minute
	OpenAIEvalRunLeaseTTL            = 20 * time.Minute
	OpenAIEvalRunRenewInterval       = 10 * time.Minute
	OpenAIEvalRunMaxDuration         = 24 * time.Hour
	OpenAIEvalMaxFingerprintRequests = 400
	OpenAIEvalMinStateProbeInterval  = 6 * time.Hour
)

type OpenAIEvalRunRequest struct {
	AccountID       int64  `json:"account_id"`
	TestType        string `json:"test_type"`
	RequestedModel  string `json:"requested_model"`
	ReasoningEffort string `json:"reasoning_effort"`
	SampleMode      string `json:"sample_mode,omitempty"`
}

type OpenAIEvalService struct {
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
		SetOpenAIEvalEffectsEnabled(false)
		return nil
	}
	SetOpenAIEvalEffectsEnabled(config.EffectsEnabled)
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
		SetOpenAIEvalEffectsEnabled(config.EffectsEnabled)
		if s.accounts != nil {
			accountCache := make(map[int64]*Account)
			for i := range config.Accounts {
				route := &config.Accounts[i]
				if route.ReasoningEffort != "" {
					continue
				}
				account, ok := accountCache[route.AccountID]
				if !ok {
					account, _ = s.accounts.GetByID(ctx, route.AccountID)
					accountCache[route.AccountID] = account
				}
				if account != nil && account.IsOpenAIOAuth() {
					state := readOpenAIBPSModelState(account, route.RequestedModel)
					route.BPSState = &state
				}
				if account != nil {
					route.DirectOAuthEligible = account.IsOpenAIOAuth() && !account.IsShadow() && !account.IsSyntheticUITest() && !account.IsOpenAIAgentIdentity() && route.ReasoningEffort == ""
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
	if len(config.Accounts) > 5000 {
		return errors.New("evaluation config exceeds the 5000 account-model-effort route limit")
	}
	seen := make(map[string]struct{}, len(config.Accounts))
	for i := range config.Accounts {
		item := &config.Accounts[i]
		item.BPSState = nil              // runtime status is read-only, never persisted in route config
		item.DirectOAuthEligible = false // derived from the current account, never persisted
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
		if err := validateOpenAIEvalSchedule(&item.FingerprintSchedule, OpenAIEvalTypeFingerprint); err != nil {
			return fmt.Errorf("route %d Fingerprint schedule: %w", i, err)
		}
		if err := validateOpenAIEvalSchedule(&item.ModelTraceSchedule, OpenAIEvalTypeModelTrace); err != nil {
			return fmt.Errorf("route %d ModelTrace schedule: %w", i, err)
		}
		if err := validateOpenAIEvalSchedule(&item.StateProbeSchedule, OpenAIEvalTypeStateProbe); err != nil {
			return fmt.Errorf("route %d State Probe schedule: %w", i, err)
		}
		if (item.StateProbeSchedule.Enabled || item.BPSAuto) && item.ReasoningEffort != "" {
			return fmt.Errorf("route %d State Probe and BPS auto policy require the default reasoning-effort route", i)
		}
		if item.StateProbeSchedule.Enabled || item.BPSAuto {
			if s.accounts == nil {
				return errors.New("account lookup is unavailable for direct OAuth route validation")
			}
			account, accountErr := s.accounts.GetByID(ctx, item.AccountID)
			if accountErr != nil || account == nil || !account.IsOpenAIOAuth() || account.IsShadow() || account.IsSyntheticUITest() || account.IsOpenAIAgentIdentity() {
				return fmt.Errorf("route %d State Probe and BPS require a direct OpenAI OAuth account", i)
			}
		}
	}
	if err := s.repo.SaveConfig(ctx, config, actorID); err != nil {
		return err
	}
	SetOpenAIEvalEffectsEnabled(config.EffectsEnabled)
	return nil
}

func validateOpenAIEvalSchedule(schedule *OpenAIEvalSchedule, testType string) error {
	if !schedule.Enabled {
		return nil
	}
	minimum := int(OpenAIEvalMinCandyInterval.Seconds())
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
	if schedule.IntervalSeconds < minimum || schedule.IntervalSeconds > 30*24*3600 {
		return fmt.Errorf("interval must be between %d seconds and 30 days", minimum)
	}
	if schedule.JitterSeconds < 0 || schedule.JitterSeconds >= schedule.IntervalSeconds-minimum+1 {
		return errors.New("jitter must preserve the configured minimum interval")
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

	if request.TestType == OpenAIEvalTypeStateProbe {
		// Ticket identity belongs to the OAuth account and model, not effort.
		request.ReasoningEffort = ""
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

	target, err := s.accountTest.ResolveOpenAIEvalTarget(runCtx, request.AccountID, request.RequestedModel)
	if err != nil {
		return nil, err
	}
	if request.TestType == OpenAIEvalTypeStateProbe && !isOpenAIStateProbeTarget(target) {
		return nil, errors.New("State Probe requires a direct OpenAI OAuth account")
	}
	start := time.Now().UTC()
	run := &OpenAIEvalRun{
		AccountID: request.AccountID, TestType: request.TestType,
		RequestedModel: request.RequestedModel, UpstreamModel: target.UpstreamModel,
		ReasoningEffort: request.ReasoningEffort, DataVersion: OpenAIEvalDataVersion,
		Status: "running", StartedAt: start, TriggeredBy: actorID, TriggerSource: source,
		Outcome: OpenAIEvalOutcome{Status: "running", Confidence: "none", Scheduling: "disabled"},
		Samples: make([]OpenAIEvalSampleRecord, 0),
	}
	runID, err := s.repo.CreateRun(runCtx, run)
	if err != nil {
		return nil, err
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
			run.Outcome = OpenAIEvalOutcome{Status: "error", Reason: run.Error, SampleCount: run.RequestCount, ExpectedCount: run.RequestCount, Confidence: "none", Scheduling: "alert_only"}
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
		// Evaluation probes are diagnostic signals.  Candy/Fingerprint can be
		// affected by sampling variance, provider-specific behavior, or a
		// temporary transport failure, so they remain alert-only and must not
		// silently remove a route from production scheduling.  ModelTrace and
		// State Probe have the same non-mutating contract.
		if shouldRecordOpenAIEvalRouteHealth(request.TestType) {
			s.recordRouteHealth(finishCtx, target.Account.ID, request.RequestedModel, request.ReasoningEffort, hardFailure, hardFailureCode)
		}
		return run, runErr
	}
	if request.TestType == OpenAIEvalTypeStateProbe {
		probe := s.accountTest.RunOpenAIStateProbe(runCtx, target)
		run.RequestCount = probe.RequestCount
		run.Outcome = OpenAIEvalOutcome{Status: probe.Verdict, Reason: probe.Failure, SampleCount: probe.RequestCount, ExpectedCount: 2, Confidence: "low", Scheduling: "alert_only", StateProbe: probe}
		run.Status = probe.Verdict
		completed, finishErr := finish(nil)
		if finishErr == nil {
			s.applyOpenAIStateProbeBPS(ctx, target, probe)
		}
		return completed, finishErr
	}

	if request.TestType == OpenAIEvalTypeCandy {
		allPassed := true
		validSamples := 0
		for i := 0; i < 5; i++ {
			result, sampleErr := s.accountTest.RunOpenAIEvalSample(runCtx, target, OpenAIEvalCandyPrompt, request.ReasoningEffort)
			run.RequestCount++
			if ctxErr := runCtx.Err(); ctxErr != nil {
				return finish(ctxErr)
			}
			if sampleErr != nil {
				markHardFailure(sampleErr)
				allPassed = false
				run.Samples = append(run.Samples, OpenAIEvalSampleRecord{ProbeID: fmt.Sprintf("candy-21-v1-%d", i+1), ErrorCode: safeOpenAIEvalErrorCode(sampleErr)})
				continue
			}
			validSamples++
			run.InputTokens += result.InputTokens
			run.OutputTokens += result.OutputTokens
			outcome := ScoreOpenAIEvalCandy(result.Text)
			allPassed = allPassed && outcome.Status == "pass"
			run.Samples = append(run.Samples, OpenAIEvalSampleRecord{ProbeID: fmt.Sprintf("candy-21-v1-%d", i+1), Valid: outcome.Status == "pass", ErrorCode: outcome.Reason})
		}
		if validSamples < 5 {
			// A transport/upstream failure means this run did not produce
			// enough evidence for a canary verdict. Keep it neutral in the UI
			// and let the operational error code explain what to retry.
			run.Outcome = OpenAIEvalOutcome{Status: "insufficient", Reason: "insufficient_valid_samples", Score: 0, SampleCount: validSamples, ExpectedCount: 5, Confidence: "none", Scheduling: "alert_only"}
			run.Status = "insufficient"
		} else if allPassed {
			run.Outcome = OpenAIEvalOutcome{Status: "pass", Reason: "all_public_candy_variants_passed", Score: 1, SampleCount: run.RequestCount, ExpectedCount: run.RequestCount, Confidence: "low", Scheduling: "alert_only"}
			run.Status = "pass"
		} else {
			run.Outcome = OpenAIEvalOutcome{Status: "warning", Reason: "one_or_more_public_candy_variants_failed", Score: 0, SampleCount: run.RequestCount, ExpectedCount: run.RequestCount, Confidence: "low", Scheduling: "alert_only"}
			run.Status = "warning"
		}
		return finish(nil)
	}

	if request.TestType == OpenAIEvalTypeModelTrace {
		trace, count, inputTokens, outputTokens, traceErr := s.runModelTrace(runCtx, target, request.ReasoningEffort)
		run.RequestCount = count
		run.InputTokens = inputTokens
		run.OutputTokens = outputTokens
		if trace != nil {
			// Do not persist prompts or model output text. The result remains
			// useful for attribution while respecting the evaluation repository's
			// no-raw-content retention contract.
			for i := range trace.Samples {
				trace.Samples[i].Prompt = ""
				trace.Samples[i].Text = ""
			}
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
			result, sampleErr := s.accountTest.RunOpenAIEvalSample(runCtx, target, fullPrompt, request.ReasoningEffort)
			run.RequestCount++
			if ctxErr := runCtx.Err(); ctxErr != nil {
				return finish(ctxErr)
			}
			if sampleErr != nil {
				markHardFailure(sampleErr)
				code := safeOpenAIEvalErrorCode(sampleErr)
				samples = append(samples, OpenAIEvalSample{ProbeID: probe.ID, Error: code})
				run.Samples = append(run.Samples, OpenAIEvalSampleRecord{ProbeID: probe.ID, ErrorCode: code})
				continue
			}
			run.InputTokens += result.InputTokens
			run.OutputTokens += result.OutputTokens
			normalized, valid := NormalizeOpenAIEvalFingerprintAnswer(result.Text, probe)
			sample := OpenAIEvalSample{ProbeID: probe.ID, Answer: normalized}
			if !valid {
				sample.Error = "invalid_probe_answer"
			}
			samples = append(samples, sample)
			run.Samples = append(run.Samples, OpenAIEvalSampleRecord{ProbeID: probe.ID, NormalizedAnswer: normalized, Valid: valid, ErrorCode: sample.Error})
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
	return s.repo.ListRuns(ctx, filter)
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
