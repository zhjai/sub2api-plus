package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func bpsRedisStore(t *testing.T, mr *miniredis.Miniredis) basispoints.StateStore {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })
	return NewGatewayCache(client).(basispoints.StateStore)
}

func TestGatewayCacheBPSStateSharedScopeAndExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	writer, reader := bpsRedisStore(t, mr), bpsRedisStore(t, mr)
	ctx := context.Background()
	zero := uint64(0)
	raw := []byte(`[{"name":"tool","description":"保留原始字节 < > &"}]`)
	ok, err := writer.SaveBPSState(ctx, "account|key|session", "catalog", &zero, raw)
	require.NoError(t, err)
	require.True(t, ok)
	got, revision, err := reader.LoadBPSState(ctx, "account|key|session", "catalog")
	require.NoError(t, err)
	require.Equal(t, raw, got)
	require.NotZero(t, revision)
	for _, scope := range []string{"other-account|key|session", "account|other-key|session", "account|key|other-session"} {
		got, version, err := reader.LoadBPSState(ctx, scope, "catalog")
		require.NoError(t, err)
		require.Nil(t, got)
		require.Zero(t, version)
	}
	mr.FastForward(bpsStateTTL - time.Minute)
	_, _, err = reader.LoadBPSState(ctx, "account|key|session", "catalog")
	require.NoError(t, err)
	require.Equal(t, bpsStateTTL, mr.TTL(bpsStateKey("account|key|session", "catalog")))
	mr.FastForward(bpsStateTTL)
	got, version, err := reader.LoadBPSState(ctx, "account|key|session", "catalog")
	require.NoError(t, err)
	require.Nil(t, got)
	require.Zero(t, version)
	// A recreated entry must not reuse the old revision after expiry.
	ok, err = writer.SaveBPSState(ctx, "account|key|session", "catalog", &zero, []byte(`[]`))
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = writer.SaveBPSState(ctx, "account|key|session", "catalog", &revision, raw)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestGatewayCacheBPSStateConcurrentCAS(t *testing.T) {
	mr := miniredis.RunT(t)
	store := bpsRedisStore(t, mr)
	ctx := context.Background()
	ok, err := store.SaveBPSState(ctx, "scope", "catalog", nil, []byte(`[]`))
	require.NoError(t, err)
	require.True(t, ok)
	_, revision, err := store.LoadBPSState(ctx, "scope", "catalog")
	require.NoError(t, err)
	var winners atomic.Int64
	errors := make(chan error, 16)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			ok, err := store.SaveBPSState(ctx, "scope", "catalog", &revision, []byte(fmt.Sprintf(`[{"name":"tool%d"}]`, i)))
			if ok {
				winners.Add(1)
			}
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, winners.Load())
}

