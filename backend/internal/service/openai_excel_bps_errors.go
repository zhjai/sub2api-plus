package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const (
	excelBPS403Cooldown      = 10 * time.Minute
	excelBPSModel403Reason   = "bps_model_access_denied"
	excelBPSAccount403Reason = "bps_403_temporary_cooldown"
)

// excelBPSModelPermissionDenied accepts only a structured model parameter or
// an exact message about the model actually sent. A generic permission error,
// an echoed model name, or another model's error is not model entitlement proof.
func excelBPSModelPermissionDenied(body []byte, upstreamModel string) bool {
	if strings.TrimSpace(upstreamModel) == "" {
		return false
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Param   string `json:"param"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	if payload.Error.Param == "model" {
		switch payload.Error.Code {
		case "model_not_found", "model_access_denied", "model_not_allowed", "model_not_available", "permission_denied":
			return true
		}
	}
	message := strings.TrimSpace(payload.Error.Message)
	for _, quote := range []string{"'", "`", "\""} {
		model := quote + upstreamModel + quote
		if message == fmt.Sprintf("The model %s does not exist or you do not have access to it.", model) ||
			message == fmt.Sprintf("You do not have access to model %s.", model) {
			return true
		}
	}
	return false
}

// handleExcelBPS403 never invalidates the grant or enters the native Codex 403
// strike counter. Its model argument is the already-resolved upstream model.
// true means this attempt should fail over; it never means permanent disable.
func (s *RateLimitService) handleExcelBPS403(ctx context.Context, account *Account, body []byte, upstreamModel string) bool {
	if s == nil || account == nil || !account.IsExcelOAuth() {
		return false
	}
	if isHTMLResponse(body) || isCloudflareBotBlockResponse(body) {
		return false
	}
	now := time.Now()
	until := now.Add(excelBPS403Cooldown)
	upstreamModel = strings.TrimSpace(upstreamModel)
	if excelBPSModelPermissionDenied(body, upstreamModel) {
		if existing := account.modelRateLimitResetAt(upstreamModel); existing != nil && existing.After(until) {
			until = *existing
		}
		setAccountModelRateLimitSnapshot(account, upstreamModel, until, excelBPSModel403Reason, now)
		if s.accountRepo != nil {
			if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, upstreamModel, until, excelBPSModel403Reason); err != nil {
				slog.Warn("bps_model_403_cooldown_write_failed", "account_id", account.ID, "model", upstreamModel, "error", err)
			}
		}
		return true
	}
	// Unknown and account-level 403s get a bounded account pause. Even a write
	// failure must not escalate a transport/permission error to permanent SetError.
	if existing := account.TempUnschedulableUntil; existing != nil && existing.After(until) {
		until = *existing
	}
	account.TempUnschedulableUntil = &until
	account.TempUnschedulableReason = excelBPSAccount403Reason
	s.notifyAccountSchedulingBlocked(account, until, excelBPSAccount403Reason)
	if s.accountRepo != nil {
		if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, excelBPSAccount403Reason); err != nil {
			slog.Warn("bps_account_403_cooldown_write_failed", "account_id", account.ID, "error", err)
		}
	}
	return true
}
