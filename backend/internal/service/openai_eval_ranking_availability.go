package service

import "time"

// Snapshot diagnostics only inspect existing state. Admission helpers may clear
// expired cooldowns, so they must never be called by policy evaluation.
func (r *OpenAIEvalRankingService) offlineRuntimeExclusions(account *Account, variants []OpenAIEvalSelectionModelVariant, now time.Time) []OpenAIEvalRankingExclusion {
	if r.gateway == nil {
		return nil
	}
	result := []OpenAIEvalRankingExclusion{}
	if state := r.gateway.openaiModelTransient; state != nil {
		state.mu.Lock()
		for _, variant := range variants {
			key, ok := openAIAccountModelTransientKey(account.ID, canonicalOpenAIAccountSchedulingModel(account, variant.SelectionModel))
			if ok && now.Before(state.entries[key].blockUntil) {
				result = append(result, OpenAIEvalRankingExclusion{"model_runtime_cooldown", "live", now.UTC()})
				break
			}
		}
		state.mu.Unlock()
	}
	if proxyID, ok := openAIProxyStreamCircuitProxyID(account); ok {
		if circuit := r.gateway.openaiProxyStreamCircuit; circuit != nil {
			circuit.mu.Lock()
			blocked := !circuit.settings.disabled && now.Before(circuit.entries[proxyID].blockedUntil)
			circuit.mu.Unlock()
			if blocked {
				result = append(result, OpenAIEvalRankingExclusion{"proxy_stream_quarantined", "live", now.UTC()})
			}
		}
	}
	return result
}
