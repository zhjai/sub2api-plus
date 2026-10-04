package repository

import (
	"context"
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// Unlike the admission reader, evaluation never prunes slots or touches indexes.
func (c *concurrencyCache) PeekAccountsLoadBatch(ctx context.Context, accounts []service.AccountWithConcurrency) (map[int64]*service.AccountLoadInfo, error) {
	result := make(map[int64]*service.AccountLoadInfo, len(accounts))
	if len(accounts) == 0 {
		return result, nil
	}
	now, err := c.rdb.Time(ctx).Result()
	if err != nil {
		return nil, err
	}
	pipe := c.rdb.Pipeline()
	type commands struct {
		slots, live *redis.IntCmd
		wait        *redis.StringCmd
	}
	cmds := make([]commands, len(accounts))
	for i, account := range accounts {
		id := strconv.FormatInt(account.ID, 10)
		cmds[i] = commands{
			pipe.ZCount(ctx, accountSlotKeyPrefix+id, "("+strconv.FormatInt(now.Unix()-int64(c.slotTTLSeconds), 10), "+inf"),
			pipe.ZCount(ctx, liveAccountSlotKeyPrefix+id, "("+strconv.FormatInt(now.Unix()-liveLeaseTTLSeconds, 10), "+inf"),
			pipe.Get(ctx, accountWaitKeyPrefix+id),
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	for i, account := range accounts {
		cmd := cmds[i]
		waiting, err := cmd.wait.Int()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		current := int(cmd.slots.Val() + cmd.live.Val())
		load := 0
		if account.MaxConcurrency > 0 {
			load = (current + waiting) * 100 / account.MaxConcurrency
		}
		result[account.ID] = &service.AccountLoadInfo{AccountID: account.ID, CurrentConcurrency: current, WaitingCount: waiting, LoadRate: load}
	}
	return result, nil
}