func TestGatewayCacheBPSStateScopeBudgetsAndExpiryReclamation(t *testing.T) {
	t.Run("entries", func(t *testing.T) {
		mr := miniredis.RunT(t)
		now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
		mr.SetTime(now)
		store := bpsRedisStore(t, mr)
		ctx := context.Background()
		ok, err := store.SaveBPSState(ctx, "scope", "catalog", nil, []byte(`[]`))
		require.NoError(t, err)
		require.True(t, ok)
		for i := range bpsStateScopeEntries - 1 {
			now = now.Add(time.Millisecond)
			mr.SetTime(now)
			ok, err := store.SaveBPSState(ctx, "scope", fmt.Sprintf("replay:%d", i), nil, []byte(`{}`))
			require.NoError(t, err)
			require.True(t, ok)
		}
		// A read refreshes LRU order. Catalog remains protected even though
		// it is older than every replay, and replay:1 is now the oldest replay.
		now = now.Add(time.Millisecond)
		mr.SetTime(now)
		_, _, err = store.LoadBPSState(ctx, "scope", "replay:0")
		require.NoError(t, err)
		ok, err = store.SaveBPSState(ctx, "scope", "replay:overflow", nil, []byte(`{}`))
		require.NoError(t, err)
		require.True(t, ok)
		require.False(t, mr.Exists(bpsStateKey("scope", "replay:1")))
		require.True(t, mr.Exists(bpsStateKey("scope", "replay:0")))
		require.True(t, mr.Exists(bpsStateKey("scope", "catalog")))
		index, _ := bpsStateScopeKeys("scope")
		members, err := mr.ZMembers(index)
		require.NoError(t, err)
		require.Len(t, members, bpsStateScopeEntries)
		ok, err = store.SaveBPSState(ctx, "other-scope", "replay:overflow", nil, []byte(`{}`))
		require.NoError(t, err)
		require.True(t, ok)
		// Keep one entry alive while the rest age out, so metadata remains and
		// the write must actually reclaim the expired members of the budget.
		advance := func(d time.Duration) { now = now.Add(d); mr.SetTime(now); mr.FastForward(d) }
		advance(bpsStateTTL - time.Minute)
		_, _, err = store.LoadBPSState(ctx, "scope", "replay:0")
		require.NoError(t, err)
		advance(time.Minute + time.Second)
		ok, err = store.SaveBPSState(ctx, "scope", "replay:after-expiry", nil, []byte(`{}`))
		require.NoError(t, err)
		require.True(t, ok)
		members, err = mr.ZMembers(index)
		require.NoError(t, err)
		require.Len(t, members, 2)
	})
	t.Run("bytes", func(t *testing.T) {
		mr := miniredis.RunT(t)
		store := bpsRedisStore(t, mr)
		ctx := context.Background()
		raw := []byte(strings.Repeat("x", 1<<20))
		for i := range 16 {
			ok, err := store.SaveBPSState(ctx, "scope", fmt.Sprintf("replay:%d", i), nil, raw)
			require.NoError(t, err)
			require.True(t, ok)
		}
		ok, err := store.SaveBPSState(ctx, "scope", "catalog", nil, []byte(`[]`))
		require.NoError(t, err)
		require.True(t, ok)
		_, weights := bpsStateScopeKeys("scope")
		require.Equal(t, fmt.Sprint(15*(1<<20)+2), mr.HGet(weights, "total"))
		// Replacing an existing entry releases its original byte weight.
		ok, err = store.SaveBPSState(ctx, "scope", "replay:0", nil, []byte(`{}`))
		require.NoError(t, err)
		require.True(t, ok)
		ok, err = store.SaveBPSState(ctx, "scope", "catalog", nil, []byte(`[]`))
		require.NoError(t, err)
		require.True(t, ok)
	})
	t.Run("replacement-protects-current-and-catalog", func(t *testing.T) {
		mr := miniredis.RunT(t)
		store := bpsRedisStore(t, mr)
		ctx := context.Background()
		now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
		write := func(item string, raw []byte) {
			t.Helper()
			now = now.Add(time.Millisecond)
			mr.SetTime(now)
			ok, err := store.SaveBPSState(ctx, "scope", item, nil, raw)
			require.NoError(t, err)
			require.True(t, ok)
		}
		large := []byte(strings.Repeat("x", 1<<20))
		write("catalog", large)
		write("replay:current", []byte("x"))
		for i := range 14 {
			write(fmt.Sprintf("replay:large-%d", i), large)
		}
		write("replay:tail", large[:len(large)-1])
		_, weights := bpsStateScopeKeys("scope")
		require.Equal(t, fmt.Sprint(bpsStateScopeBytes), mr.HGet(weights, "total"))
		write("replay:current", large)
		require.Equal(t, string(large), mr.HGet(bpsStateKey("scope", "catalog"), "raw"))
		require.Equal(t, string(large), mr.HGet(bpsStateKey("scope", "replay:current"), "raw"))
		require.False(t, mr.Exists(bpsStateKey("scope", "replay:large-0")))
		require.Equal(t, fmt.Sprint(bpsStateScopeBytes-1), mr.HGet(weights, "total"))
	})
}

