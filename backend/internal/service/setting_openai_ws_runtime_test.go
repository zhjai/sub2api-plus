//go:build unit

package service

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type wsModeSettingRepo struct {
	forwardedIPMigrationRepoStub
	readErr error
}

func (r *wsModeSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if r.readErr != nil {
		return "", r.readErr
	}
	return r.forwardedIPMigrationRepoStub.GetValue(ctx, key)
}

func (r *wsModeSettingRepo) GetAll(context.Context) (map[string]string, error) {
	if r.readErr != nil {
		return nil, r.readErr
	}
	return r.values, nil
}

func TestOpenAIWSModeRouterV2SettingsInheritanceAndRestart(t *testing.T) {
	ctx := context.Background()
	for _, initial := range []bool{false, true} {
		t.Run(strconv.FormatBool(initial), func(t *testing.T) {
			repo := &wsModeSettingRepo{forwardedIPMigrationRepoStub: forwardedIPMigrationRepoStub{values: map[string]string{}}}
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = initial
			svc := NewSettingService(repo, cfg)
			require.NoError(t, svc.LoadOpenAIWSModeRouterV2Settings(ctx))
			settings, err := svc.GetAllSettings(ctx)
			require.NoError(t, err)
			require.Equal(t, initial, settings.OpenAIWSModeRouterV2Enabled)
			require.Equal(t, initial, cfg.OpenAIWSModeRouterV2Enabled())
			require.NotContains(t, repo.values, SettingKeyOpenAIWSModeRouterV2Enabled)
			for _, enabled := range []bool{true, false} {
				settings.OpenAIWSModeRouterV2Enabled = enabled
				require.NoError(t, svc.UpdateSettings(ctx, settings))
				require.Equal(t, strconv.FormatBool(enabled), repo.values[SettingKeyOpenAIWSModeRouterV2Enabled])
				require.Equal(t, enabled, cfg.OpenAIWSModeRouterV2Enabled())
				require.Equal(t, initial, cfg.Gateway.OpenAIWS.ModeRouterV2Enabled)
				restarted := &config.Config{}
				restarted.Gateway.OpenAIWS.ModeRouterV2Enabled = !enabled
				reloaded := NewSettingService(repo, restarted)
				// Exercise the existing startup entry used by ProvideSettingService.
				require.NoError(t, reloaded.LoadForwardedClientIPSettings(ctx))
				require.Equal(t, enabled, restarted.OpenAIWSModeRouterV2Enabled())
			}
		})
	}
}

func TestOpenAIWSModeRouterV2StartupReadIsolation(t *testing.T) {
	ctx := context.Background()
	repo := &wsModeSettingRepo{forwardedIPMigrationRepoStub: forwardedIPMigrationRepoStub{values: map[string]string{
		SettingKeyOpenAIWSModeRouterV2Enabled: "true",
		SettingKeyAPIKeyACLTrustForwardedIP:   "true",
		SettingKeyForwardedClientIPHeaders:    `["X-Saved-IP"]`,
		settingKeyForwardedClientIPModeV2:     "true",
	}}}
	cfg := &config.Config{}
	svc := NewSettingService(repo, cfg)
	repo.readErr = errors.New("WS read unavailable")
	require.NoError(t, svc.LoadForwardedClientIPSettings(ctx))
	require.False(t, cfg.OpenAIWSModeRouterV2Enabled())
	require.True(t, cfg.ForwardedClientIPSettings().TrustForwardedIP)
	require.Equal(t, []string{"X-Saved-Ip"}, cfg.ForwardedClientIPSettings().Headers)
	// Conversely, forwarded-IP read failure cannot suppress the independent WS load.
	repo.readErr = nil
	repo.getMultipleErr = errors.New("forwarded IP read unavailable")
	require.Error(t, svc.LoadForwardedClientIPSettings(ctx))
	require.True(t, cfg.OpenAIWSModeRouterV2Enabled())
}

func TestOpenAIWSModeRouterV2SettingsFailuresAndOmission(t *testing.T) {
	ctx := context.Background()
	repo := &wsModeSettingRepo{forwardedIPMigrationRepoStub: forwardedIPMigrationRepoStub{values: map[string]string{SettingKeyOpenAIWSModeRouterV2Enabled: "true"}}}
	cfg := &config.Config{}
	cfg.SetOpenAIWSModeRouterV2Enabled(true)
	cfg.SetForwardedClientIPSettings(true, []string{"X-Client-IP"})
	svc := NewSettingService(repo, cfg)
	settings := &SystemSettings{}
	for _, authDefaults := range []bool{false, true} {
		repo.setMultipleErr = errors.New("write unavailable")
		if authDefaults {
			require.Error(t, svc.UpdateSettingsWithAuthSourceDefaults(ctx, settings, &AuthSourceDefaultSettings{}))
		} else {
			require.Error(t, svc.UpdateSettings(ctx, settings))
		}
		require.True(t, cfg.OpenAIWSModeRouterV2Enabled())
	}
	repo.setMultipleErr = nil
	repo.readErr = errors.New("read unavailable")
	require.Error(t, svc.LoadOpenAIWSModeRouterV2Settings(ctx))
	require.True(t, cfg.OpenAIWSModeRouterV2Enabled())
	// An omitted switch is untouched even when the post-write reload fails.
	require.NoError(t, svc.UpdateSettingsOmitting(ctx, settings, OmittedSettingKeys{SettingKeyOpenAIWSModeRouterV2Enabled: {}}))
	require.True(t, cfg.OpenAIWSModeRouterV2Enabled())
	require.Equal(t, "true", repo.values[SettingKeyOpenAIWSModeRouterV2Enabled])
	// An explicit persisted switch is applied even when unrelated cache reload fails.
	require.NoError(t, svc.UpdateSettingsOmitting(ctx, settings, OmittedSettingKeys{SettingKeySiteName: {}}))
	require.False(t, cfg.OpenAIWSModeRouterV2Enabled())
	require.True(t, cfg.ForwardedClientIPSettings().TrustForwardedIP)
	require.Equal(t, []string{"X-Client-IP"}, cfg.ForwardedClientIPSettings().Headers)
	repo.readErr = nil
	repo.values[SettingKeyOpenAIWSModeRouterV2Enabled] = "invalid"
	require.Error(t, svc.LoadOpenAIWSModeRouterV2Settings(ctx))
	require.False(t, cfg.OpenAIWSModeRouterV2Enabled())
}
