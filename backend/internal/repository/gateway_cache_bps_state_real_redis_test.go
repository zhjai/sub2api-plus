package repository

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Exercise the production Lua/RESP contract, not an in-memory Lua emulator.
// The server has no TCP listener or persistence and never uses application config.
func bpsRealRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is required for the real Redis state contract")
	}
	socket := filepath.Join(t.TempDir(), "redis.sock")
	var output bytes.Buffer
	command := exec.Command(binary, "--port", "0", "--unixsocket", socket, "--save", "", "--appendonly", "no")
	command.Stdout, command.Stderr = &output, &output
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		if t.Failed() {
			t.Log(output.String())
		}
	})
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })
	require.Eventually(t, func() bool { return client.Ping(context.Background()).Err() == nil }, 5*time.Second, 10*time.Millisecond)
	return client
}

func TestGatewayCacheBPSStateRealRedis(t *testing.T) {
	client := bpsRealRedisClient(t)
	// Same factory/type assertion as production wire_gen and gateway preparation.
	store, ok := NewGatewayCache(client).(basispoints.StateStore)
	require.True(t, ok)
	ctx := context.Background()
	t.Run("catalog across request wrappers", func(t *testing.T) {
		replay, catalog := basispoints.NewPersistentCaches(ctx, store)
		_, _, err := basispoints.PrepareWithCatalog([]byte(`{"model":"gpt-6.1-sol","input":"hello","tools":[{"type":"custom","name":"retained_patch"}]}`), "catalog", replay, catalog)
		require.NoError(t, err)
		replay, catalog = basispoints.NewPersistentCaches(ctx, NewGatewayCache(client).(basispoints.StateStore))
		body, _, err := basispoints.PrepareWithCatalog([]byte(`{"model":"gpt-6.1-sol","input":"continue"}`), "catalog", replay, catalog)
		require.NoError(t, err)
		require.Contains(t, string(body), "retained_patch")
	})
	t.Run("exact bytes and CAS", func(t *testing.T) {
		original := []byte("{\"raw\":\"保留 < > & \\n $(literal)\",\"number\":9007199254740993}")
		written, err := store.SaveBPSState(ctx, "cas", "replay:call", nil, original)
		require.NoError(t, err)
		require.True(t, written)
		got, version, err := store.LoadBPSState(ctx, "cas", "replay:call")
		require.NoError(t, err)
		require.Equal(t, original, got)
		var winners atomic.Int64
		var workers sync.WaitGroup
		for range 8 {
			workers.Go(func() {
				written, err := store.SaveBPSState(ctx, "cas", "replay:call", &version, original)
				if err != nil {
					t.Errorf("CAS: %v", err)
				}
				if written {
					winners.Add(1)
				}
			})
		}
		workers.Wait()
		require.EqualValues(t, 1, winners.Load())
	})
	t.Run("miss integrity and capacity", func(t *testing.T) {
		got, version, err := store.LoadBPSState(ctx, "missing", "catalog")
		require.NoError(t, err)
		require.Nil(t, got)
		require.Zero(t, version)
		written, err := store.SaveBPSState(ctx, "corrupt", "catalog", nil, []byte(`[]`))
		require.NoError(t, err)
		require.True(t, written)
		_, weights := bpsStateScopeKeys("corrupt")
		require.NoError(t, client.Del(ctx, weights).Err())
		_, _, err = store.LoadBPSState(ctx, "corrupt", "catalog")
		require.ErrorIs(t, err, basispoints.ErrStateUnavailable)
		written, err = store.SaveBPSState(ctx, "capacity", "catalog", nil, make([]byte, (1<<20)+1))
		require.False(t, written)
		require.ErrorIs(t, err, basispoints.ErrStateCapacity)
	})
}
