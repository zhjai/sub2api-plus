//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type evalConcurrencyCache struct {
	ConcurrencyCache
	mu       sync.Mutex
	slots    map[string]int64
	peak     int
	blocked  chan struct{}
	cacheErr error
}

func newEvalConcurrencyCache() *evalConcurrencyCache {
	return &evalConcurrencyCache{slots: make(map[string]int64), blocked: make(chan struct{}, 1)}
}

func (c *evalConcurrencyCache) AcquireAccountSlot(ctx context.Context, accountID int64, limit int, requestID string) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cacheErr != nil {
		return false, c.cacheErr
	}
	count := 0
	for _, id := range c.slots {
		if id == accountID {
			count++
		}
	}
	if count >= limit {
		select {
		case c.blocked <- struct{}{}:
		default:
		}
		return false, nil
	}
	c.slots[requestID] = accountID
	c.peak = max(c.peak, count+1)
	return true, nil
}

func (c *evalConcurrencyCache) ReleaseAccountSlot(_ context.Context, _ int64, requestID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.slots, requestID)
	return nil
}

func (c *evalConcurrencyCache) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.slots), c.peak
}

func TestEvalModelTraceSharesRemainingAccountConcurrency(t *testing.T) {
	for _, occupied := range []int{0, 1} {
		t.Run(string(rune('0'+occupied))+" existing requests", func(t *testing.T) {
			cache := newEvalConcurrencyCache()
			concurrency := NewConcurrencyService(cache)
			if occupied > 0 {
				result, err := concurrency.AcquireAccountSlot(t.Context(), 995, 2)
				require.NoError(t, err)
				require.True(t, result.Acquired)
				defer result.ReleaseFunc()
			}
			started := make(chan struct{}, 3)
			finish := make(chan struct{})
			var once sync.Once
			releaseResponses := func() { once.Do(func() { close(finish) }) }
			defer releaseResponses()
			svc, _, upstream := evalRunHarness(t, func(req *http.Request, _ int) (*http.Response, error) {
				started <- struct{}{}
				select {
				case <-finish:
					return newJSONResponse(200, evalChatJSON(strings.Repeat("42,", 332), "stop")), nil
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			})
			account, err := svc.accounts.GetByID(t.Context(), 995)
			require.NoError(t, err)
			account.Concurrency = 2
			account.Extra["openai_responses_supported"] = false
			svc.accountTest.openaiGatewayService.concurrencyService = concurrency
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan struct {
				run *OpenAIEvalRun
				err error
			}, 1)
			go func() {
				run, err := svc.Run(ctx, OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeModelTrace}, 1, "manual")
				done <- struct {
					run *OpenAIEvalRun
					err error
				}{run, err}
			}()
			for i := 0; i < 2-occupied; i++ {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("evaluation never used remaining concurrency")
				}
			}
			select {
			case <-cache.blocked:
			case <-ctx.Done():
				t.Fatal("extra ModelTrace sample did not wait")
			}
			require.EqualValues(t, 2-occupied, upstream.calls.Load())
			releaseResponses()
			outcome := <-done
			require.NoError(t, outcome.err)
			require.Equal(t, 3, outcome.run.RequestCount)
			require.Equal(t, 3, outcome.run.Outcome.SampleCount)
			require.EqualValues(t, 3, upstream.calls.Load())
			current, peak := cache.counts()
			require.Equal(t, occupied, current)
			require.Equal(t, 2, peak)
		})
	}
}

func TestEvalFullAccountWaitIsCancellableWithoutAttempts(t *testing.T) {
	cache := newEvalConcurrencyCache()
	concurrency := NewConcurrencyService(cache)
	busy, err := concurrency.AcquireAccountSlot(t.Context(), 995, 1)
	require.NoError(t, err)
	defer busy.ReleaseFunc()
	svc, _, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, evalCompletedJSON("21")), nil
	})
	account, err := svc.accounts.GetByID(t.Context(), 995)
	require.NoError(t, err)
	account.Concurrency = 1
	svc.accountTest.openaiGatewayService.concurrencyService = concurrency
	target, err := svc.accountTest.ResolveOpenAIEvalTarget(t.Context(), 995, "gpt-5.4")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan OpenAIEvalSampleRecord, 1)
	go func() {
		_, record, _ := svc.accountTest.RunOpenAIEvalSampleAttempts(ctx, target, "probe", "", 3)
		done <- record
	}()
	select {
	case <-cache.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("evaluation did not wait for the occupied account")
	}
	cancel()
	record := <-done
	require.Equal(t, "cancelled", record.ErrorCode)
	require.Zero(t, record.Attempts)
	require.Zero(t, upstream.calls.Load())
	current, _ := cache.counts()
	require.Equal(t, 1, current)
}

func TestEvalConcurrencyCacheErrorDoesNotSendRequest(t *testing.T) {
	cache := newEvalConcurrencyCache()
	cache.cacheErr = errors.New("redis unavailable")
	svc, _, upstream := evalRunHarness(t, func(*http.Request, int) (*http.Response, error) {
		return newJSONResponse(200, evalCompletedJSON("21")), nil
	})
	svc.accountTest.openaiGatewayService.concurrencyService = NewConcurrencyService(cache)
	run, err := svc.Run(t.Context(), OpenAIEvalRunRequest{AccountID: 995, RequestedModel: "gpt-5.4", TestType: OpenAIEvalTypeCandy}, 1, "manual")
	require.NoError(t, err)
	require.Equal(t, "concurrency_unavailable", run.Samples[0].ErrorCode)
	require.Zero(t, run.RequestCount)
	require.Zero(t, upstream.calls.Load())
}
