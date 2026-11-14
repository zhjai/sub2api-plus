package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
)

type excelBPSInferenceStartedKey struct{}

// WithExcelBPSInferenceStarted closes capability channel switching after the
// first attempt. Ordinary unchanged-context retry policy remains independent.
func WithExcelBPSInferenceStarted(ctx context.Context) context.Context {
	return context.WithValue(ctx, excelBPSInferenceStartedKey{}, true)
}

// ExcelBPSCompatibleContextAccount is positive issuer evidence for moving opaque
// state, not a claim that differing cache scopes imply cryptographic rejection.
// Without matching upstream identity, route compatibility remains unverified.
func ExcelBPSCompatibleContextAccount(origin, candidate *Account) bool {
	if origin == nil || candidate == nil {
		return false
	}
	if origin.ID == candidate.ID {
		return true
	}
	originID := strings.TrimSpace(origin.GetCredential("chatgpt_account_id"))
	if originID == "" {
		originID = excelOAuthAccountID(origin.GetOpenAIAccessToken())
	}
	candidateID := strings.TrimSpace(candidate.GetCredential("chatgpt_account_id"))
	return originID != "" && originID == candidateID && candidate.IsOpenAIOAuth()
}

// A known different workspace cannot carry the issuer-bound context that
// ingress would reject. Unknown identity is not proof of incompatibility and
// must not authorize history loss merely because compatibility is unverified.
func excelBPSKnownIncompatibleContextAccount(origin, candidate *Account) bool {
	if origin == nil || candidate == nil || ExcelBPSCompatibleContextAccount(origin, candidate) {
		return false
	}
	originID := strings.TrimSpace(origin.GetCredential("chatgpt_account_id"))
	if originID == "" {
		originID = excelOAuthAccountID(origin.GetOpenAIAccessToken())
	}
	candidateID := strings.TrimSpace(candidate.GetCredential("chatgpt_account_id"))
	return originID != "" && candidateID != "" && originID != candidateID
}

// ExcelBPSPreSendRoute runs before uploads, account RPM admission and inference.
// Its returned native view retains the scheduler-selected account and limits.
func ExcelBPSPreSendRoute(ctx context.Context, account *Account, model string, raw []byte) (*Account, *basispoints.ContextCapabilityError) {
	if account == nil {
		return account, nil
	}
	bps := account.BPSAccountForModel(model)
	if bps == nil && account.IsExcelOAuth() {
		bps = account
	}
	if bps == nil {
		return account, nil
	}
	_, err := applyExcelBPSOptionsForContext(ctx, raw, bps)
	capability, ok := err.(*basispoints.ContextCapabilityError)
	if !ok {
		return account, nil
	}
	if started, _ := ctx.Value(excelBPSInferenceStartedKey{}).(bool); started {
		return account, capability
	}
	if native := ExcelBPSNativeFallbackAccount(bps); native != nil {
		return native, nil
	}
	return account, capability
}

// ExcelBPSCanUseLastResort deliberately inventories configured accounts, not
// just today's free slots. A busy/rate-limited/cooling native account is still
// a complete path and must never authorize historical loss. Repository failure
// also leaves the last-resort gate closed.
func (s *OpenAIGatewayService) ExcelBPSCanUseLastResort(ctx context.Context, groupID *int64, account *Account, model string, raw []byte) bool {
	if s == nil || s.accountRepo == nil || account == nil {
		return false
	}
	if account.Extra[ExcelBPSHistoricalEncryptedKey] != true && account.Extra[ExcelBPSHostedToolsPolicyKey] != "omit" {
		return false
	}
	var accounts []Account
	var err error
	if groupID != nil {
		accounts, err = s.accountRepo.ListByGroup(ctx, *groupID)
	} else {
		return false
	}
	if err != nil {
		return false
	}
	capability := basispoints.InspectRequestCapabilities(raw)
	now := time.Now()
	for i := range accounts {
		candidate := &accounts[i]
		// Apply durable administrator admission gates without IsSchedulable:
		// that method also rejects temporary overload, RPM and cooldown windows.
		// Account expiration is distinct from a refreshable OAuth token expiry.
		if candidate.Status == StatusDisabled || !candidate.Schedulable ||
			(candidate.AutoPauseOnExpired && candidate.ExpiresAt != nil && !now.Before(*candidate.ExpiresAt)) {
			continue
		}
		if !candidate.IsOpenAI() || !supportsOpenAIRequestModel(candidate, model, false) {
			continue
		}
		if capability != nil && capability.RequiresCompatibleState && excelBPSKnownIncompatibleContextAccount(account, candidate) {
			continue
		}
		bps := candidate.BPSAccountForModel(model)
		if bps == nil && !candidate.IsExcelOAuth() {
			return false
		}
		if ExcelBPSNativeFallbackAccount(candidate) != nil {
			return false
		}
		if capability == nil {
			return false
		}
	}
	bps := account.BPSAccountForModel(model)
	if bps == nil && account.IsExcelOAuth() {
		bps = account
	}
	if bps == nil {
		return false
	}
	_, err = applyExcelBPSOptionsForContext(WithExcelBPSLastResort(ctx), raw, bps)
	return err == nil
}
