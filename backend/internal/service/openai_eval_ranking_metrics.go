package service

import (
	"sync"
	"sync/atomic"
	"time"
)

const openAIRankingMetricLimit = 100000
const openAIRankingMetricTTL = 24 * time.Hour

func (s *openAIAccountRuntimeStats) trimRuntimeStats(values *sync.Map, count *atomic.Int64) {
	if count.Load() < openAIRankingMetricLimit {
		return
	}
	// Creation is serialized. Eviction never invalidates a pointer held by an
	// in-flight observer, and evicted routes return unknown on their next read.
	var oldestKey any
	oldest := int64(^uint64(0) >> 1)
	cutoff := time.Now().Add(-openAIRankingMetricTTL).UnixNano()
	values.Range(func(key, value any) bool {
		stat := value.(*openAIAccountRuntimeStat)
		at := stat.observedAt.Load()
		if at < cutoff {
			values.Delete(key)
			s.rankingEvictions.Add(1)
			count.Add(-1)
		} else if at < oldest {
			oldest, oldestKey = at, key
		}
		return true
	})
	if count.Load() >= openAIRankingMetricLimit && oldestKey != nil {
		values.Delete(oldestKey)
		s.rankingEvictions.Add(1)
		count.Add(-1)
	}
}

func (s *openAIAccountRuntimeStats) rankingFactors(accountID int64, model, effort string, now time.Time) OpenAIEvalRankingFactors {
	f := emptyOpenAIEvalRankingFactors()
	if s == nil {
		return f
	}
	s.rankingMu.RLock()
	defer s.rankingMu.RUnlock()
	stat, ok := s.loadRoute(accountID, model, effort)
	if !ok {
		return f
	}
	errorRate, ttft, hasTTFT := snapshotOpenAIAccountRuntimeStat(stat)
	at := time.Unix(0, stat.observedAt.Load())
	if count := stat.sampleCount.Load(); count > 0 && !at.After(now) && now.Sub(at) < openAIRankingMetricTTL {
		f.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(1-errorRate, at), Value: rankingPtr(errorRate), SampleCount: count,
			Source: rankingPtr("request_ewma_account_model_effort"), WindowSeconds: int(openAIRankingMetricTTL.Seconds()), MonitorIDs: []int64{}}
	}
	at = time.Unix(0, stat.ttftObservedAt.Load())
	if count := stat.ttftSampleCount.Load(); hasTTFT && count > 0 && !at.After(now) && now.Sub(at) < openAIRankingMetricTTL {
		f.TTFT = OpenAIEvalRankingTTFT{OpenAIEvalFactorMeta: rankingKnown(.5, at), MS: rankingPtr(ttft), SampleCount: count}
	}
	return f
}

func (s *openAIAccountRuntimeStats) rankingVersions() map[openAIAccountRuntimeRouteKey]uint64 {
	versions := make(map[openAIAccountRuntimeRouteKey]uint64)
	if s == nil {
		return versions
	}
	s.rankingMu.RLock()
	defer s.rankingMu.RUnlock()
	s.routes.Range(func(key, value any) bool {
		versions[key.(openAIAccountRuntimeRouteKey)] = value.(*openAIAccountRuntimeStat).metricVersion.Load()
		return true
	})
	return versions
}

func (s *openAIAccountRuntimeStats) rankingMetricsChanged(gen *openAIRankingGeneration, rows []OpenAIEvalRankedAccount, model, effort string) bool {
	if s == nil {
		return false
	}
	s.rankingMu.RLock()
	defer s.rankingMu.RUnlock()
	for _, row := range rows {
		key, ok := openAIAccountRuntimeRouteKeyFor(row.AccountID, model, effort)
		if !ok {
			return true
		}
		version := uint64(0)
		if stat, exists := s.loadRoute(row.AccountID, model, effort); exists {
			version = stat.metricVersion.Load()
		}
		if version != gen.metricVersions[key] {
			return true
		}
	}
	return false
}
