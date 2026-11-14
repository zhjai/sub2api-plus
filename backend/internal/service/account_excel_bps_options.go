package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	ExcelBPSCredentialsKey                = "excel_bps_credentials"
	ExcelBPSEnabledKey                    = "openai_excel_bps"
	ExcelBPSModelsKey                     = "openai_excel_bps_models"
	ExcelBPSAutoDisableOn403Key           = "openai_excel_bps_auto_disable_on_403"
	ExcelBPSAutoRecoverOn403Key           = "openai_excel_bps_auto_recover_on_403"
	ExcelBPS403RecoveryIntervalMinutesKey = "openai_excel_bps_403_recovery_interval_minutes"
	ExcelBPSAutoMoveOn403Key              = "openai_excel_bps_auto_move_on_403"
	ExcelBPS403TargetGroupIDKey           = "openai_excel_bps_403_target_group_id"
	ExcelBPSCacheCreationAsInputKey       = "openai_excel_bps_cache_creation_as_input"
	ExcelBPS403DisabledAtKey              = "openai_excel_bps_403_disabled_at"
	ExcelBPS403LastProbeAtKey             = "openai_excel_bps_403_last_probe_at"
	ExcelBPS403DisabledReasonKey          = "openai_excel_bps_403_disabled_reason"
	ExcelBPS403ProbeModelKey              = "openai_excel_bps_403_probe_model"
)

func bpsBool(extra map[string]any, key string, fallback bool) bool {
	if value, exists := extra[key]; exists {
		result, _ := value.(bool)
		return result
	}
	return fallback
}
func bpsInteger(raw any) (int64, bool) {
	var s string
	switch v := raw.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		n := int64(v)
		return n, float64(n) == v
	case json.Number:
		s = v.String()
	default:
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}
func (a *Account) excelBPSRouteRequested() bool {
	return a != nil && a.IsOpenAIOAuth() && !a.IsShadow() && bpsBool(a.Extra, ExcelBPSEnabledKey, a.IsExcelOAuth())
}
func (a *Account) IsExcelBPSEnabled() bool {
	if !a.excelBPSRouteRequested() || a.ExcelBPSCredentialAccount() == nil || a.Extra[ExcelBPS403DisabledAtKey] != nil {
		return false
	}
	grant := a.ExcelBPSCredentialAccount()
	if a.Extra["openai_excel_bps_grant_invalid"] == excelBPSGrantFingerprint(grant) {
		return false
	}
	if until, err := time.Parse(time.RFC3339Nano, mapString(a.Extra, "openai_excel_bps_rate_limit_until")); err == nil && time.Now().Before(until) {
		return false
	}
	return true
}
func (a *Account) IsExcelBPSEnabledForModel(requested string) bool {
	if !a.IsExcelBPSEnabled() {
		return false
	}
	model := a.GetMappedModel(requested)
	if windows, ok := a.Extra["openai_excel_bps_model_rate_limits"].(map[string]any); ok {
		if until, err := time.Parse(time.RFC3339Nano, mapString(windows, model)); err == nil && time.Now().Before(until) {
			return false
		}
	}

	raw, exists := a.Extra[ExcelBPSModelsKey]
	if !exists {
		return true
	}
	switch values := raw.(type) {
	case []string:
		for _, v := range values {
			if strings.TrimSpace(v) == model {
				return true
			}
		}
	case []any:
		for _, v := range values {
			if s, ok := v.(string); ok && strings.TrimSpace(s) == model {
				return true
			}
		}
	}
	return false
}
func (a *Account) IsExcelBPSCacheCreationAsInputEnabled() bool {
	return a != nil && bpsBool(a.Extra, ExcelBPSCacheCreationAsInputKey, true)
}
func (a *Account) ExcelBPS403RecoveryInterval() time.Duration {
	if a != nil {
		if n, ok := bpsInteger(a.Extra[ExcelBPS403RecoveryIntervalMinutesKey]); ok && n >= 1 && n <= 10080 {
			return time.Duration(n) * time.Minute
		}
	}
	return time.Hour
}
func ValidateExcelBPSAccountOptions(a *Account) error {
	if err := ValidateExcelBPSSessionProxyOptions(a); err != nil {
		return err
	}
	if a == nil {
		return nil
	}
	for _, key := range []string{ExcelBPSEnabledKey, ExcelBPSAutoDisableOn403Key, ExcelBPSAutoRecoverOn403Key, ExcelBPSAutoMoveOn403Key, ExcelBPSCacheCreationAsInputKey} {
		if value, exists := a.Extra[key]; exists {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s must be a boolean", key)
			}
		}
	}
	if raw, exists := a.Extra[ExcelBPS403RecoveryIntervalMinutesKey]; exists {
		if n, ok := bpsInteger(raw); !ok || n < 1 || n > 10080 {
			return fmt.Errorf("BPS recovery interval must be an integer between 1 and 10080 minutes")
		}
	}
	if bpsBool(a.Extra, ExcelBPSAutoMoveOn403Key, false) {
		if n, ok := bpsInteger(a.Extra[ExcelBPS403TargetGroupIDKey]); !ok || n <= 0 {
			return fmt.Errorf("BPS auto move requires a valid target group")
		}
	}
	if raw, exists := a.Extra[ExcelBPSModelsKey]; exists {
		var values []string
		switch v := raw.(type) {
		case []string:
			values = v
		case []any:
			for _, x := range v {
				s, ok := x.(string)
				if !ok {
					return fmt.Errorf("BPS models must be strings")
				}
				values = append(values, s)
			}
		default:
			return fmt.Errorf("BPS models must be an array")
		}
		if len(values) > 100 {
			return fmt.Errorf("BPS supports at most 100 selected models")
		}
		for _, v := range values {
			if strings.TrimSpace(v) == "" || len(v) > 256 {
				return fmt.Errorf("invalid BPS model name")
			}
		}
	}
	return nil
}

