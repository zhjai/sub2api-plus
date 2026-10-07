package repository

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func evalBudgetTestControl() service.OpenAIEvalBackgroundControl {
	return service.OpenAIEvalBackgroundControl{BudgetEnabled: true, MaxRequestsPerHour: 6, MinSendIntervalSeconds: 1, MaxBackgroundConcurrency: 10, SamplingWindowSeconds: 900}
}

func TestOpenAIEvalBudgetRedisAtomicReservationAndActualCharge(t *testing.T) {
	m, cache := hardRPMRedis(t)
	ctx := context.Background()
	now := time.Now()
	m.SetTime(now)
	other := &gatewayCache{rdb: cache.rdb}
	control := evalBudgetTestControl()
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := cache
			if i%2 == 0 {
				c = other
			}
			d, err := c.OpenAIEvalBudget(ctx, "same-credential", service.OpenAIEvalBudgetOperation{Action: "admit", Owner: fmt.Sprint(i), Nominal: 3, Control: control})
			if err != nil {
				t.Error(err)
			}
			if d.Allowed {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 2, accepted.Load())
	d, err := cache.OpenAIEvalBudget(ctx, "same-credential", service.OpenAIEvalBudgetOperation{Action: "read", Control: control})
	require.NoError(t, err)
	require.Equal(t, 6, d.Reserved)
	require.Zero(t, d.SentLastHour)
	// Crashed owners release their unused reservations after lease expiry.
	m.SetTime(now.Add(91 * time.Second))
	d, err = other.OpenAIEvalBudget(ctx, "same-credential", service.OpenAIEvalBudgetOperation{Action: "admit", Owner: "actual", Nominal: 2, Control: control})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Equal(t, 2, d.Reserved)
	send := service.OpenAIEvalBudgetOperation{Action: "send", Owner: "actual", Control: control}
	d, err = cache.OpenAIEvalBudget(ctx, "same-credential", send)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Equal(t, 1, d.Reserved)
	require.Equal(t, 1, d.SentLastHour)
	d, err = other.OpenAIEvalBudget(ctx, "same-credential", send)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Equal(t, "evaluation_send_interval", d.DeferredReason)
	require.Equal(t, 1, d.SentLastHour)
	for i := 1; i < 6; i++ {
		m.SetTime(now.Add(time.Duration(91+i) * time.Second))
		d, err = cache.OpenAIEvalBudget(ctx, "same-credential", send)
		require.NoError(t, err)
		require.True(t, d.Allowed)
	}
	m.SetTime(now.Add(98 * time.Second))
	d, err = cache.OpenAIEvalBudget(ctx, "same-credential", send)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Equal(t, 6, d.SentLastHour)
	require.Zero(t, d.Reserved)
	_, err = cache.OpenAIEvalBudget(ctx, "same-credential", service.OpenAIEvalBudgetOperation{Action: "release", Owner: "actual"})
	require.NoError(t, err)
	d, err = cache.OpenAIEvalBudget(ctx, "same-credential", service.OpenAIEvalBudgetOperation{Action: "read"})
	require.NoError(t, err)
	require.Zero(t, d.ActiveRuns)
	require.Equal(t, 6, d.SentLastHour)
	m.SetTime(now.Add(2 * time.Hour))
	d, err = cache.OpenAIEvalBudget(ctx, "same-credential", service.OpenAIEvalBudgetOperation{Action: "read"})
	require.NoError(t, err)
	require.Zero(t, d.SentLastHour)
}

