package service

import (
	"context"
	"crypto/subtle"
	"errors"
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
	if m == nil || a == nil || !m.Enabled || (m.AccountID != nil && *m.AccountID != a.ID) || m.Provider != a.Platform {
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

func (r *OpenAIEvalRankingService) monitorCredentialMatches(m *ChannelMonitor, a *Account) bool {
	if r.monitor == nil || r.monitor.encryptor == nil || m.APIKeyDecryptFailed || a.Type != AccountTypeAPIKey {
		return false
	}
	key, err := r.monitor.encryptor.Decrypt(m.APIKey)
	accountKey := a.GetCredential("api_key")
	return err == nil && key != "" && accountKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(accountKey)) == 1
}

func (r *OpenAIEvalRankingService) monitorEvidence(ctx context.Context, accounts map[int64]*Account) ([]openAIRankingMonitorEvidence, error) {
	return r.monitorEvidenceFor(ctx, accounts, nil, nil)
}

func (r *OpenAIEvalRankingService) monitorEvidenceFor(ctx context.Context, accounts map[int64]*Account, requestedModels map[int64]string, requestedEffort *string) ([]openAIRankingMonitorEvidence, error) {
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
		if page == 10 {
			return nil, errors.New("monitor evidence discovery incomplete")
		}
	}
	sort.Slice(monitors, func(i, j int) bool { return monitors[i].ID < monitors[j].ID })
	result := []openAIRankingMonitorEvidence{}
	queries := 0
	for _, m := range monitors {
		// Independent probe settings do not require a quota association. Match
		// their real endpoint and credential to the route, never the display name.
		matched := []*Account{}
		for _, a := range accounts {
			if rankingMonitorMatchesAccount(m, a) && r.monitorCredentialMatches(m, a) {
				matched = append(matched, a)
			}
		}
		if len(matched) == 0 {
			continue
		}
		sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })
		effort, ok := rankingMonitorEffort(m)
		if !ok || (requestedEffort != nil && effort != *requestedEffort) {
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
			modelAccounts := matched
			if requestedModels != nil {
				modelAccounts = nil
				for _, a := range matched {
					if requestedModels[a.ID] == model {
						modelAccounts = append(modelAccounts, a)
					}
				}
				if len(modelAccounts) == 0 {
					continue
				}
			}
			if queries >= 512 {
				return result, errors.New("monitor evidence history limit reached")
			}
			queries++
			rows, err := r.monitor.repo.ListHistory(ctx, m.ID, model, 20)
			if err != nil {
				return nil, err
			}
			for _, a := range modelAccounts {
				result = append(result, openAIRankingMonitorEvidence{accountID: a.ID, model: model, effort: effort, window: window, rows: rows, monitorID: m.ID, configuredAt: m.UpdatedAt})
			}
		}
	}
	return result, nil
}

func applyRankingMonitor(f *OpenAIEvalRankingFactors, evidence []openAIRankingMonitorEvidence, accountID int64, model, effort string, now time.Time) {
	applyRankingMonitorModels(f, evidence, accountID, []string{model}, effort, now)
}

func applyRankingMonitorModels(f *OpenAIEvalRankingFactors, evidence []openAIRankingMonitorEvidence, accountID int64, models []string, effort string, now time.Time) {
	if f.ErrorRate.Known {
		return
	}
	modelSet := make(map[string]bool, len(models))
	for _, model := range models {
		modelSet[model] = true
	}
	var total, failed int64
	var observed time.Time
	window := 0
	ids := []int64{}
	for _, item := range evidence {
		if item.accountID != accountID || !modelSet[item.model] || item.effort != effort {
			continue
		}
		used := false
		for _, row := range item.rows {
			if row == nil || row.Model != item.model || row.CheckedAt.Before(item.configuredAt) || row.CheckedAt.After(now) || now.Sub(row.CheckedAt) >= item.window {
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
			expires := row.CheckedAt.Add(item.window)
			if f.monitorExpiresAt == nil || expires.Before(*f.monitorExpiresAt) {
				f.monitorExpiresAt = &expires
			}
			if len(f.Monitoring) < 20 {
				f.Monitoring = append(f.Monitoring, OpenAIEvalRankingMonitor{item.monitorID, item.model, row.Status, row.CheckedAt.UTC(), row.LatencyMs, row.PingLatencyMs})
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
