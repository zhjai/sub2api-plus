package service

import (
	"context"
	"time"
)

// Upstream windows and local request totals have different meanings. Never
// turn a successful local SQL query into a fabricated zero-percent quota.
func (s *AccountUsageService) getExcelBPSUsage(ctx context.Context, account *Account, force bool) (*UsageInfo, error) {
	usage, err := s.getExcelBPSLocalUsage(ctx, account)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	applyExtraToUsage(usage, account.Extra, now)
	if sampled, ok := account.Extra["codex_usage_updated_at"].(string); ok {
		if at, parseErr := time.Parse(time.RFC3339, sampled); parseErr == nil {
			usage.QuotaUpdatedAt = &at
		}
	}
	if usage.FiveHour != nil || usage.SevenDay != nil {
		usage.QuotaSource = "account_codex_snapshot"
	}
	stale := usage.QuotaUpdatedAt == nil || now.Sub(*usage.QuotaUpdatedAt) >= openAIProbeCacheTTL
	if (force || stale) && s.shouldProbeOpenAICodexSnapshot(account.ID, now, force) {
		if s.openAIQuotaService == nil {
			usage.QuotaRefreshError = "Account quota query service is unavailable; local totals are not upstream quota."
		} else if quota, queryErr := s.openAIQuotaService.QueryUsage(ctx, account.ID); queryErr != nil {
			// Error text is already sanitized by the quota service. Preserve a
			// previous snapshot and its timestamp when a refresh fails.
			usage.QuotaRefreshError = queryErr.Error()
		} else if updates := buildOpenAIAutoResetUsageUpdates(quota, now); len(updates) > 0 {
			mergeAccountExtra(account, updates)
			s.persistOpenAICodexProbeSnapshot(account.ID, updates)
			applyExtraToUsage(usage, account.Extra, now)
			usage.QuotaSource = "chatgpt_wham_usage"
			usage.QuotaUpdatedAt = &now
		} else {
			usage.QuotaRefreshError = "Upstream returned no account usage windows; BPS remaining quota is unknown."
		}
	}
	// Dual grants retain the native/account window totals. The separate BPS
	// counters above use rolling windows and contain only recorded BPS traffic.
	if !account.IsExcelOAuth() && s.usageLogRepo != nil {
		if usage.FiveHour != nil {
			if stats, err := s.usageLogRepo.GetAccountWindowStats(ctx, account.ID, codexWindowStatsStart(usage.FiveHour, 5*time.Hour, now)); err == nil {
				usage.FiveHour.WindowStats = windowStatsFromAccountStats(stats)
			}
		}
		if usage.SevenDay != nil {
			if stats, err := s.usageLogRepo.GetAccountWindowStats(ctx, account.ID, codexWindowStatsStart(usage.SevenDay, 7*24*time.Hour, now)); err == nil {
				usage.SevenDay.WindowStats = windowStatsFromAccountStats(stats)
			}
		}
	}
	return usage, nil
}
