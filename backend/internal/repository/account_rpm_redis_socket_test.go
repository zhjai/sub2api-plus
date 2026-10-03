package repository

import (
	"context"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type hardRPMLostReplyConn struct {
	net.Conn
	drop *atomic.Bool
}

func (c *hardRPMLostReplyConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.drop.CompareAndSwap(true, false) {
		_ = c.Conn.Close()
		return 0, io.EOF
	}
	return n, err
}

func TestAccountHardRPMRealRedisSocketLostReplyAndConcurrency(t *testing.T) {
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server not installed")
	}
	socket := filepath.Join(t.TempDir(), "redis.sock")
	process := exec.Command(binary, "--port", "0", "--unixsocket", socket, "--save", "", "--appendonly", "no")
	require.NoError(t, process.Start())
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	var drop atomic.Bool
	var dials atomic.Int64
	client := redis.NewClient(&redis.Options{
		Network: "unix", Addr: socket, MaxRetries: 1, MinRetryBackoff: time.Millisecond,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			dials.Add(1)
			return &hardRPMLostReplyConn{Conn: conn, drop: &drop}, nil
		},
	})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.Eventually(t, func() bool { return client.Ping(ctx).Err() == nil }, 3*time.Second, 10*time.Millisecond)
	_, err = accountRPMAdmitScript.Load(ctx, client).Result()
	require.NoError(t, err)
	cache := &gatewayCache{rdb: client}
	before := dials.Load()
	drop.Store(true)
	decision, err := cache.AdmitAccountRPM(ctx, 7, 13, "lost-reply")
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Equal(t, 1, decision.Used)
	require.False(t, drop.Load(), "first successful Lua reply was discarded")
	require.Greater(t, dials.Load(), before, "go-redis retried on a new socket")
	var allowed, failed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, err := cache.AdmitAccountRPM(ctx, 7, 13, time.Unix(int64(i), 0).String())
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
	require.EqualValues(t, 12, allowed.Load())
	counts, err := cache.ReadAccountRPMBatch(ctx, map[int64]int{7: 13, 8: 13})
	require.NoError(t, err)
	require.Equal(t, 13, counts[7].Used)
	require.Zero(t, counts[8].Used)
	require.InDelta(t, 60, counts[7].RetryAfter.Seconds(), 3)
	ttl, err := client.PTTL(ctx, accountRPMKey(7)).Result()
	require.NoError(t, err)
	require.InDelta(t, 65, ttl.Seconds(), 3)
}

func TestAccountHardRPMRedisStalledSocketIsBounded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var peers sync.WaitGroup
	defer peers.Wait()
	defer listener.Close()
	peers.Add(1)
	go func() {
		defer peers.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			peers.Add(1)
			go func() {
				defer peers.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	client := redis.NewClient(&redis.Options{Addr: listener.Addr().String(), ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second})
	defer client.Close()
	cache := &gatewayCache{rdb: client}
	for _, snapshot := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		started := time.Now()
		if snapshot {
			_, err = cache.ReadAccountRPMBatch(ctx, map[int64]int{1: 1})
		} else {
			_, err = cache.AdmitAccountRPM(ctx, 1, 1, "stalled")
		}
		cancel()
		require.Error(t, err)
		require.Less(t, time.Since(started), time.Second, "must not inherit the shared 30-second Redis socket timeout")
	}
}
