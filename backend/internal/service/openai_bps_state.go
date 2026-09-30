package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// State Probe and an in-band BPS 403 can finish concurrently for the same
// account. Serialize the read/modify/write pair so a probe cannot overwrite a
// freshly recorded permanent-disable reason in this process.
var openAIBPSStateMu sync.Mutex

func openAIBPSModelStateKey(model string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(model))))
	return "openai_eval_bps_" + hex.EncodeToString(digest[:8])
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
	openAIBPSStateMu.Lock()
	defer openAIBPSStateMu.Unlock()
	account, err := s.accounts.GetByID(ctx, target.Account.ID)
	if err != nil || account == nil {
		return
	}
	state, changed := nextOpenAIBPSModelState(readOpenAIBPSModelState(account, target.RequestedModel), probe.Verdict)
	if !changed {
		return
	}
	state.UpdatedAt = time.Now().UTC()
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.accounts.UpdateExtra(stateCtx, account.ID, map[string]any{openAIBPSModelStateKey(target.RequestedModel): state}); err != nil {
		logger.LegacyPrintf("service.openai_eval", "[OpenAI State Probe] failed to persist BPS state account=%d model=%s: %v", account.ID, target.RequestedModel, err)
	}
}
