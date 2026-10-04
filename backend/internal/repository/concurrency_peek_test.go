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

func TestRankingPeekCountsFreshRegularAndLiveWithoutMutation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	server.SetTime(now)
	cache := NewConcurrencyCache(client, 15, 900).(service.AccountLoadPeekReader)
	regularKey, liveKey, waitKey := accountSlotKeyPrefix+"17", liveAccountSlotKeyPrefix+"17", accountWaitKeyPrefix+"17"
	require.NoError(t, client.ZAdd(ctx, regularKey, redis.Z{Score: float64(now.Unix() - 900), Member: "expired"}, redis.Z{Score: float64(now.Unix()), Member: "current"}).Err())
	require.NoError(t, client.ZAdd(ctx, liveKey, redis.Z{Score: float64(now.Unix() - liveLeaseTTLSeconds), Member: "expired-live"}, redis.Z{Score: float64(now.Unix()), Member: "current-live"}).Err())
	require.NoError(t, client.Set(ctx, waitKey, 1, time.Minute).Err())
	before := server.Dump()
	load, err := cache.PeekAccountsLoadBatch(ctx, []service.AccountWithConcurrency{{ID: 17, MaxConcurrency: 4}, {ID: 18, MaxConcurrency: 1}})
	require.NoError(t, err)
	require.Equal(t, 2, load[17].CurrentConcurrency)
	require.Equal(t, 1, load[17].WaitingCount)
	require.Equal(t, 75, load[17].LoadRate)
	require.Zero(t, load[18].LoadRate)
	require.Equal(t, before, server.Dump(), "evaluation must not prune expired slots or touch TTLs/indexes")
}
