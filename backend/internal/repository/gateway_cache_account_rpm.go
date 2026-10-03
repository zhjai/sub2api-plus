package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// One bounded ZSET per local account. Expired members are removed before the
// idempotency check, so an old receipt never grants an uncounted new dispatch.
var accountRPMAdmitScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - 60000)
local count = redis.call('ZCARD', KEYS[1])
local allowed = 0
if redis.call('ZSCORE', KEYS[1], ARGV[2]) then
  allowed = 1
elseif count < tonumber(ARGV[1]) then
  redis.call('ZADD', KEYS[1], now, ARGV[2])
  count = count + 1
  allowed = 1
end
if count > 0 then redis.call('PEXPIRE', KEYS[1], 65000) end
local index = 0
if allowed == 0 then index = math.max(0, count - tonumber(ARGV[1])) end
local oldest = redis.call('ZRANGE', KEYS[1], index, index, 'WITHSCORES')
local reset = 0
if #oldest == 2 then reset = tonumber(oldest[2]) + 60000 end
return {allowed, count, reset, math.max(0, reset - now)}
`)

const accountRPMReadLua = `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local count = redis.call('ZCOUNT', KEYS[1], '(' .. (now - 60000), '+inf')
local index = math.max(0, count - tonumber(ARGV[1]))
local oldest = redis.call('ZRANGEBYSCORE', KEYS[1], '(' .. (now - 60000), '+inf', 'WITHSCORES', 'LIMIT', index, 1)
local reset = 0
if #oldest == 2 then reset = tonumber(oldest[2]) + 60000 end
return {0, count, reset, math.max(0, reset - now)}
`

func accountRPMKey(id int64) string { return fmt.Sprintf("account_rpm:{%d}", id) }

func decodeAccountRPMDecision(raw any) (service.AccountRPMDecision, error) {
	values, ok := raw.([]any)
	if !ok || len(values) != 4 {
		return service.AccountRPMDecision{}, fmt.Errorf("invalid account RPM result")
	}
	n := [4]int64{}
	for i, value := range values {
		v, ok := value.(int64)
		if !ok || v < 0 {
			return service.AccountRPMDecision{}, fmt.Errorf("invalid account RPM result field %d", i)
		}
		n[i] = v
	}
	if n[0] > 1 || n[1] > service.AccountRPMLimitMax || n[3] > 60000 || n[2] > 253402300799000 {
		return service.AccountRPMDecision{}, fmt.Errorf("account RPM result out of range")
	}
	d := service.AccountRPMDecision{Allowed: n[0] == 1, Used: int(n[1]), RetryAfter: time.Duration(n[3]) * time.Millisecond}
	if n[2] > 0 {
		d.ResetAt = time.UnixMilli(n[2]).UTC()
	}
	return d, nil
}

func (c *gatewayCache) AdmitAccountRPM(ctx context.Context, id int64, limit int, attempt string) (service.AccountRPMDecision, error) {
	if id <= 0 || limit < 0 || limit > service.AccountRPMLimitMax || len(attempt) == 0 || len(attempt) > 128 {
		return service.AccountRPMDecision{}, fmt.Errorf("invalid account RPM admission arguments")
	}
	if limit == 0 {
		return service.AccountRPMDecision{Allowed: true}, nil
	}
	if c == nil || c.rdb == nil {
		return service.AccountRPMDecision{}, fmt.Errorf("account RPM Redis unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := accountRPMRedisWithTimeout(ctx, c.rdb)
	if err != nil {
		return service.AccountRPMDecision{}, err
	}
	raw, err := accountRPMAdmitScript.Run(ctx, client, []string{accountRPMKey(id)}, limit, attempt).Result()
	if err != nil {
		return service.AccountRPMDecision{}, err
	}
	return decodeAccountRPMDecision(raw)
}

func (c *gatewayCache) ReadAccountRPMBatch(ctx context.Context, limits map[int64]int) (map[int64]service.AccountRPMDecision, error) {
	result := make(map[int64]service.AccountRPMDecision, len(limits))
	if len(limits) == 0 {
		return result, nil
	}
	if c == nil || c.rdb == nil {
		return nil, fmt.Errorf("account RPM Redis unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := accountRPMRedisWithTimeout(ctx, c.rdb)
	if err != nil {
		return nil, err
	}
	pipe := client.Pipeline()
	cmds := make(map[int64]*redis.Cmd, len(limits))
	for id, limit := range limits {
		if id <= 0 || limit <= 0 || limit > service.AccountRPMLimitMax {
			return nil, fmt.Errorf("invalid account RPM account ID")
		}
		// EVAL in a pipeline avoids NOSCRIPT races without a separate preload.
		cmds[id] = pipe.Eval(ctx, accountRPMReadLua, []string{accountRPMKey(id)}, limit)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	for id, cmd := range cmds {
		decision, err := decodeAccountRPMDecision(cmd.Val())
		if err != nil {
			return nil, err
		}
		result[id] = decision
	}
	return result, nil
}

// WithTimeout shares the existing pool and bounds socket I/O even when the
// shared client's ContextTimeoutEnabled is false. Never close this clone.
func accountRPMRedisWithTimeout(ctx context.Context, client *redis.Client) (*redis.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	return client.WithTimeout(timeout), nil
}

var _ service.AccountRPMAdmissionCache = (*gatewayCache)(nil)
