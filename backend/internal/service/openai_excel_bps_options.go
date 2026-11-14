package service

import (
	"context"
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
	return ValidateExcelBPSAccountOptions(a)
}

func applyExcelBPSOptions(raw []byte, a *Account) ([]byte, error) {
	return applyExcelBPSOptionsForContext(context.Background(), raw, a)
}

type excelBPSLastResortKey struct{}

// WithExcelBPSLastResort is only for the pre-send router after it has proved
// every complete compatible route structurally unavailable. Capacity/RPM/cache
// errors do not establish that condition. It never changes the stored opt-in.
func WithExcelBPSLastResort(ctx context.Context) context.Context {
	return context.WithValue(ctx, excelBPSLastResortKey{}, true)
}

func applyExcelBPSOptionsForContext(ctx context.Context, raw []byte, a *Account) ([]byte, error) {
	if err := ValidateExcelBPSOptions(a); err != nil {
		return nil, err
	}
	lastResort, _ := ctx.Value(excelBPSLastResortKey{}).(bool)
	if lastResort && a != nil && a.Extra[ExcelBPSHistoricalEncryptedKey] == true {
		var err error
		raw, err = basispoints.StripEncryptedContent(raw)
		if err != nil {
			return nil, err
		}
	}
	if capability := basispoints.InspectRequestCapabilities(raw); capability != nil {
		omitHosted := lastResort && a != nil && a.Extra[ExcelBPSHostedToolsPolicyKey] == "omit" && capability.Code == "basispoints_hosted_capability" && capability.Param != "input.hosted_tool_dependency" && capability.Param != "tool_choice"
		if !omitHosted {
			return nil, capability
		}
	}
	if a != nil && a.Extra[ExcelBPSHostedToolsPolicyKey] == "reject" && gjson.GetBytes(raw, "tool_choice").String() != "none" {
		if err := basispoints.RejectUnsupportedHostedTools(raw); err != nil {
			return nil, fmt.Errorf("BPS hosted tool policy: %w", err)
		}
	}
	return raw, nil
}
