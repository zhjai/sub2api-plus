package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/redis/go-redis/v9"
)

const (
	bpsStatePrefix      = "bps:state:{bps-state}:"
	bpsStateRevisionKey = bpsStatePrefix + "revision"
	bpsStateTTL         = 2 * time.Hour
	// Replay wraps a native item of at most 1 MiB with its call signature.
	bpsStateMaxBytes     = (1 << 20) + 1024
	bpsStateScopeEntries = 513 // 512 replays and one catalog, sharing one budget.
	bpsStateScopeBytes   = 16 << 20
)

var _ basispoints.StateStore = (*gatewayCache)(nil)

// All keys share a Redis hash slot so the revision and entry can be updated
// atomically. Hash the complete scope (account/API key/trusted session) and item
// separately, keeping identities and arbitrarily long client IDs out of keys.
func bpsStateKey(scope, item string) string {
	scopeHash, itemHash := sha256.Sum256([]byte(scope)), sha256.Sum256([]byte(item))
	return bpsStatePrefix + hex.EncodeToString(scopeHash[:]) + ":" + hex.EncodeToString(itemHash[:])
}

func bpsStateScopeKeys(scope string) (string, string) {
	scopeHash := sha256.Sum256([]byte(scope))
	prefix := bpsStatePrefix + hex.EncodeToString(scopeHash[:])
	return prefix + ":expiry", prefix + ":weights"
}

func validBPSStateIdentity(scope, item string) bool {
	return scope != "" && (item == "catalog" || (strings.HasPrefix(item, "replay:") && len(item) > len("replay:")))
}

var loadBPSStateScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return {0} end
if redis.call('HSTRLEN', KEYS[1], 'raw') > tonumber(ARGV[2]) then return {-1} end
local data = redis.call('HMGET', KEYS[1], 'raw', 'version')
if not data[1] or not data[2] or not redis.call('ZSCORE', KEYS[2], KEYS[1]) or
   not redis.call('HGET', KEYS[3], KEYS[1]) then return {-2} end
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
redis.call('ZADD', KEYS[2], now + tonumber(ARGV[1]), KEYS[1])
redis.call('PEXPIRE', KEYS[1], ARGV[1])
redis.call('PEXPIRE', KEYS[2], ARGV[1])
redis.call('PEXPIRE', KEYS[3], ARGV[1])
return {1, data[1], data[2]}
`)

func (c *gatewayCache) LoadBPSState(ctx context.Context, scope, item string) ([]byte, uint64, error) {
	if c == nil || c.rdb == nil || !validBPSStateIdentity(scope, item) {
		return nil, 0, basispoints.ErrStateUnavailable
	}
	index, weights := bpsStateScopeKeys(scope)
	maxBytes := bpsStateMaxBytes
	if item == "catalog" {
		maxBytes = 1 << 20
	}
	values, err := loadBPSStateScript.Run(ctx, c.rdb, []string{bpsStateKey(scope, item), index, weights}, bpsStateTTL.Milliseconds(), maxBytes).Slice()
	if err != nil || len(values) == 0 {
		return nil, 0, basispoints.ErrStateUnavailable
	}
	status, ok := values[0].(int64)
	if !ok {
		return nil, 0, basispoints.ErrStateUnavailable
	}
	switch status {
	case 0:
		return nil, 0, nil
	case -1:
		return nil, 0, basispoints.ErrStateCapacity
	case 1:
		if len(values) != 3 {
			break
		}
		raw, rawOK := values[1].(string)
		version, versionOK := values[2].(string)
		revision, parseErr := strconv.ParseUint(version, 10, 64)
		if rawOK && versionOK && parseErr == nil && revision > 0 && len(raw) > 0 {
			return []byte(raw), revision, nil
		}
	}
	return nil, 0, basispoints.ErrStateUnavailable
}

var saveBPSStateScript = redis.NewScript(`
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local expired = redis.call('ZRANGEBYSCORE', KEYS[3], '-inf', now)
for _, key in ipairs(expired) do
  local weight = tonumber(redis.call('HGET', KEYS[4], key) or '0')
  redis.call('HINCRBY', KEYS[4], 'total', -weight)
  redis.call('HDEL', KEYS[4], key)
  redis.call('ZREM', KEYS[3], key)
  redis.call('DEL', key)
