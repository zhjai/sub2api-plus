package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
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
