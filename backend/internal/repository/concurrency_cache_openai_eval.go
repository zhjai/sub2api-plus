package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/redis/go-redis/v9"
)

// The waiter check and reduced-limit slot acquisition share a Redis operation.
// Uses the ordinary slots including live requests, never a cached load snapshot.
var openAIEvalSlotScript = redis.NewScript(`
redis.replicate_commands()
local now=tonumber(redis.call('TIME')[1])
local total=0
local limit=tonumber(ARGV[1])
-- Keep one foreground slot whenever possible. With a single-slot account,
-- the background request may use it only when there is no live request or
-- waiter; the checks below still reject it as soon as foreground work exists.
-- A non-positive account limit means unlimited.  Keep the waiter/live-request
-- checks below, but do not turn an unlimited account into an untestable one.
if limit<=0 then
 for i=1,#KEYS,3 do
  if tonumber(redis.call('GET',KEYS[i+2]) or '0')>0 then return {0,now} end
 end
 redis.call('ZADD',KEYS[1],now,ARGV[3])
 redis.call('EXPIRE',KEYS[1],ARGV[2])
 return {1,now}
end
if limit>1 then limit=limit-1 end
for i=1,#KEYS,3 do
 if tonumber(redis.call('GET',KEYS[i+2]) or '0')>0 then return {0,now} end
 redis.call('ZREMRANGEBYSCORE',KEYS[i],'-inf',now-tonumber(ARGV[2]))
 redis.call('ZREMRANGEBYSCORE',KEYS[i+1],'-inf',now-60)
 total=total+redis.call('ZCARD',KEYS[i])+redis.call('ZCARD',KEYS[i+1])
end
if total>=limit then return {0,now} end
redis.call('ZADD',KEYS[1],now,ARGV[3])
redis.call('EXPIRE',KEYS[1],ARGV[2])
return {1,now}
`)

func (c *concurrencyCache) AcquireOpenAIEvalSlot(ctx context.Context, accountID int64, limit int, requestID string) (bool, error) {
	return c.AcquireOpenAIEvalCredentialSlot(ctx, accountID, []service.AccountWithConcurrency{{ID: accountID, MaxConcurrency: limit}}, requestID)
}

func (c *concurrencyCache) AcquireOpenAIEvalCredentialSlot(ctx context.Context, accountID int64, peers []service.AccountWithConcurrency, requestID string) (bool, error) {
	keys := []string{fmt.Sprintf("%s%d", accountSlotKeyPrefix, accountID), liveAccountSlotKey(accountID), fmt.Sprintf("%s%d", accountWaitKeyPrefix, accountID)}
	limit := 0
	for _, peer := range peers {
		if peer.MaxConcurrency > 0 && (limit == 0 || peer.MaxConcurrency < limit) {
			limit = peer.MaxConcurrency
		}
		if peer.ID != accountID {
			keys = append(keys, fmt.Sprintf("%s%d", accountSlotKeyPrefix, peer.ID), liveAccountSlotKey(peer.ID), fmt.Sprintf("%s%d", accountWaitKeyPrefix, peer.ID))
		}
	}
	acquired, now, err := runScriptInt64Pair(ctx, c.rdb, openAIEvalSlotScript, keys, limit, c.slotTTLSeconds, requestID)
	if err == nil && acquired == 1 {
		c.touchActiveIndexAt(ctx, accountActiveIndexKey, accountID, now+int64(c.slotTTLSeconds))
	}
	return acquired == 1, err
}
