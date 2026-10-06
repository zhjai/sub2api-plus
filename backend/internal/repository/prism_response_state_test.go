package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestPrismResponseStateImmutableAndExpires(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &gatewayCache{rdb: rdb}
	ctx := context.Background()
	value, err := cache.GetPrismResponse(ctx, "scope-id")
	require.NoError(t, err)
	require.Nil(t, value)
	require.NoError(t, cache.PutPrismResponse(ctx, "scope-id", []byte("original"), time.Hour))
	require.NoError(t, cache.PutPrismResponse(ctx, "scope-id", []byte("stale"), time.Hour))
	value, err = cache.GetPrismResponse(ctx, "scope-id")
	require.NoError(t, err)
	require.Equal(t, "original", string(value))
	value, err = cache.GetPrismResponse(ctx, "other-scope")
	require.NoError(t, err)
	require.Nil(t, value)
	server.FastForward(time.Hour + time.Second)
	value, err = cache.GetPrismResponse(ctx, "scope-id")
	require.NoError(t, err)
	require.Nil(t, value)
	require.Error(t, cache.PutPrismResponse(ctx, "scope-id", nil, 0))
	require.Error(t, cache.PutPrismResponse(ctx, "scope-id", nil, 25*time.Hour))
}
