package service

import (
	"context"
	"time"
)

// SelectOpenAIAccountForRetry rechecks the original account and acquires a new
// slot without running ranking again. Hard eligibility and concurrency checks
// remain the same as session-affinity admission; recent soft scores cannot move
// a bounded same-account retry onto another account.
func (s *OpenAIGatewayService) SelectOpenAIAccountForRetry(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, req.GroupID)
	ctx = s.withOpenAIProfitControlGate(ctx, req.GroupID)
	req.RequirePrivacySet = s.openAIGroupRequiresPrivacySet(ctx, req.GroupID)
	req.DisableStickyEscape = true
	req.PreserveStickyBinding = true
	if req.SessionHash == "" {
		req.SessionHash = "same-account-retry"
	}
	resolver := &defaultOpenAIAccountScheduler{service: s}
	selection, _, err := resolver.selectBySessionHash(ctx, req)
	decision := OpenAIAccountScheduleDecision{Layer: "same_account_retry", ReasonCode: "same_account_retry"}
	if selection != nil && selection.Account != nil {
		decision.SelectedAccountID = selection.Account.ID
		decision.SelectedAccountType = selection.Account.Type
		decision.Acquired = selection.Acquired
		decision.WaitPlan = selection.WaitPlan != nil
		decision.StickyPreviousHit = req.PreviousResponseID != ""
	}
	s.recordLegacyOpenAIAccountScheduleTrace(ctx, req, selection, decision, err, time.Now())
	return selection, decision, err
}
