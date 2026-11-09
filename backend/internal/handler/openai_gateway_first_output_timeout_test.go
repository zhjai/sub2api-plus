package handler

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIForwardMayFailoverOnlyAfterNonSemanticWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	before := service.OpenAICompactKeepaliveAdjustedWrittenSize(c)

	_, err := fmt.Fprint(c.Writer, ":\n\n")
	require.NoError(t, err)
	c.Writer.Flush()

	require.True(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{
		SafeToFailoverAfterWrite: true,
	}))
	require.False(t, openAIForwardMayFailover(c, before, &service.UpstreamFailoverError{}))
}

func TestOpenAIFirstOutputFailoverStopsAfterOneAccountSwitch(t *testing.T) {
	failoverErr := &service.UpstreamFailoverError{SafeToFailoverAfterWrite: true}
	count := 0

	require.False(t, openAIFirstOutputFailoverExhausted(failoverErr, &count))
	require.Equal(t, 1, count)
	require.True(t, openAIFirstOutputFailoverExhausted(failoverErr, &count))
	require.Equal(t, 1, count)
}

func TestOpenAIFirstOutputBudgetIgnoresDeferredTransportRetries(t *testing.T) {
	count := 0
	transportErr := &service.UpstreamFailoverError{
		SafeToFailoverAfterWrite: true,
		RetryBeforeSessionEscape: true,
	}
	for attempt := 0; attempt < 4; attempt++ {
		require.False(t, openAIFirstOutputFailoverExhausted(transportErr, &count))
	}
	require.Zero(t, count)
	guardErr := &service.UpstreamFailoverError{SafeToFailoverAfterWrite: true}
	require.False(t, openAIFirstOutputFailoverExhausted(guardErr, &count))
	require.True(t, openAIFirstOutputFailoverExhausted(guardErr, &count))
	require.False(t, openAIFirstOutputFailoverExhausted(transportErr, &count), "exhausted guard must not block transport recovery")
	require.Equal(t, 1, count)
}

func TestOpenAIExecProtocolLeakHasIndependentSwitchBudget(t *testing.T) {
	firstOutputCount := 1
	execLeakCount := 0
	require.False(t, openAIExecProtocolLeakFailoverExhausted(&execLeakCount))
	require.Equal(t, 1, firstOutputCount)
	require.Equal(t, 1, execLeakCount)
	require.False(t, openAIExecProtocolLeakFailoverExhausted(&execLeakCount))
	require.False(t, openAIExecProtocolLeakFailoverExhausted(&execLeakCount))
	require.True(t, openAIExecProtocolLeakFailoverExhausted(&execLeakCount))
	require.Equal(t, maxOpenAIExecProtocolLeakSwitches, execLeakCount)
}

func TestOpenAIRequestAllowsFailoverReplayStopsCanceledClient(t *testing.T) {
	require.False(t, openAIRequestAllowsFailoverReplay(nil))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	requestCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(requestCtx)

	require.True(t, openAIRequestAllowsFailoverReplay(c))
	cancel()
	require.False(t, openAIRequestAllowsFailoverReplay(c))
}
