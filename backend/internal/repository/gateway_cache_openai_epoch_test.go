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

func TestGatewayCacheConvergeOpenAIOpaqueRouteEpochNeverRollsBack(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache, ok := NewGatewayCache(client).(service.OpenAIOpaqueRouteEpochConvergenceCache)
	require.True(t, ok)
	reader, ok := NewGatewayCache(client).(service.OpenAIOpaqueRouteEpochCache)
	require.True(t, ok)
	ctx := context.Background()

	floor := service.OpenAIOpaqueRouteEpochState{Epoch: 2, Bumps: 2, WindowStartedUnix: 123}
	got, err := cache.ConvergeOpenAIOpaqueRouteEpoch(ctx, 7, "session-a", "gpt-6-astra", "high", 7101, floor, time.Hour)
	require.NoError(t, err)
	require.Equal(t, floor, got)

	state, advanced, err := reader.BumpOpenAIOpaqueRouteEpoch(ctx, 7, "session-a", "gpt-6-astra", "high", 7101, 2, 5, time.Hour, time.Hour)
	require.NoError(t, err)
	require.True(t, advanced)
	require.EqualValues(t, 3, state.Epoch)

	got, err = cache.ConvergeOpenAIOpaqueRouteEpoch(ctx, 7, "session-a", "gpt-6-astra", "high", 7101, floor, time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 3, got.Epoch, "an older local floor must not roll Redis back")
}
