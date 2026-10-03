package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func hardRPMHandlerContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	return c, w
}

func TestAccountHardRPMHandlerCapacityBudgetAndExhaustion(t *testing.T) {
	c, w := hardRPMHandlerContext()
	fs := NewFailoverState(1, false)
	for i := 1; i <= 2; i++ {
		err := &service.AccountRPMError{AccountID: int64(i), Decision: service.AccountRPMDecision{RetryAfter: 3 * time.Second}}
		handled, retry := fs.handleAccountRPMError(c, err, false)
		require.True(t, handled)
		require.True(t, retry)
	}
	require.Zero(t, fs.SwitchCount)
	require.Empty(t, fs.SameAccountRetryCount)
	require.Nil(t, fs.LastFailoverErr)
	require.True(t, fs.allExclusionsAreLocalVetoed())
	// Local vetoes do not trigger the single-account 503 retry/backoff loop.
	fs.LastFailoverErr = &service.UpstreamFailoverError{StatusCode: 503}
	require.Equal(t, FailoverExhausted, fs.HandleSelectionExhausted(context.Background()))
	require.Len(t, fs.FailedAccountIDs, 2)
	require.True(t, accountRPMSelectionExhausted(c, fs.FailedAccountIDs))
	require.Equal(t, 429, w.Code)
	require.Equal(t, "3", w.Header().Get("Retry-After"))
	require.Contains(t, w.Body.String(), "account_rpm_limit_exceeded")
}

func TestAccountHardRPMHandlerOwnerOutputAndUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		movable, output, unavailable, noMigration bool
		status                                    int
	}{
		{"owner", false, false, false, false, 429}, {"output", true, true, false, false, 429}, {"later_ws", true, false, false, true, 429}, {"cache", true, false, true, false, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w := hardRPMHandlerContext()
			excluded := map[int64]struct{}{}
			err := &service.AccountRPMError{AccountID: 4, Unavailable: tc.unavailable, NoMigration: tc.noMigration, Cause: errors.New("private infrastructure")}
			handled, retry := handleAccountRPMError(c, err, excluded, tc.movable, tc.output)
			require.True(t, handled)
			require.False(t, retry)
			require.Empty(t, excluded)
			require.Equal(t, tc.status, w.Code)
			require.NotContains(t, w.Body.String(), "private infrastructure")
			require.False(t, shouldReportOpenAIWSProxyAccountFailure(err))
		})
	}
	c, w := hardRPMHandlerContext()
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	handled, retry := handleAccountRPMError(c, &service.AccountRPMError{}, nil, true, false)
	require.True(t, handled)
	require.False(t, retry)
	require.Empty(t, w.Body.String())
}

func TestAccountHardRPMHandlerBoundAndMixedExclusions(t *testing.T) {
	c, _ := hardRPMHandlerContext()
	excluded := map[int64]struct{}{}
	for i := 0; i < maxAccountRPMVetoes; i++ {
		handled, retry := handleAccountRPMError(c, &service.AccountRPMError{AccountID: int64(i + 1)}, excluded, true, false)
		require.True(t, handled)
		require.Equal(t, i < maxAccountRPMVetoes-1, retry)
	}
	c, _ = hardRPMHandlerContext()
	excluded = map[int64]struct{}{99: {}}
	_, retry := handleAccountRPMError(c, &service.AccountRPMError{AccountID: 1}, excluded, true, false)
	require.True(t, retry)
	require.False(t, accountRPMSelectionExhausted(c, excluded))
}

func TestAccountHardRPMLiveErrorMapping(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		c, w := hardRPMHandlerContext()
		local := &service.AccountRPMError{AccountID: 1, Unavailable: unavailable, Decision: service.AccountRPMDecision{RetryAfter: 7 * time.Second}}
		(&OpenAIGatewayHandler{}).writeLiveCreateError(c, local)
		require.Equal(t, local.StatusCode(), w.Code)
		require.Contains(t, w.Body.String(), local.Code())
		if !unavailable {
			require.Equal(t, "7", w.Header().Get("Retry-After"))
		}
	}
}
