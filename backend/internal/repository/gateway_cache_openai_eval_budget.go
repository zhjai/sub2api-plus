package repository

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// One script serializes reservation, actual charges and leased ownership.
// Server time and expiry prevent worker clock skew and crashed-run reservations.
var openAIEvalBudgetScript = redis.NewScript(`
redis.replicate_commands()
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local action,owner,kind=ARGV[1],ARGV[2],ARGV[3]
local sendID=ARGV[11]
local automatic=ARGV[4]=='1'
local nominal,enabled,maximum,interval,concurrency=tonumber(ARGV[5]),ARGV[6]=='1',tonumber(ARGV[7]),tonumber(ARGV[8])*1000,tonumber(ARGV[9])
local expired=redis.call('ZRANGEBYSCORE',KEYS[1],'-inf',now)
for _,id in ipairs(expired) do
 redis.call('HDEL',KEYS[2],id)
 redis.call('HDEL',KEYS[3],id)
end
redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now)
redis.call('ZREMRANGEBYSCORE',KEYS[4],'-inf',now-3600000)
for _,id in ipairs(redis.call('ZRANGEBYSCORE',KEYS[8],'-inf',now-3600000)) do redis.call('HDEL',KEYS[9],id) end
redis.call('ZREMRANGEBYSCORE',KEYS[8],'-inf',now-3600000)
local sent=redis.call('ZCARD',KEYS[4])
local reserved=0
for _,value in ipairs(redis.call('HVALS',KEYS[2])) do reserved=reserved+tonumber(value) end
reserved=reserved+redis.call('ZCARD',KEYS[8])
local active=redis.call('ZCARD',KEYS[1])
local last=tonumber(redis.call('GET',KEYS[5]) or '0')
local nextAt=0
if enabled and last>0 then nextAt=last+interval end
if enabled and redis.call('EXISTS',KEYS[10])==1 then nextAt=math.max(nextAt,now+1000) end
local function result(allowed,reason)
 if reason~='' then redis.call('SET',KEYS[6],reason,'PX',3600000) end
 return {allowed,sent,reserved,active,reason,nextAt}
end
if action=='read' then return {1,sent,reserved,active,redis.call('GET',KEYS[6]) or '',nextAt} end
if action=='confirm' or action=='refund' or action=='uncertain' then
 local pending=redis.call('HGET',KEYS[9],sendID)
 if not pending then return result(0,'evaluation_send_reservation_lost') end
 if pending~=owner..':1' and pending~=owner..':0' then return result(0,'evaluation_send_owner_mismatch') end
 if redis.call('GET',KEYS[10])==sendID then redis.call('DEL',KEYS[10]) end
 if action=='uncertain' then redis.call('SET',KEYS[5],now,'PX',86401000);return result(1,'evaluation_send_unknown') end
 redis.call('ZREM',KEYS[8],sendID);redis.call('HDEL',KEYS[9],sendID);reserved=reserved-1
 if action=='confirm' then
  redis.call('ZADD',KEYS[4],now,sendID);redis.call('PEXPIRE',KEYS[4],3601000);sent=sent+1
  redis.call('SET',KEYS[5],now,'PX',86401000)
 elseif pending==owner..':1' and redis.call('ZSCORE',KEYS[1],owner) then
  redis.call('HINCRBY',KEYS[2],owner,1);reserved=reserved+1
 end
 return result(1,'')
end
if action=='release' then
 redis.call('ZREM',KEYS[1],owner); redis.call('HDEL',KEYS[2],owner); redis.call('HDEL',KEYS[3],owner)
 return result(1,'')
end
if action=='admit' then
 if redis.call('ZSCORE',KEYS[1],owner) then return result(0,'duplicate_run_owner') end
 if automatic then
  for _,value in ipairs(redis.call('HVALS',KEYS[3])) do
   if value==kind then return result(0,'credential_test_already_running') end
  end
 end
 if enabled then
  if redis.call('EXISTS',KEYS[10])==1 then return result(0,'evaluation_send_interval') end
  if active>=concurrency then return result(0,'background_concurrency') end
  if sent+reserved+nominal>maximum then return result(0,'evaluation_budget_exhausted') end
  local remaining=reserved+nominal
  local duration=(remaining-1)*interval
  local rpm=tonumber(ARGV[12])
  if rpm>0 then duration=math.max(duration,math.floor((remaining-1)/rpm)*60000) end
  if math.max(now,nextAt)+duration>=now+tonumber(ARGV[10])*1000 then return result(0,'budget_cannot_complete_run') end
 end
 redis.call('ZADD',KEYS[1],now+90000,owner)
 redis.call('HSET',KEYS[2],owner,enabled and nominal or 0)
 redis.call('HSET',KEYS[3],owner,automatic and kind or '')
 active=active+1
 if enabled then reserved=reserved+nominal end
elseif action=='renew' then
 if not redis.call('ZSCORE',KEYS[1],owner) then return result(0,'evaluation_budget_lease_lost') end
 redis.call('ZADD',KEYS[1],now+90000,owner)
elseif action=='send' or action=='prepare' then
 if not redis.call('ZSCORE',KEYS[1],owner) then return result(0,'evaluation_budget_lease_lost') end
 if action=='prepare' and (sendID=='' or redis.call('HEXISTS',KEYS[9],sendID)==1) then return result(0,'evaluation_duplicate_send_admission') end
 local mine=tonumber(redis.call('HGET',KEYS[2],owner) or '0')
 if enabled then
  if now<nextAt then return result(0,'evaluation_send_interval') end
  if sent+reserved+(mine>0 and 0 or 1)>maximum then return result(0,'evaluation_budget_exhausted') end
 end
 if mine>0 then redis.call('HINCRBY',KEYS[2],owner,-1); reserved=reserved-1 end
 if action=='prepare' then
  redis.call('ZADD',KEYS[8],now,sendID)
  redis.call('HSET',KEYS[9],sendID,owner..(mine>0 and ':1' or ':0'))
  redis.call('PEXPIRE',KEYS[8],3601000);redis.call('PEXPIRE',KEYS[9],3601000)
  reserved=reserved+1
  if enabled then redis.call('SET',KEYS[10],sendID,'PX',150000) end
 else
  local seq=redis.call('INCR',KEYS[7])
  redis.call('ZADD',KEYS[4],now,owner..':'..seq)
  sent=sent+1
 end
 redis.call('SET',KEYS[5],now,'PX',86401000)
 nextAt=enabled and now+interval or 0
else return result(0,'invalid_budget_operation') end
redis.call('DEL',KEYS[6])
for _,key in ipairs({KEYS[1],KEYS[2],KEYS[3]}) do redis.call('PEXPIRE',key,180000) end
redis.call('PEXPIRE',KEYS[4],3601000)
redis.call('PEXPIRE',KEYS[7],86401000)
return result(1,'')
`)