func TestOpenAIEvalBudgetRedisDefaultNoOverlapAndRenew(t *testing.T) {
	m, c := hardRPMRedis(t)
	ctx := context.Background()
	now := time.Now()
	m.SetTime(now)
	op := service.OpenAIEvalBudgetOperation{Action: "admit", Owner: "a", Automatic: true, TestType: "candy", Nominal: 1}
	d, err := c.OpenAIEvalBudget(ctx, "credential", op)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	op.Owner = "b"
	d, err = c.OpenAIEvalBudget(ctx, "credential", op)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	m.SetTime(now.Add(60 * time.Second))
	d, err = c.OpenAIEvalBudget(ctx, "credential", service.OpenAIEvalBudgetOperation{Action: "renew", Owner: "a"})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	m.SetTime(now.Add(100 * time.Second))
	d, err = c.OpenAIEvalBudget(ctx, "credential", op)
	require.NoError(t, err)
	require.False(t, d.Allowed)
	op.TestType = "modeltrace"
	d, err = c.OpenAIEvalBudget(ctx, "credential", op)
	require.NoError(t, err)
	require.True(t, d.Allowed)
	m.SetTime(now.Add(151 * time.Second))
	d, err = c.OpenAIEvalBudget(ctx, "credential", service.OpenAIEvalBudgetOperation{Action: "send", Owner: "a"})
	require.NoError(t, err)
	require.False(t, d.Allowed)
	require.Equal(t, "evaluation_budget_lease_lost", d.DeferredReason)
	for _, key := range m.Keys() {
		require.NotContains(t, key, "credential")
	}
}

func TestOpenAIEvalForegroundAtomicSlotLimit(t *testing.T) {
	_, g := hardRPMRedis(t)
	c := NewConcurrencyCache(g.rdb, 15, 60).(*concurrencyCache)
	ctx := context.Background()
	ok, err := c.AcquireOpenAIEvalSlot(ctx, 1, 2, "idle")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireOpenAIEvalSlot(ctx, 1, 2, "busy")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.ReleaseAccountSlot(ctx, 1, "idle"))
	ok, err = c.IncrementAccountWaitCount(ctx, 1, 10)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireOpenAIEvalSlot(ctx, 1, 2, "waiting")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.DecrementAccountWaitCount(ctx, 1))
	ok, err = c.AcquireOpenAIEvalSlot(ctx, 1, 2, "background")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireOpenAIEvalSlot(ctx, 1, 2, "extra")
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = c.AcquireAccountSlot(ctx, 1, 2, "foreground")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestOpenAIEvalForegroundSingleSlotOnlyWhenIdle(t *testing.T) {
	_, g := hardRPMRedis(t)
	c := NewConcurrencyCache(g.rdb, 15, 60).(*concurrencyCache)
	ctx := context.Background()
	ok, err := c.AcquireOpenAIEvalSlot(ctx, 1, 1, "background")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireOpenAIEvalSlot(ctx, 1, 1, "second")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.ReleaseAccountSlot(ctx, 1, "background"))
	ok, err = c.IncrementAccountWaitCount(ctx, 1, 10)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireOpenAIEvalSlot(ctx, 1, 1, "waiting")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.DecrementAccountWaitCount(ctx, 1))
	ok, err = c.AcquireAccountSlot(ctx, 1, 1, "foreground")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireOpenAIEvalSlot(ctx, 1, 1, "busy")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.DecrementAccountWaitCount(ctx, 1))
}