func TestGatewayCacheBPSStateLimitsAndCorruption(t *testing.T) {
	mr := miniredis.RunT(t)
	store := bpsRedisStore(t, mr)
	ctx := context.Background()
	for _, tc := range []struct {
		item string
		size int
	}{{"catalog", (1 << 20) + 1}, {"replay:call", bpsStateMaxBytes + 1}} {
		ok, err := store.SaveBPSState(ctx, "scope", tc.item, nil, []byte(strings.Repeat("x", tc.size)))
		require.False(t, ok)
		require.ErrorIs(t, err, basispoints.ErrStateCapacity)
	}
	key := bpsStateKey("scope", "catalog")
	mr.HSet(key, "raw", strings.Repeat("x", bpsStateMaxBytes+1), "version", "1")
	_, _, err := store.LoadBPSState(ctx, "scope", "catalog")
	require.ErrorIs(t, err, basispoints.ErrStateCapacity)
	mr.Del(key)
	mr.HSet(key, "raw", `[]`)
	_, _, err = store.LoadBPSState(ctx, "scope", "catalog")
	require.ErrorIs(t, err, basispoints.ErrStateUnavailable)
	ok, err := store.SaveBPSState(ctx, "scope", "catalog", nil, []byte(`[]`))
	require.False(t, ok)
	require.ErrorIs(t, err, basispoints.ErrStateUnavailable)
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = store.LoadBPSState(ctx, "other", "catalog")
	require.ErrorIs(t, err, basispoints.ErrStateUnavailable)
}

func TestGatewayCacheBPSStatePersistentCatalogOutageRecovery(t *testing.T) {
	mr := miniredis.RunT(t)
	store := bpsRedisStore(t, mr)
	ctx := context.Background()
	declared := []byte(`{"model":"gpt-6.1-sol","input":"hello","tools":[{"type":"function","name":"durable_tool","parameters":{"type":"object","properties":{}}}]}`)
	inherited := []byte(`{"model":"gpt-6.1-sol","input":"continue"}`)
	replay, catalog := basispoints.NewPersistentCaches(ctx, store)
	_, _, err := basispoints.PrepareWithCatalog(declared, "scope", replay, catalog)
	require.NoError(t, err)
	// A separate cache and Redis client simulate another replica/process.
	replay, catalog = basispoints.NewPersistentCaches(ctx, bpsRedisStore(t, mr))
	body, _, err := basispoints.PrepareWithCatalog(inherited, "scope", replay, catalog)
	require.NoError(t, err)
	require.Contains(t, string(body), "durable_tool")
	mr.Close()
	failedReplay, failedCatalog := basispoints.NewPersistentCaches(ctx, store)
	_, _, err = basispoints.PrepareWithCatalog(inherited, "scope", failedReplay, failedCatalog)
	require.ErrorIs(t, err, basispoints.ErrStateUnavailable)
	require.NoError(t, mr.Restart())
	// Failed wrappers stay failed, while a fresh request can recover immediately.
	_, _, err = basispoints.PrepareWithCatalog(inherited, "scope", failedReplay, failedCatalog)
	require.ErrorIs(t, err, basispoints.ErrStateUnavailable)
	replay, catalog = basispoints.NewPersistentCaches(ctx, store)
	body, _, err = basispoints.PrepareWithCatalog(inherited, "scope", replay, catalog)
	require.NoError(t, err)
	require.Contains(t, string(body), "durable_tool")
}

