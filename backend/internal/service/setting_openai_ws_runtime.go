package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func (s *SettingService) openAIWSModeRouterV2EnabledFromSettings(values map[string]string) bool {
	if value, err := strconv.ParseBool(strings.TrimSpace(values[SettingKeyOpenAIWSModeRouterV2Enabled])); err == nil {
		return value
	}
	return s != nil && s.cfg != nil && s.cfg.OpenAIWSModeRouterV2Enabled()
}

// LoadOpenAIWSModeRouterV2Settings restores the independent override at startup.
// Read/parse errors leave all runtime configuration untouched.
func (s *SettingService) LoadOpenAIWSModeRouterV2Settings(ctx context.Context) error {
	if s == nil || s.cfg == nil || s.settingRepo == nil {
		return nil
	}
	s.settingsWriteMu.Lock()
	defer s.settingsWriteMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, gatewayForwardingDBTimeout)
	defer cancel()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIWSModeRouterV2Enabled)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return fmt.Errorf("load OpenAI WS mode router v2: %w", err)
	}
	enabled := s.cfg.OpenAIWSModeRouterV2Enabled()
	if err == nil && strings.TrimSpace(raw) != "" {
		enabled, err = strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("invalid OpenAI WS mode router v2 setting: %w", err)
		}
	}
	s.cfg.SetOpenAIWSModeRouterV2Enabled(enabled)
	return nil
}

// Publish only a successfully persisted, explicitly included key. Do not reload
// unrelated settings: a read failure after a successful write must not prevent
// this switch from taking effect, or overwrite other runtime configuration.
func (s *SettingService) applyOpenAIWSModeRouterV2Update(updates map[string]string) {
	if raw, ok := updates[SettingKeyOpenAIWSModeRouterV2Enabled]; ok && s.cfg != nil {
		s.cfg.SetOpenAIWSModeRouterV2Enabled(raw == "true")
	}
}
