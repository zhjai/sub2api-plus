package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type OpenAIBPSModelState struct {
	Active         bool      `json:"active"`
	DegradedStreak int       `json:"degraded_streak"`
	HealthyStreak  int       `json:"healthy_streak"`
	DisabledReason string    `json:"disabled_reason,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Test doubles and legacy repository adapters use this process-local fallback.
// The production repository applies the same transition under a database row
// lock so multiple workers cannot overwrite one another's BPS state.
var openAIBPSStateMu sync.Mutex

func OpenAIBPSModelStateExtraKeyFor(model string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(model))))
	return "openai_eval_bps_" + hex.EncodeToString(digest[:8])
}

func openAIBPSModelStateKey(model string) string {
	return OpenAIBPSModelStateExtraKeyFor(model)
}

func readOpenAIBPSModelState(account *Account, model string) OpenAIBPSModelState {
	var state OpenAIBPSModelState
	if account == nil || account.Extra == nil {
		return state
	}
	raw, err := json.Marshal(account.Extra[openAIBPSModelStateKey(model)])
	if err == nil {
		_ = json.Unmarshal(raw, &state)
	}
	return state
}

func (a *Account) IsOpenAIBPSActiveForModel(model string) bool {
	return a != nil && a.IsOpenAIOAuth() && !a.IsShadow() && !a.IsSyntheticUITest() && !a.IsOpenAIAgentIdentity() && readOpenAIBPSModelState(a, model).Active
}

func (s *OpenAIGatewayService) isOpenAIBPSRouteEnabled(ctx context.Context, accountID int64, model string) bool {
	if s == nil || s.openAIEvalRepo == nil {
		return false
	}
	config, err := s.openAIEvalRepo.GetConfig(ctx)
	if err != nil || config == nil || !config.BPSAutoEnabled {
		return false
	}
	for _, route := range config.Accounts {
		if route.AccountID == accountID && strings.EqualFold(route.RequestedModel, model) && route.ReasoningEffort == "" && route.BPSAuto {
			return true
		}
	}
	return false
}

func nextOpenAIBPSModelState(state OpenAIBPSModelState, verdict string) (OpenAIBPSModelState, bool) {
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
		if state.DegradedStreak >= 3 {
			state.Active = true
		}
	case "healthy":
		state.DegradedStreak = 0
		if state.Active {
			state.HealthyStreak++
			if state.HealthyStreak >= 2 {
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

func (s *OpenAIEvalService) applyOpenAIStateProbeBPS(ctx context.Context, target *OpenAIEvalTarget, probe *OpenAIStateProbeResult) {
	if s == nil || s.repo == nil || s.accounts == nil || target == nil || target.Account == nil || probe == nil {
		return
	}
	config, err := s.repo.GetConfig(ctx)
	if err != nil || config == nil || !config.BPSAutoEnabled {
		return
	}
	auto := false
	for _, route := range config.Accounts {
		if route.AccountID == target.Account.ID && strings.EqualFold(route.RequestedModel, target.RequestedModel) && route.ReasoningEffort == "" && route.BPSAuto {
			auto = true
			break
		}
	}
	if !auto {
		return
	}
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := updateOpenAIBPSModelState(stateCtx, s.accounts, target.Account.ID, target.RequestedModel, func(state OpenAIBPSModelState) (OpenAIBPSModelState, bool) {
		next, changed := nextOpenAIBPSModelState(state, probe.Verdict)
		if changed {
			next.UpdatedAt = time.Now().UTC()
		}
		return next, changed
	}); err != nil {
		logger.LegacyPrintf("service.openai_eval", "[OpenAI State Probe] failed to persist BPS state account=%d model=%s: %v", target.Account.ID, target.RequestedModel, err)
	}
}

func updateOpenAIBPSModelState(ctx context.Context, accounts AccountRepository, accountID int64, model string, transition func(OpenAIBPSModelState) (OpenAIBPSModelState, bool)) error {
	if accounts == nil || accountID <= 0 || transition == nil {
		return nil
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

// ResetOpenAIBPSState is the explicit administrator recovery path for a BPS
// route disabled by upstream_403. Native State Probe results deliberately do
// not call this method because they exercise a different upstream endpoint.
func (s *OpenAIEvalService) ResetOpenAIBPSState(ctx context.Context, accountID int64, model string, actorID int64) (OpenAIBPSModelState, error) {
	if s == nil || s.accounts == nil || s.repo == nil {
		return OpenAIBPSModelState{}, errors.New("OpenAI evaluation service is unavailable")
	}
	model = strings.TrimSpace(model)
	if accountID <= 0 || !isOpenAIEvalSupportedModel(model) {
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
	if err := updateOpenAIBPSModelState(stateCtx, s.accounts, accountID, model, func(OpenAIBPSModelState) (OpenAIBPSModelState, bool) {
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
