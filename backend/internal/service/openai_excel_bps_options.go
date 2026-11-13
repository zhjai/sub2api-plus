package service

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/tidwall/gjson"
)

const (
	ExcelBPSHistoricalEncryptedKey = "openai_bps_skip_historical_encrypted"
	ExcelBPSHostedToolsPolicyKey   = "openai_bps_hosted_tools_policy"
)

// ValidateExcelBPSOptions validates only explicitly configured adapter options.
// Omission keeps the existing bridge behavior; none can enable native fallback.
func ValidateExcelBPSOptions(a *Account) error {
	if a == nil {
		return nil
	}
	if value, exists := a.Extra[ExcelBPSHistoricalEncryptedKey]; exists {
		if _, ok := value.(bool); !ok {
			return codexIdentityError("OPENAI_BPS_INVALID_OPTIONS", "Ignore historical encrypted content must be a boolean")
		}
	}
	if value, exists := a.Extra[ExcelBPSHostedToolsPolicyKey]; exists && value != "omit" && value != "reject" {
		return codexIdentityError("OPENAI_BPS_INVALID_OPTIONS", "BPS hosted tool policy must be omit or reject")
	}
	return nil
}

func applyExcelBPSOptions(raw []byte, a *Account) ([]byte, error) {
	if err := ValidateExcelBPSOptions(a); err != nil {
		return nil, err
	}
	if gjson.GetBytes(raw, "context_management.#").Int() > 0 {
		return nil, codexIdentityError("OPENAI_BPS_COMPACTION_UNSUPPORTED", "BPS does not support native remote compaction; send expanded conversation history")
	}
	for _, item := range gjson.GetBytes(raw, "input").Array() {
		if item.Get("type").String() == "compaction_trigger" {
			return nil, codexIdentityError("OPENAI_BPS_COMPACTION_UNSUPPORTED", "BPS does not support native remote compaction; send expanded conversation history")
		}
	}
	if a.Extra[ExcelBPSHistoricalEncryptedKey] == true {
		var err error
		raw, err = basispoints.StripEncryptedContent(raw)
		if err != nil {
			return nil, err
		}
	}
	if a.Extra[ExcelBPSHostedToolsPolicyKey] == "reject" && gjson.GetBytes(raw, "tool_choice").String() != "none" {
		if err := basispoints.RejectUnsupportedHostedTools(raw); err != nil {
			return nil, fmt.Errorf("BPS hosted tool policy: %w", err)
		}
	}
	return raw, nil
}