end
local current = redis.call('HGET', KEYS[1], 'version')
if redis.call('EXISTS', KEYS[1]) == 1 and
   (not current or redis.call('HEXISTS', KEYS[1], 'raw') == 0 or
    not redis.call('ZSCORE', KEYS[3], KEYS[1]) or not redis.call('HGET', KEYS[4], KEYS[1])) then return -2 end
if ARGV[1] ~= '' and (current or '0') ~= ARGV[1] then return 0 end
local oldWeight = tonumber(redis.call('HGET', KEYS[4], KEYS[1]) or '0')
local total = tonumber(redis.call('HGET', KEYS[4], 'total') or '0')
local newCount = redis.call('ZCARD', KEYS[3])
if oldWeight == 0 then newCount = newCount + 1 end
local newTotal = total - oldWeight + string.len(ARGV[2])
local victims = {}
if newCount > tonumber(ARGV[4]) or newTotal > tonumber(ARGV[5]) then
  -- All entries have the same idle TTL, so expiry order is also LRU order.
  -- Selection is bounded by the scope entry limit and never includes this
  -- write's key or the catalog. Full call/result history rebuilds evicted replay.
  local candidates = redis.call('ZRANGE', KEYS[3], 0, tonumber(ARGV[4]) - 1)
  for _, key in ipairs(candidates) do
    if key ~= KEYS[1] and key ~= KEYS[5] then
      table.insert(victims, key)
      newCount = newCount - 1
      newTotal = newTotal - tonumber(redis.call('HGET', KEYS[4], key) or '0')
      if newCount <= tonumber(ARGV[4]) and newTotal <= tonumber(ARGV[5]) then break end
    end
  end
end
if newCount > tonumber(ARGV[4]) or newTotal > tonumber(ARGV[5]) then return -1 end
redis.call('INCR', KEYS[2])
local revision = redis.call('GET', KEYS[2])
for _, key in ipairs(victims) do
  redis.call('HDEL', KEYS[4], key)
  redis.call('ZREM', KEYS[3], key)
  redis.call('DEL', key)
end
redis.call('HSET', KEYS[1], 'raw', ARGV[2], 'version', revision)
redis.call('PEXPIRE', KEYS[1], ARGV[3])
redis.call('ZADD', KEYS[3], now + tonumber(ARGV[3]), KEYS[1])
redis.call('HSET', KEYS[4], KEYS[1], string.len(ARGV[2]), 'total', newTotal)
redis.call('PEXPIRE', KEYS[3], ARGV[3])
redis.call('PEXPIRE', KEYS[4], ARGV[3])
return 1
`)

func (c *gatewayCache) SaveBPSState(ctx context.Context, scope, item string, expected *uint64, raw []byte) (bool, error) {
	if c == nil || c.rdb == nil || !validBPSStateIdentity(scope, item) || len(raw) == 0 {
		return false, basispoints.ErrStateUnavailable
	}
	if len(raw) > bpsStateMaxBytes || (item == "catalog" && len(raw) > 1<<20) {
		return false, basispoints.ErrStateCapacity
	}
	want := ""
	if expected != nil {
		want = strconv.FormatUint(*expected, 10)
	}
	// The one fixed-size revision key does not expire. Reusing a revision after
	// entry expiry would allow an old in-flight catalog writer to win an ABA race.
	index, weights := bpsStateScopeKeys(scope)
	status, err := saveBPSStateScript.Run(ctx, c.rdb, []string{bpsStateKey(scope, item), bpsStateRevisionKey, index, weights, bpsStateKey(scope, "catalog")}, want, raw, bpsStateTTL.Milliseconds(), bpsStateScopeEntries, bpsStateScopeBytes).Int64()
	if err == nil && status == -1 {
		return false, basispoints.ErrStateCapacity
	}
	if err != nil || status < 0 || status > 1 {
		return false, basispoints.ErrStateUnavailable
	}
	return status == 1, nil
}