func TestOpenAIEvalForegroundSingleSlotLiveLeaseBlocksBackground(t *testing.T) {
	_, g := hardRPMRedis(t)
	c := NewConcurrencyCache(g.rdb, 15, 60).(*concurrencyCache)
	ctx := context.Background()
	require.NoError(t, g.rdb.ZAdd(ctx, liveAccountSlotKey(1), redis.Z{
		Score: float64(time.Now().Unix()), Member: "live-foreground",
	}).Err())
	ok, err := c.AcquireOpenAIEvalSlot(ctx, 1, 1, "background")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, g.rdb.ZRem(ctx, liveAccountSlotKey(1), "live-foreground").Err())
	ok, err = c.AcquireOpenAIEvalSlot(ctx, 1, 1, "background")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestOpenAIEvalUnlimitedConcurrencyStillAdmitsWhenForegroundIdle(t *testing.T) {
	_, g := hardRPMRedis(t)
	c := NewConcurrencyCache(g.rdb, 15, 60).(*concurrencyCache)
	ctx := context.Background()
	ok, err := c.AcquireOpenAIEvalSlot(ctx, 1, 0, "unlimited-idle")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, c.ReleaseAccountSlot(ctx, 1, "unlimited-idle"))
	ok, err = c.IncrementAccountWaitCount(ctx, 1, 10)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireOpenAIEvalSlot(ctx, 1, 0, "unlimited-live")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestOpenAIEvalBudgetPhysicalReservationRefundAndUnknown(t *testing.T) {
	_, c := hardRPMRedis(t)
	ctx := context.Background()
	control := evalBudgetTestControl()
	d, err := c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "admit", Owner: "run", Nominal: 2, Control: control})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	d, err = c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "prepare", Owner: "run", SendID: "no-dial", Control: control})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Zero(t, d.SentLastHour)
	require.Equal(t, 2, d.Reserved)
	d, err = c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "refund", Owner: "run", SendID: "no-dial"})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Zero(t, d.SentLastHour)
	require.Equal(t, 2, d.Reserved)
	// Interval is retained conservatively after a failed dial; this fixture
	// disables numeric controls only to exercise the confirmation boundary.
	control.BudgetEnabled = false
	d, err = c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "prepare", Owner: "run", SendID: "written", Control: control})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	d, err = c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "confirm", Owner: "run", SendID: "written"})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Equal(t, 1, d.SentLastHour)
	require.Equal(t, 1, d.Reserved)
	d, err = c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "prepare", Owner: "run", SendID: "unknown", Control: control})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	d, err = c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "uncertain", Owner: "run", SendID: "unknown"})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Equal(t, 1, d.SentLastHour)
	require.Equal(t, 1, d.Reserved)
	_, err = c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "release", Owner: "run"})
	require.NoError(t, err)
	d, err = c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "read"})
	require.NoError(t, err)
	require.Equal(t, 1, d.Reserved)
	require.Equal(t, "evaluation_send_unknown", d.DeferredReason)
}

func TestOpenAIEvalForegroundDuplicateRowsAreAtomicPeers(t *testing.T) {
	_, g := hardRPMRedis(t)
	c := NewConcurrencyCache(g.rdb, 15, 60).(*concurrencyCache)
	ctx := context.Background()
	peers := []service.AccountWithConcurrency{{ID: 1, MaxConcurrency: 2}, {ID: 2, MaxConcurrency: 2}}
	ok, err := c.IncrementAccountWaitCount(ctx, 2, 10)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireOpenAIEvalCredentialSlot(ctx, 1, peers, "background")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.DecrementAccountWaitCount(ctx, 2))
	ok, err = c.AcquireAccountSlot(ctx, 2, 2, "foreground")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = c.AcquireOpenAIEvalCredentialSlot(ctx, 1, peers, "background")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, c.ReleaseAccountSlot(ctx, 2, "foreground"))
	ok, err = c.AcquireOpenAIEvalCredentialSlot(ctx, 1, peers, "background")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestOpenAIEvalBudgetIntervalUsesPhysicalFlushTime(t *testing.T) {
	m, c := hardRPMRedis(t)
	ctx := context.Background()
	now := time.Now()
	m.SetTime(now)
	control := evalBudgetTestControl()
	for _, owner := range []string{"first", "second"} {
		d, err := c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "admit", Owner: owner, Nominal: 1, Control: control})
		require.NoError(t, err)
		require.True(t, d.Allowed)
	}
	d, err := c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "prepare", Owner: "first", SendID: "slow-dial", Control: control})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	m.SetTime(now.Add(2 * time.Second))
	op := service.OpenAIEvalBudgetOperation{Action: "prepare", Owner: "second", SendID: "next", Control: control}
	d, err = c.OpenAIEvalBudget(ctx, "fixture", op)
	require.NoError(t, err)
	require.False(t, d.Allowed, "pending dial must not allow a later send to overtake the physical interval")
	d, err = c.OpenAIEvalBudget(ctx, "fixture", service.OpenAIEvalBudgetOperation{Action: "confirm", Owner: "first", SendID: "slow-dial"})
	require.NoError(t, err)
	require.True(t, d.Allowed)
	require.Equal(t, 1, d.SentLastHour)
	d, err = c.OpenAIEvalBudget(ctx, "fixture", op)
	require.NoError(t, err)
	require.False(t, d.Allowed, "interval starts from actual write confirmation")
	m.SetTime(now.Add(3 * time.Second))
	d, err = c.OpenAIEvalBudget(ctx, "fixture", op)
	require.NoError(t, err)
	require.True(t, d.Allowed)
}
