package service

import (
	"context"
	"time"
)

// WithOpenAIExecCapability marks only requests declaring exec for the
// capability-scoped runtime gate. Call this before selecting an account.
func WithOpenAIExecCapability(ctx context.Context, body []byte) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return withOpenAIExecCapability(ctx, responsesBodyDeclaresExec(body))
}

// ObserveOpenAIExecCapabilityResult records completed capability failures in a
// separate namespace from generic account/model health. A completed ordinary
// chat request neither clears nor trips the exec breaker.
func (s *OpenAIGatewayService) ObserveOpenAIExecCapabilityResult(account *Account, canonicalModel string, result *OpenAIForwardResult) {
	if s == nil || account == nil || result == nil || !result.ToolCapabilityFailure ||
		!result.RequiresSessionAccountEscape() {
		return
	}
	s.getOpenAIAccountModelTransientState().recordFailure(account.ID, canonicalModel, time.Now(), openAITransientCapabilityExec)
}

// ObserveOpenAIExecCapabilityFailure is the equivalent hook for the generic
// Responses gateway result type.
func (s *OpenAIGatewayService) ObserveOpenAIExecCapabilityFailure(account *Account, canonicalModel string, failed bool) {
	if s == nil || account == nil || !failed {
		return
	}
	s.getOpenAIAccountModelTransientState().recordFailure(account.ID, canonicalModel, time.Now(), openAITransientCapabilityExec)
}

func (s *OpenAIGatewayService) isOpenAIExecCapabilityBlocked(ctx context.Context, account *Account, requestedModel string) bool {
	if s == nil || account == nil || !openAIExecCapabilityRequired(ctx) {
		return false
	}
	return s.getOpenAIAccountModelTransientState().isBlocked(account.ID,
		canonicalOpenAIAccountSchedulingModel(account, requestedModel), time.Now(), openAITransientCapabilityExec)
}
