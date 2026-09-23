package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// A model mismatch is a channel-integrity failure, not a normal successful
// response. Keep the quarantine bounded so an upstream rollout or alias can
// recover without permanently disabling the credential.
const openAIUpstreamModelMismatchCooldown = 15 * time.Minute

// QuarantineOpenAIUpstreamModelMismatch temporarily removes an OpenAI account
// from scheduling after it declares a model different from the model sent on
// the wire. The state is persisted and, when configured, mirrored to the
// scheduler cache so another request can immediately select a different
// account. This applies to both OAuth/Codex and API-key OpenAI accounts.
func (s *OpenAIGatewayService) QuarantineOpenAIUpstreamModelMismatch(
	ctx context.Context,
	account *Account,
	sentModel string,
	responseModel string,
) bool {
	if s == nil || s.accountRepo == nil || account == nil || !account.IsOpenAI() {
		return false
	}
	sentModel = strings.TrimSpace(sentModel)
	responseModel = strings.TrimSpace(responseModel)
	if sentModel == "" || responseModel == "" || upstreamModelsMatchForAudit(sentModel, responseModel) {
		return false
	}

	now := time.Now().UTC()
	until := now.Add(openAIUpstreamModelMismatchCooldown)
	errorMessage := fmt.Sprintf("sent_model=%s upstream_response_model=%s", sentModel, responseModel)
	state := &TempUnschedState{
		UntilUnix:        until.Unix(),
		TriggeredAtUnix:  now.Unix(),
		StatusCode:       200,
		MatchedKeyword:   openAIUpstreamModelMismatchReason,
		RuleIndex:        -1,
		ErrorMessage:     errorMessage,
		TriggerCount:     1,
		TriggerThreshold: 1,
	}
	reasonBytes, _ := json.Marshal(state)
	reason := string(reasonBytes)
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.accountRepo.SetTempUnschedulable(persistCtx, account.ID, until, reason); err != nil {
		return false
	}

	if account.TempUnschedulableUntil == nil || account.TempUnschedulableUntil.Before(until) {
		account.TempUnschedulableUntil = &until
		account.TempUnschedulableReason = reason
	}
	if s.rateLimitService != nil {
		s.rateLimitService.notifyAccountSchedulingBlocked(account, until, openAIUpstreamModelMismatchReason)
		if s.rateLimitService.tempUnschedCache != nil {
			_ = s.rateLimitService.tempUnschedCache.SetTempUnsched(persistCtx, account.ID, state)
		}
	}
	return true
}