func TestGatewayCacheBPSStateReplayPersistsBeforeToolDelivery(t *testing.T) {
	for _, outage := range []bool{false, true} {
		t.Run(fmt.Sprint("outage=", outage), func(t *testing.T) {
			mr := miniredis.RunT(t)
			store := bpsRedisStore(t, mr)
			replay, catalog := basispoints.NewPersistentCaches(context.Background(), store)
			_, bridge, err := basispoints.PrepareWithCatalog([]byte(`{"model":"gpt-6.1-sol","input":"hello","tools":[{"type":"custom","name":"patch"}]}`), "scope", replay, catalog)
			require.NoError(t, err)
			payload := "*** Begin Patch\n*** Add File: exact.txt\n+保留 < > &\n*** End Patch"
			code, err := json.Marshal(map[string]any{"name": "patch", "input": payload})
			require.NoError(t, err)
			arguments, err := json.Marshal(map[string]any{"code": string(code), "summary": "Patch", "extended_summary": "Original envelope", "destructive": false, "references": []any{}})
			require.NoError(t, err)
			native := map[string]any{"type": "function_call", "id": "fc_original", "call_id": "call_original", "name": "run_officejs", "arguments": string(arguments), "status": "completed"}
			event, err := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_original", "output": []any{native}}})
			require.NoError(t, err)
			if outage {
				mr.Close()
			}
			stream := bridge.Stream(io.NopCloser(strings.NewReader("event: response.completed\ndata: " + string(event) + "\n\n")))
			output, err := io.ReadAll(stream)
			_ = stream.Close()
			require.NoError(t, err)
			if outage {
				require.Contains(t, string(output), "response.failed")
				require.NotContains(t, string(output), `"type":"custom_tool_call"`)
				require.ErrorIs(t, replay.Err(), basispoints.ErrStateUnavailable)
				return
			}
			require.NotContains(t, string(output), "response.failed")
			var clientCall map[string]any
			for _, line := range strings.Split(string(output), "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var data map[string]any
				require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data))
				if data["type"] == "response.completed" {
					clientCall = data["response"].(map[string]any)["output"].([]any)[0].(map[string]any)
				}
			}
			require.NotNil(t, clientCall)
			require.Equal(t, payload, clientCall["input"])
			raw, _, err := store.LoadBPSState(context.Background(), "scope", "replay:call_original")
			require.NoError(t, err)
			require.NotEmpty(t, raw)
			// A new replica must restore the exact original upstream envelope,
			// including its summaries, rather than reconstruct a different call.
			replay, catalog = basispoints.NewPersistentCaches(context.Background(), bpsRedisStore(t, mr))
			continuation, err := json.Marshal(map[string]any{"model": "gpt-6.1-sol", "input": []any{clientCall, map[string]any{"type": "custom_tool_call_output", "call_id": "call_original", "output": "done"}}})
			require.NoError(t, err)
			body, _, err := basispoints.PrepareWithCatalog(continuation, "scope", replay, catalog)
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(body, &wire))
			items := wire["input"].([]any)
			require.Equal(t, native, items[len(items)-2])
		})
	}
}