func (c *gatewayCache) OpenAIEvalBudget(ctx context.Context, namespace string, op service.OpenAIEvalBudgetOperation) (service.OpenAIEvalBudgetDecision, error) {
	var result service.OpenAIEvalBudgetDecision
	if c == nil || c.rdb == nil || namespace == "" {
		return result, fmt.Errorf("evaluation budget Redis unavailable")
	}
	if op.Action != "read" && op.Owner == "" {
		return result, fmt.Errorf("evaluation budget owner required")
	}
	// Hash even already-derived IDs: callers can never accidentally put tokens in keys.
	prefix := fmt.Sprintf("openai:eval:{%x}:", sha256.Sum256([]byte(namespace)))
	keys := []string{prefix + "runs", prefix + "reservations", prefix + "types", prefix + "sends", prefix + "last", prefix + "deferred", prefix + "sequence", prefix + "pending", prefix + "pending_owners", prefix + "dispatch"}
	flag := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	values, err := openAIEvalBudgetScript.Run(ctx, c.rdb, keys, op.Action, op.Owner, op.TestType, flag(op.Automatic), op.Nominal, flag(op.Control.BudgetEnabled), op.Control.MaxRequestsPerHour, op.Control.MinSendIntervalSeconds, op.Control.MaxBackgroundConcurrency, op.Control.SamplingWindowSeconds, op.SendID, op.RPM).Slice()
	if err != nil {
		return result, err
	}
	if len(values) != 6 {
		return result, fmt.Errorf("invalid evaluation budget result")
	}
	number := func(i int) int64 { n, _ := values[i].(int64); return n }
	result.Allowed = number(0) == 1
	result.SentLastHour = int(number(1))
	result.Reserved = int(number(2))
	result.ActiveRuns = int(number(3))
	result.DeferredReason, _ = values[4].(string)
	if n := number(5); n > 0 {
		at := time.UnixMilli(n).UTC()
		result.NextSendAt = &at
	}
	return result, nil
}

var _ service.OpenAIEvalBudgetStore = (*gatewayCache)(nil)