// New configurations get the approved defaults; explicit saved choices win.
func ApplyExcelBPSDefaults(a *Account) {
	if a == nil || a.ExcelBPSCredentialAccount() == nil {
		return
	}
	a.Extra = shallowCopyMap(a.Extra)
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}
	for k, v := range map[string]any{ExcelBPSAutoDisableOn403Key: true, ExcelBPSAutoRecoverOn403Key: true, ExcelBPS403RecoveryIntervalMinutesKey: 60, ExcelBPSAutoMoveOn403Key: false, ExcelBPSCacheCreationAsInputKey: true} {
		if _, ok := a.Extra[k]; !ok {
			a.Extra[k] = v
		}
	}
}

var excelBPSServerMarkerKeys = []string{ExcelBPS403DisabledAtKey, ExcelBPS403LastProbeAtKey, ExcelBPS403DisabledReasonKey, ExcelBPS403ProbeModelKey, "openai_excel_bps_403_moved_at", "openai_excel_bps_403_moved_group_id", "openai_excel_bps_grant_invalid", "openai_excel_bps_rate_limit_until", "openai_excel_bps_grant_refresh_until", "openai_excel_bps_model_rate_limits"}

func StripExcelBPSServerMarkers(extra map[string]any) map[string]any {
	out := shallowCopyMap(extra)
	for _, k := range excelBPSServerMarkerKeys {
		delete(out, k)
	}
	return out
}

// Server-owned markers cannot be injected or cleared through a stale form. A
// manual off clears recovery eligibility; a subsequent on starts a fresh route.
func MergeExcelBPSServerMarkers(extra, current map[string]any) map[string]any {
	out := StripExcelBPSServerMarkers(extra)
	if out == nil {
		out = map[string]any{}
	}
	explicitlyOff := extra[ExcelBPSEnabledKey] == false
	for _, k := range excelBPSServerMarkerKeys {
		if v, ok := current[k]; ok {
			out[k] = v
		}
	}
	if explicitlyOff {
		for _, k := range []string{ExcelBPS403DisabledAtKey, ExcelBPS403LastProbeAtKey, ExcelBPS403DisabledReasonKey, ExcelBPS403ProbeModelKey} {
			delete(out, k)
		}
	}
	return out
}

func excelBPSGrantFingerprint(a *Account) string {
	if a == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(a.GetOpenAIAccessToken()))
	return hex.EncodeToString(sum[:])
}

func AccountHasExcelBPSConfiguration(a *Account) bool {
	if a == nil || a.Platform != PlatformOpenAI {
		return false
	}
	if a.IsExcelOAuth() || a.Credentials[ExcelBPSCredentialsKey] != nil {
		return true
	}
	for k := range a.Extra {
		if strings.HasPrefix(k, "openai_excel_bps") {
			return true
		}
	}
	return false
}
