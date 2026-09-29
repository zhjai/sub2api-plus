package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheOpenAISessionEscapeRouteIsScopedAndMonotonic(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, ok := NewGatewayCache(client).(service.OpenAISessionEscapeRouteCache)
	require.True(t, ok)
	ctx := context.Background()

	require.NoError(t, store.AddOpenAISessionEscapedRoute(ctx, 4, "session", "gpt-6-astra", "high", 7, 0.06, time.Minute))
	require.NoError(t, store.AddOpenAISessionEscapedRoute(ctx, 4, "session", "gpt-6-astra", "high", 8, 0.08, time.Minute))

	state, err := store.GetOpenAISessionEscapedRoute(ctx, 4, "session", "gpt-6-astra", "high")
	require.NoError(t, err)
	require.Equal(t, []int64{7, 8}, state.AccountIDs)
	require.True(t, state.HasFailedRate)
	require.Equal(t, 0.08, state.FailedRateMultiplier)

	otherEffort, err := store.GetOpenAISessionEscapedRoute(ctx, 4, "session", "gpt-6-astra", "low")
	require.NoError(t, err)
	require.Empty(t, otherEffort.AccountIDs)
	require.False(t, otherEffort.HasFailedRate)
	otherGroup, err := store.GetOpenAISessionEscapedRoute(ctx, 5, "session", "gpt-6-astra", "high")
	require.NoError(t, err)
	require.Empty(t, otherGroup.AccountIDs)

	key := buildOpenAISessionEscapeRouteKey(4, "session", "gpt-6-astra", "high")
	ttl := mr.TTL(key)
	require.Greater(t, ttl, time.Duration(0))
	require.LessOrEqual(t, ttl, time.Minute)
}
