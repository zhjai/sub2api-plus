package repository

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func hardRPMRedis(t *testing.T) (*miniredis.Miniredis, *gatewayCache) {
	t.Helper()
	m := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: m.Addr(), MaxRetries: 0})
	t.Cleanup(func() { _ = c.Close() })
	return m, &gatewayCache{rdb: c}
}

func TestAccountHardRPMRedisConcurrentMaximum(t *testing.T) {
	_, cache := hardRPMRedis(t)
	var allowed atomic.Int64
	var failed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, err := cache.AdmitAccountRPM(context.Background(), 7, 13, fmt.Sprint(i))
			if err != nil {
				failed.Add(1)
			}
			if d.Allowed {
				allowed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.Zero(t, failed.Load())
	require.EqualValues(t, 13, allowed.Load())
	counts, err := cache.ReadAccountRPMBatch(context.Background(), map[int64]int{7: 13, 8: 13})
	require.NoError(t, err)
	require.Equal(t, 13, counts[7].Used)
	require.Zero(t, counts[8].Used)
	ttl, err := cache.rdb.PTTL(context.Background(), accountRPMKey(7)).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Duration(0))
	require.LessOrEqual(t, ttl, 65*time.Second)
}

func TestAccountHardRPMRedisRollingExpiryDuplicateAndLowering(t *testing.T) {
	m, cache := hardRPMRedis(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 3, 0, 0, 59, 0, time.UTC)
	m.SetTime(start)
	d, err := cache.AdmitAccountRPM(ctx, 1, 2, "lost-reply")
	require.NoError(t, err)
	require.True(t, d.Allowed)
	m.SetTime(start.Add(time.Second)) // a minute boundary does not refill
	d, err = cache.AdmitAccountRPM(ctx, 1, 2, "lost-reply")
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Equal(t, 1, d.Used)
	m.SetTime(start.Add(10 * time.Second))
	d, err = cache.AdmitAccountRPM(ctx, 1, 2, "second")
	require.NoError(t, err)
	require.True(t, d.Allowed)
	d, err = cache.AdmitAccountRPM(ctx, 1, 1, "lowered")
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Equal(t, 60*time.Second, d.RetryAfter)
	m.SetTime(start.Add(59 * time.Second))
	d, err = cache.AdmitAccountRPM(ctx, 1, 2, "denied")
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Equal(t, 2, d.Used)
	m.SetTime(start.Add(60 * time.Second))
	// The expired duplicate receipt is pruned before lookup. It cannot bypass a reduced limit.
	d, err = cache.AdmitAccountRPM(ctx, 1, 1, "lost-reply")
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Equal(t, 1, d.Used)
	m.SetTime(start.Add(70 * time.Second))
	d, err = cache.AdmitAccountRPM(ctx, 1, 1, "lost-reply")
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Equal(t, 1, d.Used)
	d, err = cache.AdmitAccountRPM(ctx, 2, 1, "shadow")
	require.NoError(t, err)
	require.True(t, d.Allowed)
	m.SetError("cache down")
	_, err = cache.AdmitAccountRPM(ctx, 1, 1, "failure")
	require.Error(t, err)
	_, err = cache.ReadAccountRPMBatch(ctx, map[int64]int{1: 1})
	require.Error(t, err)
}

func TestAccountHardRPMSnapshotAndBoundedDecode(t *testing.T) {
	extra := filterSchedulerExtra(map[string]any{"rpm_limit": 8, "base_rpm": 12, "private_unknown": "ignored"})
	require.Equal(t, 8, extra["rpm_limit"])
	require.Equal(t, 12, extra["base_rpm"])
	for _, raw := range []any{nil, "secret-value", []any{int64(1)}, []any{int64(2), int64(1), int64(0), int64(0)}, []any{int64(0), int64(10001), int64(0), int64(0)}, []any{int64(0), strings.Repeat("private", 10000), int64(0), int64(0)}} {
		_, err := decodeAccountRPMDecision(raw)
		require.Error(t, err)
		require.Less(t, len(err.Error()), 100)
		require.NotContains(t, err.Error(), "private")
	}
	d, err := (&gatewayCache{}).AdmitAccountRPM(context.Background(), 1, 0, "unlimited")
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Equal(t, "account_rpm:{17}", accountRPMKey(17))
	var _ service.AccountRPMAdmissionCache = (*gatewayCache)(nil)
}
