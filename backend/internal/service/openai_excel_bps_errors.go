package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ExcelBPS403MoveRequestedAtKey is a transient CAS instruction, not stored metadata.
const ExcelBPS403MoveRequestedAtKey = "openai_excel_bps_403_move_requested_at"

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
		if account.excelBPSNativeCredentials != nil {
			if repo, ok := s.accountRepo.(ExcelBPSStateRepository); ok {
				windows, _ := account.Extra["openai_excel_bps_model_rate_limits"].(map[string]any)
				windows = shallowCopyMap(windows)
				if windows == nil {
					windows = map[string]any{}
				}
				windows[upstreamModel] = until.UTC().Format(time.RFC3339Nano)
				changed, err := repo.CompareAndSwapExcelBPSExtra(ctx, account, map[string]any{"openai_excel_bps_model_rate_limits": windows})
				if err == nil && changed {
					account.Extra = shallowCopyMap(account.Extra)
					if account.Extra == nil {
						account.Extra = map[string]any{}
					}
					account.Extra["openai_excel_bps_model_rate_limits"] = windows
					return true
				}
			}
			return false
		}

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
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &failure)
	qualifying := failure.Error.Code == "permission_denied" || failure.Error.Code == "account_deactivated" || failure.Error.Code == "access_denied" || failure.Error.Code == "forbidden"
	autoDisable := bpsBool(account.Extra, ExcelBPSAutoDisableOn403Key, true)
	autoMove := bpsBool(account.Extra, ExcelBPSAutoMoveOn403Key, false)
	if qualifying && (autoDisable || autoMove) {
		if repo, ok := s.accountRepo.(ExcelBPSStateRepository); ok {
			updates := map[string]any{}
			if autoDisable {
				updates[ExcelBPS403DisabledAtKey] = now.UTC().Format(time.RFC3339Nano)
				updates[ExcelBPS403DisabledReasonKey] = "upstream_403"
				updates[ExcelBPS403LastProbeAtKey] = nil
				updates[ExcelBPS403ProbeModelKey] = upstreamModel
			}
			if autoMove {
				updates[ExcelBPS403MoveRequestedAtKey] = now.UTC().Format(time.RFC3339Nano)
			}
			changed, err := repo.CompareAndSwapExcelBPSExtra(ctx, account, updates)
			if err == nil && changed {
				account.Extra = shallowCopyMap(account.Extra)
				if account.Extra == nil {
					account.Extra = map[string]any{}
				}
				for k, v := range updates {
					if k != ExcelBPS403MoveRequestedAtKey {
						account.Extra[k] = v
					}
				}
				return true
			}
			return false
		}
	}
	if account.excelBPSNativeCredentials != nil {
		return false
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

func (s *OpenAIGatewayService) handleExcelBPSGrantError(ctx context.Context, a *Account, status int, headers http.Header) bool {
	if s == nil || s.accountRepo == nil {
		return false
	}
	repo, ok := s.accountRepo.(ExcelBPSStateRepository)
	if !ok {
		return false
	}
	updates := map[string]any{}
	if status == http.StatusTooManyRequests {
		wait := time.Minute
		if seconds, err := strconv.Atoi(headers.Get("Retry-After")); err == nil && seconds > 0 {
			wait = time.Duration(seconds) * time.Second
		} else if at, err := http.ParseTime(headers.Get("Retry-After")); err == nil && time.Until(at) > 0 {
			wait = time.Until(at)
		}
		updates["openai_excel_bps_rate_limit_until"] = time.Now().Add(wait).UTC().Format(time.RFC3339Nano)
	} else {
		updates["openai_excel_bps_grant_invalid"] = excelBPSGrantFingerprint(a)
	}
	changed, err := repo.CompareAndSwapExcelBPSExtra(ctx, a, updates)
	if err != nil || !changed {
		return false
	}
	a.Extra = shallowCopyMap(a.Extra)
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}
	for k, v := range updates {
		a.Extra[k] = v
	}
	return true
}
