package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// OpenAIBPSAccountState is the runtime BPS state for an OAuth account.  The
// probe model is only a health-check input; state transitions affect the whole
// account so another model cannot accidentally keep using a degraded account.
type OpenAIBPSAccountState struct {
	Active         bool      `json:"active"`
	DegradedStreak int       `json:"degraded_streak"`
	HealthyStreak  int       `json:"healthy_streak"`
	DisabledReason string    `json:"disabled_reason,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Kept as an alias for the pre-account-scoped API and stored legacy payloads.
type OpenAIBPSModelState = OpenAIBPSAccountState

// Test doubles and legacy repository adapters use this process-local fallback.
// The production repository applies the same transition under a database row
// lock so multiple workers cannot overwrite one another's BPS state.
var openAIBPSStateMu sync.Mutex

func OpenAIBPSModelStateExtraKeyFor(model string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(model))))
	return "openai_eval_bps_" + hex.EncodeToString(digest[:8])
}

func OpenAIBPSAccountStateExtraKey() string { return "openai_eval_bps_account" }

func openAIBPSModelStateKey(model string) string {
	return OpenAIBPSModelStateExtraKeyFor(model)
}

const openAIBPSLegacyStatePrefix = "openai_eval_bps_"

func decodeOpenAIBPSState(value any) (OpenAIBPSAccountState, bool) {
	var state OpenAIBPSAccountState
	raw, err := json.Marshal(value)
	if err != nil || string(raw) == "null" || json.Unmarshal(raw, &state) != nil {
		return state, false
	}
	return state, true
}

func isOpenAIBPSLegacyStateKey(key string) bool {
	if key == OpenAIBPSAccountStateExtraKey() || !strings.HasPrefix(key, openAIBPSLegacyStatePrefix) {
		return false
	}
	suffix := strings.TrimPrefix(key, openAIBPSLegacyStatePrefix)
	if len(suffix) != 16 {
		return false
	}
	_, err := hex.DecodeString(suffix)
	return err == nil
}

// ResolveOpenAIBPSAccountState prefers the account-scoped key and otherwise
// migrates legacy locks and timestamps without granting automatic route
// eligibility. Legacy keys retain their diagnostic history; account probes
// establish fresh activation/recovery streaks. The boolean requests account
// key persistence on the next transition.
func ResolveOpenAIBPSAccountState(extra map[string]any) (OpenAIBPSAccountState, bool) {
	if value, exists := extra[OpenAIBPSAccountStateExtraKey()]; exists {
		if state, ok := decodeOpenAIBPSState(value); ok {
			return state, false
		}
	}
	keys := make([]string, 0)
	for key := range extra {
		if isOpenAIBPSLegacyStateKey(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var merged OpenAIBPSAccountState
	found := false
	for _, key := range keys {
		state, ok := decodeOpenAIBPSState(extra[key])
		if !ok {
			continue
		}
		found = true
		if state.UpdatedAt.After(merged.UpdatedAt) {
			merged.UpdatedAt = state.UpdatedAt
		}
		if strings.EqualFold(state.DisabledReason, "upstream_403") || merged.DisabledReason == "" {
			merged.DisabledReason = state.DisabledReason
		}
	}
	return merged, found
}

func readOpenAIBPSModelState(account *Account, model string) OpenAIBPSModelState {
	if account == nil || account.Extra == nil {
		return OpenAIBPSModelState{}
	}
	state, legacy := ResolveOpenAIBPSAccountState(account.Extra)
	if !legacy || strings.TrimSpace(model) == "" {
		return state
	}
	// Legacy callers that ask for an exact model retain the exact-key behavior
	// only when no account-scoped migration has occurred yet.
	if exact, ok := decodeOpenAIBPSState(account.Extra[openAIBPSModelStateKey(model)]); ok {
		return exact
	}
	return state
}

func readOpenAIBPSAccountState(account *Account) OpenAIBPSAccountState {
	if account == nil {
		return OpenAIBPSAccountState{}
	}
	state, _ := ResolveOpenAIBPSAccountState(account.Extra)
	return state
}

func (a *Account) IsOpenAIBPSActiveForModel(model string) bool {
	return a != nil && a.IsOpenAIOAuth() && !a.IsShadow() && !a.IsSyntheticUITest() && !a.IsOpenAIAgentIdentity() && readOpenAIBPSAccountState(a).Active
}

func (s *OpenAIGatewayService) openAIBPSRouteMode(ctx context.Context, accountID int64, model string) (string, bool) {
	if s == nil || s.openAIEvalRepo == nil || accountID <= 0 {
		return "", false
	}
	config, err := s.openAIEvalRepo.GetConfig(ctx)
	if err != nil || config == nil {
		return "", false
	}
	if bps, ok := openAIEvalBPSAccountConfigFor(config, accountID); ok {
		return normalizeOpenAIEvalBPSMode(bps.Mode, false), true
	}
	// Legacy evaluation targets remain migration data, never routing authority.
	return "", false
}

func openAIEvalBPSAccountConfigFor(config *OpenAIEvalConfig, accountID int64) (OpenAIEvalBPSAccountConfig, bool) {
	if config == nil || accountID <= 0 {
		return OpenAIEvalBPSAccountConfig{}, false
	}
	for _, item := range config.BPSAccounts {
		if item.AccountID == accountID {
			return item, true
		}
	}
	return OpenAIEvalBPSAccountConfig{}, false
}

func (s *OpenAIGatewayService) isOpenAIBPSRouteEnabled(ctx context.Context, accountID int64, model string) bool {
	mode, ok := s.openAIBPSRouteMode(ctx, accountID, model)
	if !ok || mode == OpenAIEvalBPSModeForceOff {
		return false
	}
	if mode == OpenAIEvalBPSModeForceOn {
		return true
	}
	config, err := s.openAIEvalRepo.GetConfig(ctx)
	return err == nil && config != nil && config.BPSAutoEnabled
}

// isOpenAIBPSForwardEligible separates the administrator's route mode from
// the runtime State Probe state.  force_on is an explicit route decision and
// therefore does not require Active, while auto still does.
func (s *OpenAIGatewayService) isOpenAIBPSForwardEligible(ctx context.Context, account *Account, model string) bool {
	if account == nil || !account.IsOpenAIOAuth() || account.IsShadow() || account.IsSyntheticUITest() || account.IsOpenAIAgentIdentity() {
		return false
	}
	mode, ok := s.openAIBPSRouteMode(ctx, account.ID, model)
	if !ok || mode == OpenAIEvalBPSModeForceOff {
		return false
	}
	state := readOpenAIBPSAccountState(account)
	if strings.TrimSpace(state.DisabledReason) != "" {
		// In particular, upstream_403 is an explicit lock and must only be
		// cleared by the administrator reset endpoint.
		return false
	}
	if mode == OpenAIEvalBPSModeForceOn {
		return true
	}
	return s.isOpenAIBPSRouteEnabled(ctx, account.ID, model) && state.Active
}

func nextOpenAIBPSAccountState(state OpenAIBPSAccountState, verdict string, failureThreshold, recoveryThreshold int) (OpenAIBPSAccountState, bool) {
	if failureThreshold < 1 {
		failureThreshold = 3
	}
	if recoveryThreshold < 1 {
		recoveryThreshold = 2
	}
	if state.DisabledReason != "" {
		if strings.EqualFold(strings.TrimSpace(state.DisabledReason), "upstream_403") {
			// A native State Probe exercises the normal OpenAI route, not the BPS
			// endpoint. Its healthy result therefore cannot prove that a BPS
			// upstream_403 lock is recoverable. Only a BPS-specific recovery path
			// or an explicit administrator reset may clear this state.
			return state, false
		}
		if verdict == "healthy" {
			state.DisabledReason = ""
			state.Active = false
			state.DegradedStreak = 0
			state.HealthyStreak = 0
			return state, true
		}
		return state, false
	}
	before := state
	switch verdict {
	case "degraded":
		state.DegradedStreak++
		state.HealthyStreak = 0
		if state.DegradedStreak >= failureThreshold {
			state.Active = true
		}
	case "healthy":
		state.DegradedStreak = 0
		if state.Active {
			state.HealthyStreak++
			if state.HealthyStreak >= recoveryThreshold {
				state.Active = false
				state.HealthyStreak = 0
			}
		} else {
			state.HealthyStreak = 0
		}
	default:
		return state, false
	}
	return state, state != before
}

// Legacy helper retained for old tests and adapters. New account-scoped code
// should pass the configured thresholds explicitly.
func nextOpenAIBPSModelState(state OpenAIBPSModelState, verdict string) (OpenAIBPSModelState, bool) {
	return nextOpenAIBPSAccountState(state, verdict, 3, 2)
}

func (s *OpenAIEvalService) applyOpenAIStateProbeBPS(ctx context.Context, target *OpenAIEvalTarget, probe *OpenAIStateProbeResult, isBPSAccountProbe bool) {
	if s == nil || s.repo == nil || s.accounts == nil || target == nil || target.Account == nil || probe == nil || !isBPSAccountProbe {
		return
	}
	config, err := s.repo.GetConfig(ctx)
	if err != nil || config == nil || !config.BPSAutoEnabled {
		return
	}
	bps, configured := openAIEvalBPSAccountConfigFor(config, target.Account.ID)
	if !configured || normalizeOpenAIEvalBPSMode(bps.Mode, false) != OpenAIEvalBPSModeAuto {
		return
	}
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := updateOpenAIBPSAccountState(stateCtx, s.accounts, target.Account.ID, func(state OpenAIBPSAccountState) (OpenAIBPSAccountState, bool) {
		next, changed := nextOpenAIBPSAccountState(state, probe.Verdict, bps.FailureThreshold, bps.RecoveryThreshold)
		if changed {
			next.UpdatedAt = time.Now().UTC()
		}
		return next, changed
	}); err != nil {
		logger.LegacyPrintf("service.openai_eval", "[OpenAI State Probe] failed to persist BPS state account=%d probe_model=%s: %v", target.Account.ID, target.RequestedModel, err)
	}
}

func updateOpenAIBPSModelState(ctx context.Context, accounts AccountRepository, accountID int64, model string, transition func(OpenAIBPSModelState) (OpenAIBPSModelState, bool)) error {
	if accounts == nil || accountID <= 0 || transition == nil {
		return nil
	}
	if updater, ok := accounts.(OpenAIBPSAccountStateRepository); ok {
		return updater.UpdateOpenAIBPSAccountState(ctx, accountID, func(state OpenAIBPSAccountState) (OpenAIBPSAccountState, bool) { return transition(state) })
	}
	if updater, ok := accounts.(OpenAIBPSModelStateRepository); ok {
		return updater.UpdateOpenAIBPSModelState(ctx, accountID, model, transition)
	}

	// Test doubles and legacy repositories do not yet expose the database
	// transaction helper. Keep a safe single-process fallback while production
	// repositories use the row-locked implementation below.
	openAIBPSStateMu.Lock()
	defer openAIBPSStateMu.Unlock()
	account, err := accounts.GetByID(ctx, accountID)
	if err != nil || account == nil {
		if err != nil {
			return err
		}
		return ErrAccountNotFound
	}
	state, changed := transition(readOpenAIBPSModelState(account, model))
	if !changed {
		return nil
	}
	return accounts.UpdateExtra(ctx, accountID, map[string]any{openAIBPSModelStateKey(model): state})
}

func updateOpenAIBPSAccountState(ctx context.Context, accounts AccountRepository, accountID int64, transition func(OpenAIBPSAccountState) (OpenAIBPSAccountState, bool)) error {
	if accounts == nil || accountID <= 0 || transition == nil {
		return nil
	}
	if updater, ok := accounts.(OpenAIBPSAccountStateRepository); ok {
		return updater.UpdateOpenAIBPSAccountState(ctx, accountID, transition)
	}
	openAIBPSStateMu.Lock()
	defer openAIBPSStateMu.Unlock()
	account, err := accounts.GetByID(ctx, accountID)
	if err != nil || account == nil {
		if err != nil {
			return err
		}
		return ErrAccountNotFound
	}
	state, changed := transition(readOpenAIBPSAccountState(account))
	if !changed {
		return nil
	}
	return accounts.UpdateExtra(ctx, accountID, map[string]any{OpenAIBPSAccountStateExtraKey(): state})
}

// ResetOpenAIBPSState is the explicit administrator recovery path for a BPS
// route disabled by upstream_403. Native State Probe results deliberately do
// not call this method because they exercise a different upstream endpoint.
func (s *OpenAIEvalService) ResetOpenAIBPSState(ctx context.Context, accountID int64, model string, actorID int64) (OpenAIBPSModelState, error) {
	if s == nil || s.accounts == nil || s.repo == nil {
		return OpenAIBPSModelState{}, errors.New("OpenAI evaluation service is unavailable")
	}
	model = strings.TrimSpace(model)
	if accountID <= 0 || (model != "" && !isOpenAIEvalSupportedModel(model)) {
		return OpenAIBPSModelState{}, errors.New("invalid BPS account or model")
	}
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return OpenAIBPSModelState{}, err
	}
	if account == nil || !account.IsOpenAIOAuth() || account.IsShadow() || account.IsSyntheticUITest() || account.IsOpenAIAgentIdentity() {
		return OpenAIBPSModelState{}, errors.New("BPS requires a direct OpenAI OAuth account")
	}

	reset := OpenAIBPSModelState{UpdatedAt: time.Now().UTC()}
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := updateOpenAIBPSAccountState(stateCtx, s.accounts, accountID, func(OpenAIBPSAccountState) (OpenAIBPSAccountState, bool) {
		return reset, true
	}); err != nil {
		return OpenAIBPSModelState{}, err
	}
	if err := s.repo.RecordAuditEvent(stateCtx, actorID, "bps_state_reset", map[string]any{
		"account_id":      accountID,
		"requested_model": model,
	}); err != nil {
		logger.LegacyPrintf("service.openai_eval", "[OpenAI BPS] state reset persisted but audit write failed account=%d model=%s: %v", accountID, model, err)
	}
	return reset, nil
}
