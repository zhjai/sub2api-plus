package service

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"time"
)

type openAIRankingMonitorEvidence struct {
	accountID     int64
	model, effort string
	window        time.Duration
	rows          []*ChannelMonitorHistoryEntry
	monitorID     int64
	configuredAt  time.Time
}

func rankingMonitorEffort(m *ChannelMonitor) (string, bool) {
	if m.BodyOverrideMode == "replace" || len(m.ExtraHeaders) > 0 {
		return "", false
	}
	if m.BodyOverrideMode == "" || m.BodyOverrideMode == "off" {
		return "", true
	}
	if m.BodyOverrideMode != "merge" {
		return "", false
	}
	// Only an explicit effort-only override establishes the same probe task.
	for key := range m.BodyOverride {
		if key != "reasoning" && key != "reasoning_effort" {
			return "", false
		}
	}
	value, ok := m.BodyOverride["reasoning_effort"].(string)
	if reasoning, exists := m.BodyOverride["reasoning"]; exists {
		object, valid := reasoning.(map[string]any)
		if !valid || len(object) != 1 {
			return "", false
		}
		nested, valid := object["effort"].(string)
		if !valid || (ok && value != nested) {
			return "", false
		}
		value, ok = nested, true
	}
	if !ok {
		return "", len(m.BodyOverride) == 0
	}
	effort := NormalizeMaxReasoningEffort(value)
	return effort, effort != ""
}

func rankingMonitorMatchesAccount(m *ChannelMonitor, a *Account) bool {
	if m == nil || a == nil || !m.Enabled || m.AccountID == nil || *m.AccountID != a.ID || m.Provider != a.Platform {
		return false
	}
	if m.CheckMode != "" && m.CheckMode != MonitorCheckModeProbe && m.CheckMode != MonitorCheckModeQuotaProbe {
		return false
	}
	if m.APIMode != "" && m.APIMode != "responses" && m.APIMode != "chat_completions" {
		return false
	}
	if !a.IsOpenAICompatible() || a.IsOpenAIOAuthLike() {
		return false
	}
	base := a.GetOpenAIBaseURL()
	if a.Platform == PlatformGrok {
		base = a.GetGrokBaseURL()
	}
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Host == "" {
		return false
	}
	v, err := url.Parse(strings.TrimRight(m.Endpoint, "/"))
	if err != nil || v.Host == "" {
		return false
	}
	return u.Scheme == v.Scheme && strings.EqualFold(u.Host, v.Host) && strings.TrimRight(u.Path, "/") == strings.TrimRight(v.Path, "/") && u.RawQuery == v.RawQuery
}

func (r *OpenAIEvalRankingService) monitorEvidence(ctx context.Context, accounts map[int64]*Account) ([]openAIRankingMonitorEvidence, error) {
	if r.monitor == nil || r.monitor.repo == nil || !r.monitor.probeRuntime(ctx).ActiveProbesAllowed() {
		return nil, nil
	}
	monitors := []*ChannelMonitor{}
	for page := 1; page <= 10; page++ {
		batch, total, err := r.monitor.repo.List(ctx, ChannelMonitorListParams{Page: page, PageSize: 100, Enabled: rankingPtr(true)})
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, batch...)
		if len(batch) == 0 || int64(len(monitors)) >= total {
			break
		}
	}
	sort.Slice(monitors, func(i, j int) bool { return monitors[i].ID < monitors[j].ID })
	result := []openAIRankingMonitorEvidence{}
	queries := 0
	for _, m := range monitors {
		if m.AccountID == nil {
			continue
		}
		a := accounts[*m.AccountID]
		if !rankingMonitorMatchesAccount(m, a) {
			continue
		}
		effort, ok := rankingMonitorEffort(m)
		if !ok {
			continue
		}
		window := 2 * time.Duration(m.IntervalSeconds) * time.Second
		if window < 5*time.Minute {
			window = 5 * time.Minute
		}
		if window > 30*time.Minute {
			window = 30 * time.Minute
		}
		models := append([]string{m.PrimaryModel}, m.ExtraModels...)
		for _, model := range dedupeAndSortModelIDs(models) {
			if queries >= 512 {
				return result, nil
			}
			queries++
			rows, err := r.monitor.repo.ListHistory(ctx, m.ID, model, 20)
			if err != nil {
				return nil, err
			}
			result = append(result, openAIRankingMonitorEvidence{accountID: a.ID, model: model, effort: effort, window: window, rows: rows, monitorID: m.ID, configuredAt: m.UpdatedAt})
		}
	}
	return result, nil
}

func applyRankingMonitor(f *OpenAIEvalRankingFactors, evidence []openAIRankingMonitorEvidence, accountID int64, model, effort string, now time.Time) {
	var total, failed int64
	var observed time.Time
	window := 0
	ids := []int64{}
	for _, item := range evidence {
		if item.accountID != accountID || item.model != model || item.effort != effort {
			continue
		}
		used := false
		for _, row := range item.rows {
			if row == nil || row.Model != model || row.CheckedAt.Before(item.configuredAt) || row.CheckedAt.After(now) || now.Sub(row.CheckedAt) >= item.window {
				continue
			}
			switch row.Status {
			case MonitorStatusOperational, MonitorStatusDegraded:
			case MonitorStatusFailed, MonitorStatusError:
				failed++
			default:
				continue
			}
			total++
			used = true
			if row.CheckedAt.After(observed) {
				observed = row.CheckedAt
			}
			if len(f.Monitoring) < 20 {
				f.Monitoring = append(f.Monitoring, OpenAIEvalRankingMonitor{item.monitorID, model, row.Status, row.CheckedAt.UTC(), row.LatencyMs, row.PingLatencyMs})
			}
		}
		if used {
			ids = append(ids, item.monitorID)
			window = max(window, int(item.window.Seconds()))
		}
	}
	if !f.ErrorRate.Known && total > 0 {
		value := float64(failed) / float64(total)
		f.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(1-value, observed), Value: rankingPtr(value), SampleCount: total,
			Source: rankingPtr("v1_matched_probe"), WindowSeconds: window, MonitorIDs: ids}
	}
}