func TestGatewayCacheBPSStateFullSessionContinuesAndRebuildsEvictedReplay(t *testing.T) {
	mr := miniredis.RunT(t)
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	mr.SetTime(now)
	ctx := context.Background()
	store := bpsRedisStore(t, mr)
	prepare := func(raw []byte) ([]byte, *basispoints.Bridge) {
		t.Helper()
		replay, catalog := basispoints.NewPersistentCaches(ctx, store)
		body, bridge, err := basispoints.PrepareWithCatalog(raw, "scope", replay, catalog)
		require.NoError(t, err)
		return body, bridge
	}
	deliver := func(bridge *basispoints.Bridge, id, payload string) map[string]any {
		t.Helper()
		code, err := json.Marshal(map[string]any{"name": "patch", "input": payload})
		require.NoError(t, err)
		arguments, err := json.Marshal(map[string]any{"code": string(code), "summary": "Patch", "extended_summary": "Native summary", "destructive": false, "references": []any{}})
		require.NoError(t, err)
		native := map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": id, "name": "run_officejs", "arguments": string(arguments), "status": "completed"}
		event, err := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_" + id, "output": []any{native}}})
		require.NoError(t, err)
		stream := bridge.Stream(io.NopCloser(strings.NewReader("event: response.completed\ndata: " + string(event) + "\n\n")))
		output, err := io.ReadAll(stream)
		_ = stream.Close()
		require.NoError(t, err)
		require.NotContains(t, string(output), "response.failed")
		for _, line := range strings.Split(string(output), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var data map[string]any
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data))
			if data["type"] == "response.completed" {
				call := data["response"].(map[string]any)["output"].([]any)[0].(map[string]any)
				require.Equal(t, payload, call["input"])
				return call
			}
		}
		t.Fatal("stream did not deliver completed tool call")
		return nil
	}
	_, bridge := prepare([]byte(`{"model":"gpt-6.1-sol","input":"hello","tools":[{"type":"custom","name":"patch"}]}`))
	oldPayload := "*** Begin Patch\n*** Add File: old.txt\n+保留 < > &\n*** End Patch"
	oldCall := deliver(bridge, "old", oldPayload)
	// Fill the remaining replay budget. The first real call is the oldest.
	for i := range bpsStateScopeEntries - 2 {
		now = now.Add(time.Millisecond)
		mr.SetTime(now)
		ok, err := store.SaveBPSState(ctx, "scope", fmt.Sprintf("replay:filler-%d", i), nil, []byte(`{}`))
		require.NoError(t, err)
		require.True(t, ok)
	}
	now = now.Add(time.Millisecond)
	mr.SetTime(now)
	body, bridge := prepare([]byte(`{"model":"gpt-6.1-sol","input":"continue"}`))
	require.Contains(t, string(body), "patch")
	newPayload := "*** Begin Patch\n*** Add File: new.txt\n+next\n*** End Patch"
	newCall := deliver(bridge, "new", newPayload)
	require.False(t, mr.Exists(bpsStateKey("scope", "replay:old")))
	require.True(t, mr.Exists(bpsStateKey("scope", "replay:new")))
	require.True(t, mr.Exists(bpsStateKey("scope", "catalog")))
	index, weights := bpsStateScopeKeys("scope")
	members, err := mr.ZMembers(index)
	require.NoError(t, err)
	require.Len(t, members, bpsStateScopeEntries)
	// Next request includes complete calls and results. The evicted native
	// envelope is reconstructed while exact client executable bytes survive.
	history, err := json.Marshal(map[string]any{"model": "gpt-6.1-sol", "input": []any{
		oldCall, map[string]any{"type": "custom_tool_call_output", "call_id": "old", "output": "old done"},
		newCall, map[string]any{"type": "custom_tool_call_output", "call_id": "new", "output": "new done"},
	}})
	require.NoError(t, err)
	body, _ = prepare(history)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(body, &wire))
	items := wire["input"].([]any)
	for i, want := range []string{oldPayload, newPayload} {
		call := items[len(items)-4+i*2].(map[string]any)
		require.Equal(t, "run_officejs", call["name"])
		var args, code map[string]any
		require.NoError(t, json.Unmarshal([]byte(call["arguments"].(string)), &args))
		if i == 0 {
			// Rebuilt custom calls use today's raw custom transport.
			require.Equal(t, "codex2api.custom/patch", args["summary"])
			require.Equal(t, want, args["code"])
		} else {
			// The retained call keeps its exact original JSON envelope.
			require.NoError(t, json.Unmarshal([]byte(args["code"].(string)), &code))
			require.Equal(t, want, code["input"])
			require.Equal(t, "patch", code["name"])
		}
		require.Equal(t, call["call_id"], items[len(items)-3+i*2].(map[string]any)["call_id"])
	}
	var countedBytes int
	members, err = mr.ZMembers(index)
	require.NoError(t, err)
	require.LessOrEqual(t, len(members), bpsStateScopeEntries)
	for _, key := range members {
		countedBytes += len(mr.HGet(key, "raw"))
	}
	require.LessOrEqual(t, countedBytes, bpsStateScopeBytes)
	require.Equal(t, fmt.Sprint(countedBytes), mr.HGet(weights, "total"))
}
