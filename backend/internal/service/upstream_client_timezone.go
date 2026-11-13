package service

import (
	"context"
	"errors"
	"strings"
	"time"
	_ "time/tzdata"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const UpstreamClientTimezoneModeKey = "upstream_client_timezone_mode"
const UpstreamClientTimezoneKey = "upstream_client_timezone"

// UpstreamClientTimezoneState contains only configured/effective server-side data.
// Client-provided timezones, proxy addresses, IP addresses and transport errors are never exposed.
type UpstreamClientTimezoneState struct {
	Mode      string     `json:"mode"`
	Status    string     `json:"status"`
	Timezone  string     `json:"timezone,omitempty"`
	Source    string     `json:"source,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type UpstreamClientTimezoneError struct{ Code string }

func (e *UpstreamClientTimezoneError) Error() string { return "upstream client timezone: " + e.Code }

func loadUpstreamTimezone(zone string) (*time.Location, error) {
	if zone == "" || zone == "Local" || strings.TrimSpace(zone) != zone || strings.Contains(zone, "..") || strings.HasPrefix(zone, "/") {
		return nil, errors.New("invalid IANA timezone")
	}
	return time.LoadLocation(zone)
}

// ValidateUpstreamClientTimezoneExtra validates a complete configuration. Callers of
// partial-update APIs must merge these two keys with persisted configuration first.
func ValidateUpstreamClientTimezoneExtra(extra map[string]any) error {
	mode := "off"
	if raw, ok := extra[UpstreamClientTimezoneModeKey]; ok {
		var valid bool
		mode, valid = raw.(string)
		if !valid || (mode != "off" && mode != "manual" && mode != "auto") {
			return infraerrors.BadRequest("INVALID_UPSTREAM_CLIENT_TIMEZONE", "upstream client timezone mode must be off, manual or auto")
		}
	}
	zone := ""
	if raw, ok := extra[UpstreamClientTimezoneKey]; ok {
		var valid bool
		zone, valid = raw.(string)
		if !valid {
			return infraerrors.BadRequest("INVALID_UPSTREAM_CLIENT_TIMEZONE", "upstream client timezone must be an IANA name")
		}
	}
	if mode == "manual" || zone != "" {
		if _, err := loadUpstreamTimezone(zone); err != nil {
			return infraerrors.BadRequest("INVALID_UPSTREAM_CLIENT_TIMEZONE", "upstream client timezone must be a valid IANA name, for example America/New_York")
		}
	}
	return nil
}

func upstreamTimezoneConfig(account *Account) (mode, zone string) {
	if account == nil || account.Platform != PlatformOpenAI {
		return "off", ""
	}
	mode, _ = account.Extra[UpstreamClientTimezoneModeKey].(string)
	zone, _ = account.Extra[UpstreamClientTimezoneKey].(string)
	if mode == "" {
		mode = "off"
	}
	return
}

var defaultUpstreamTimezoneResolver = newUpstreamTimezoneResolver()

// ApplyUpstreamClientTimezone operates only on explicitly marked environment
// content and web-search protocol metadata. Call before any bridge strips markers.
// proxy must be the actual selected outbound proxy; empty means direct server egress.
func ApplyUpstreamClientTimezone(ctx context.Context, account *Account, body []byte, proxy string) ([]byte, error) {
	return defaultUpstreamTimezoneResolver.apply(ctx, account, body, proxy)
}

func (r *upstreamTimezoneResolver) apply(ctx context.Context, account *Account, body []byte, proxy string) ([]byte, error) {
	mode, zone := upstreamTimezoneConfig(account)
	if mode == "off" || !hasUpstreamTimezoneCarrier(body) {
		return body, nil
	}
	state := UpstreamClientTimezoneState{Mode: mode, Status: "ready", Timezone: zone, Source: "manual"}
	var err error
	if mode == "auto" {
		state, err = r.resolve(ctx, proxy)
	} else if mode != "manual" {
		err = &UpstreamClientTimezoneError{Code: "invalid_mode"}
	}
	if err == nil {
		var location *time.Location
		location, err = loadUpstreamTimezone(state.Timezone)
		if err != nil {
			err = &UpstreamClientTimezoneError{Code: "invalid_timezone"}
		} else {
			body, err = rewriteUpstreamTimezone(body, location, r.now())
		}
	}
	if err != nil {
		state.Status = "error"
		state.Error = "timezone_unavailable"
		var typed *UpstreamClientTimezoneError
		if errors.As(err, &typed) {
			state.Error = typed.Code
		}
	}
	r.record(account, state)
	return body, err
}

// UpstreamClientTimezoneStatus never triggers networking. Auto is pending until
// this process has encountered a marked request with the account's current settings.
func UpstreamClientTimezoneStatus(account *Account) *UpstreamClientTimezoneState {
	if account == nil || account.Platform != PlatformOpenAI {
		return nil
	}
	mode, zone := upstreamTimezoneConfig(account)
	state := &UpstreamClientTimezoneState{Mode: mode, Status: "off"}
	if mode == "manual" || mode == "auto" {
		if cached, ok := defaultUpstreamTimezoneResolver.status(account); ok {
			return &cached
		}
	}
	if mode == "manual" {
		state.Status, state.Source, state.Timezone = "ready", "manual", zone
		if _, err := loadUpstreamTimezone(zone); err != nil {
			state.Status, state.Error = "error", "invalid_timezone"
		}
	} else if mode == "auto" {
		state.Status = "pending"
	}
	return state
}

// preserveUpstreamClientSettings keeps account settings across unrelated edits.
// Disable explicitly with mode=off; deleting a key in an unrelated form is not opt-out.
func preserveUpstreamClientSettings(previous, next map[string]any) map[string]any {
	for _, key := range []string{UpstreamClientTimezoneModeKey, UpstreamClientTimezoneKey, ExcelBPSHistoricalEncryptedKey, ExcelBPSHostedToolsPolicyKey} {
		if _, supplied := next[key]; !supplied {
			if value, exists := previous[key]; exists {
				if next == nil {
					next = make(map[string]any)
				}
				next[key] = value
			}
		}
	}
	return next
}
func hasUpstreamClientSettingsPatch(extra map[string]any) bool {
	for _, key := range []string{UpstreamClientTimezoneModeKey, UpstreamClientTimezoneKey, ExcelBPSHistoricalEncryptedKey, ExcelBPSHostedToolsPolicyKey} {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	return false
}
func validateUpstreamClientSettings(account *Account) error {
	if err := ValidateUpstreamClientTimezoneExtra(account.Extra); err != nil {
		return err
	}
	return ValidateExcelBPSOptions(account)
}
