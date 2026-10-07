package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// A ticket may be reused by upstream across sessions. A different policy must
// poison that ambiguous binding rather than let a later writer reauthorize it.
// Compare/write/expire is atomic across instances; no local cache is consulted.
var bindCodexIdentityContinuationScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
local ttl = math.max(1, tonumber(ARGV[2]))
local remaining = redis.call('PTTL', KEYS[1])
ttl = math.max(ttl, remaining)
if not current or current == ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ttl)
  return 1
end
redis.call('SET', KEYS[1], '{}', 'PX', ttl)
return 0
`)

func (c *gatewayCache) SetOpenAIIdentityContinuation(ctx context.Context, key string, binding service.OpenAIIdentityContinuation, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("identity continuation TTL must be positive")
	}
	value, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	bound, err := bindCodexIdentityContinuationScript.Run(ctx, c.rdb, []string{key}, string(value), ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if bound != 1 {
		return service.ErrCodexIdentityContinuationConflict
	}
	return nil
}

func (c *gatewayCache) GetOpenAIIdentityContinuation(ctx context.Context, key string) (*service.OpenAIIdentityContinuation, error) {
	value, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var binding service.OpenAIIdentityContinuation
	if err := json.Unmarshal(value, &binding); err != nil {
		return nil, err
	}
	return &binding, nil
}
