package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Prism response chains share the existing Redis connection. Callers supply a
// hashed key incorporating user/key/account/credential scope; no raw tokens are
// used in keys. Expiry bounds retained conversation data across server restarts.
func (c *gatewayCache) GetPrismResponse(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.rdb == nil || len(key) == 0 || len(key) > 256 {
		return nil, fmt.Errorf("Prism continuation store unavailable")
	}
	value, err := c.rdb.Get(ctx, "prism:response:"+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return value, err
}

func (c *gatewayCache) PutPrismResponse(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if c == nil || c.rdb == nil || len(key) == 0 || len(key) > 256 || ttl <= 0 || ttl > 24*time.Hour || len(value) > 4<<20 {
		return fmt.Errorf("Prism continuation state exceeds storage limits")
	}
	// IDs are immutable. NX prevents a stale concurrent completion from replacing
	// an already published response chain; owners never mutate an existing ID.
	if err := c.rdb.SetNX(ctx, "prism:response:"+key, value, ttl).Err(); err != nil {
		return err
	}
	return nil
}
