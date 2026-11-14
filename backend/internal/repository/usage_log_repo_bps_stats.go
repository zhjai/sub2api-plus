package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"time"
)

// GetAccountBPSWindowStats attributes usage by the actual upstream endpoint.
// Native traffic on the same account (including the same model) is excluded.
func (r *usageLogRepository) GetAccountBPSWindowStats(ctx context.Context, accountID int64, startTime time.Time) (*usagestats.AccountStats, error) {
	query := `
 SELECT COUNT(*) AS requests,
 COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) AS tokens,
 COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)), 0) AS cost,
 COALESCE(SUM(total_cost), 0) AS standard_cost,
 COALESCE(SUM(actual_cost), 0) AS user_cost
 FROM usage_logs
 WHERE account_id = $1 AND created_at >= $2
 AND upstream_endpoint LIKE '/basispoints/api/%'`
	stats := &usagestats.AccountStats{}
	if err := scanSingleRow(ctx, r.sql, query, []any{accountID, startTime},
		&stats.Requests, &stats.Tokens, &stats.Cost, &stats.StandardCost, &stats.UserCost); err != nil {
		return nil, err
	}
	return stats, nil
}
